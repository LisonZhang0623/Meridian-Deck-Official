package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// A transport belongs to all of its terminals, not to the first terminal that
// opened it. This is also important when the initial terminal is closed during
// a later key exchange.
type sshTransport struct {
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	closed       bool
	connections  []net.Conn
	clients      []*ssh.Client
	fingerprints []string
}

func (t *sshTransport) addConnection(conn net.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		_ = conn.Close()
		return false
	}
	t.connections = append(t.connections, conn)
	return true
}

func (t *sshTransport) addClient(client *ssh.Client, fingerprint string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		_ = client.Close()
		return false
	}
	t.clients = append(t.clients, client)
	t.fingerprints = append(t.fingerprints, fingerprint)
	return true
}

func (t *sshTransport) close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	clients, connections := append([]*ssh.Client(nil), t.clients...), append([]net.Conn(nil), t.connections...)
	t.mu.Unlock()
	t.cancel()
	for _, client := range clients {
		_ = client.Close()
	}
	for _, conn := range connections {
		_ = conn.Close()
	}
}

type sshVerification struct {
	host, fingerprint string
	response          chan bool
}
type managedSession struct {
	id, generation string
	profile        Profile
	transport      *sshTransport
	client         *ssh.Client
	ctx            context.Context
	cancel         context.CancelFunc
	phase          string
	channel        *ssh.Session
	stdin          io.WriteCloser
	sftp           *sftp.Client
	pending        *sshVerification
	challenge      *sshChallenge
	tmuxName       string
	inputMu        sync.Mutex
}

type SSHManager struct {
	mu sync.RWMutex
	// Connect and all session emissions share this gate. A reused ID cannot be
	// installed while an older generation's emit callback is in flight. Emit
	// callbacks may inspect the manager but must not synchronously mutate it.
	eventGate    sync.Mutex
	sessions     map[string]*managedSession
	store        TrustStore
	emit         Emit
	emitTerminal func(Event) <-chan struct{}
	closed       bool
}

// emitTerminal publishes an ordered frame and returns its acknowledgement
// channel without waiting. A nil callback uses emit without acknowledgement.
func NewSSHManager(store TrustStore, emit Emit, emitTerminal func(Event) <-chan struct{}) *SSHManager {
	if emit == nil {
		emit = func(Event) {}
	}
	if emitTerminal == nil {
		emitTerminal = func(event Event) <-chan struct{} {
			emit(event)
			return nil
		}
	}
	return &SSHManager{sessions: make(map[string]*managedSession), store: store, emit: emit, emitTerminal: emitTerminal}
}

func (m *SSHManager) Connect(id string, profile Profile, credentials Credentials, jump *Jump) error {
	if jump == nil {
		return m.ConnectChain(id, profile, credentials, nil)
	}
	return m.ConnectChain(id, profile, credentials, []Jump{*jump})
}

func (m *SSHManager) ConnectChain(id string, profile Profile, credentials Credentials, jumps []Jump) error {
	return m.connectChain(id, profile, credentials, jumps, "")
}

func (m *SSHManager) ConnectTmux(id string, profile Profile, credentials Credentials, jumps []Jump, name string) error {
	if !validTmuxName(name) {
		return errors.New("tmux 会话名称须为 1–64 个字母、数字、短横线或下划线")
	}
	return m.connectChain(id, profile, credentials, jumps, name)
}

func (m *SSHManager) connectChain(id string, profile Profile, credentials Credentials, jumps []Jump, tmuxName string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if len(jumps) > maxJumpHops {
		return errors.New("最多支持 5 跳 SSH 跳板机")
	}
	seen, next := map[string]bool{profile.ID: true}, profile.JumpProfileID
	for _, hop := range jumps {
		if next == "" || hop.Profile.ID != next || seen[next] {
			return errors.New("跳板机链路无效或存在循环引用")
		}
		seen[next] = true
		next = hop.Profile.JumpProfileID
	}
	if next != "" {
		return errors.New("跳板机链路配置不完整")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &sshTransport{ctx: ctx, cancel: cancel}
	sessionCtx, sessionCancel := context.WithCancel(ctx)
	s := &managedSession{id: id, generation: NewID(), profile: profile, transport: t, ctx: sessionCtx, cancel: sessionCancel, phase: "connecting", tmuxName: tmuxName}
	m.eventGate.Lock()
	m.mu.Lock()
	if m.closed || len(m.sessions) >= 100 {
		m.mu.Unlock()
		m.eventGate.Unlock()
		cancel()
		return errors.New("SSH 服务已停止或终端数量达到上限")
	}
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		m.eventGate.Unlock()
		cancel()
		return errors.New("会话已存在")
	}
	m.sessions[id] = s
	m.mu.Unlock()
	m.emit(Event{"type": "state", "sessionId": id, "generation": s.generation, "phase": "connecting", "message": "正在连接服务器"})
	m.eventGate.Unlock()
	go func() {
		if err := m.connectRoute(s, credentials, jumps); err != nil {
			m.stopTransport(t, "failed", sshMessage(err))
		}
	}()
	return nil
}

func (m *SSHManager) connectRoute(s *managedSession, credentials Credentials, jumps []Jump) error {
	var previous *ssh.Client
	for i := len(jumps) - 1; i >= 0; i-- {
		hop := jumps[i]
		label := fmt.Sprintf("跳板机 %d/%d · %s", len(jumps)-i, len(jumps), hop.Profile.Name)
		client, err := m.openTransport(s, hop.Profile, hop.Credentials, previous, label)
		if err != nil {
			return fmt.Errorf("%s：%s", label, sshMessage(err))
		}
		previous = client
	}
	client, err := m.openTransport(s, s.profile, credentials, previous, "服务器")
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.sessions[s.id] != s {
		m.mu.Unlock()
		return context.Canceled
	}
	s.client = client
	m.mu.Unlock()
	return m.openTerminal(s, nil, false)
}

func profileTimeout(p Profile) time.Duration {
	seconds := p.Timeout
	if seconds < 5 || seconds > 120 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func (m *SSHManager) openTransport(s *managedSession, profile Profile, credentials Credentials, previous *ssh.Client, label string) (*ssh.Client, error) {
	t := s.transport
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	address := net.JoinHostPort(profile.Host, strconv.Itoa(profile.Port))
	ctx, cancel := context.WithTimeout(t.ctx, profileTimeout(profile))
	conn, err := dialSSHAddress(ctx, previous, profile.ProxyAddress, address)
	cancel()
	if err != nil {
		return nil, err
	}
	if !t.addConnection(conn) {
		return nil, context.Canceled
	}
	// Deadlines are not implemented by direct-tcpip channels. Closing the
	// connection on timeout works both for TCP and for a jump channel.
	timer := time.AfterFunc(profileTimeout(profile), func() { _ = conn.Close() })
	defer timer.Stop()
	auth, closeAuth, err := m.authMethods(s, profile, credentials, label, timer)
	defer closeAuth()
	if err != nil {
		return nil, err
	}
	verifiedFingerprint := ""
	config := &ssh.ClientConfig{User: profile.Username, Auth: auth, Timeout: profileTimeout(profile)}
	config.HostKeyCallback = func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		if verifiedFingerprint != "" {
			if fingerprint != verifiedFingerprint {
				return errors.New(label + "在密钥更新时改变了主机指纹，请重新连接并核对服务器身份")
			}
			return nil
		}
		host := "[" + strings.ToLower(profile.Host) + "]:" + strconv.Itoa(profile.Port)
		previous := m.store.Fingerprint(host)
		if previous != fingerprint {
			timer.Stop() // Waiting for the user's fingerprint decision has no login timeout.
			pending := &sshVerification{host: host, fingerprint: fingerprint, response: make(chan bool, 1)}
			m.eventGate.Lock()
			m.mu.Lock()
			if m.sessions[s.id] != s {
				m.mu.Unlock()
				m.eventGate.Unlock()
				return context.Canceled
			}
			s.pending = pending
			s.phase = "verifying"
			m.mu.Unlock()
			m.emit(Event{"type": "state", "sessionId": s.id, "generation": s.generation, "phase": "verifying", "message": label + "身份待核验，请确认主机指纹"})
			event := Event{"type": "hostkey", "sessionId": s.id, "generation": s.generation, "host": host, "fingerprint": fingerprint, "keyType": key.Type()}
			if previous != "" {
				event["previous"] = previous
			}
			m.emit(event)
			m.eventGate.Unlock()
			select {
			case accept := <-pending.response:
				if !accept {
					return errors.New("已取消服务器身份确认")
				}
			case <-t.ctx.Done():
				return t.ctx.Err()
			}
			timer.Reset(profileTimeout(profile))
		}
		verifiedFingerprint = fingerprint
		m.state(s, "authenticating", label+"身份已核验，正在登录", nil)
		return nil
	}
	connection, channels, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	client := ssh.NewClient(connection, channels, requests)
	if !t.addClient(client, verifiedFingerprint) {
		return nil, context.Canceled
	}
	go func() {
		err := client.Wait()
		message := label + "连接已断开，终端输出已保留"
		if err != nil && !errors.Is(err, io.EOF) {
			message += "：" + sshMessage(err)
		}
		m.stopTransport(t, "disconnected", message)
	}()
	if profile.Keepalive {
		go m.keepalive(t, client)
	}
	return client, nil
}

func (m *SSHManager) keepalive(t *sshTransport, client *ssh.Client) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			result := make(chan error, 1)
			go func() { _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); result <- err }()
			select {
			case err := <-result:
				if err != nil {
					m.stopTransport(t, "disconnected", "SSH 保活失败，请重新连接")
					return
				}
			case <-time.After(10 * time.Second):
				m.stopTransport(t, "disconnected", "SSH 保活超时，请重新连接")
				return
			case <-t.ctx.Done():
				return
			}
		}
	}
}

func (m *SSHManager) Trust(id string, accept bool) error {
	return m.trust(id, "", "", accept)
}

func (m *SSHManager) TrustHostKey(id, host, fingerprint string, accept bool) error {
	if host == "" || fingerprint == "" {
		return errors.New("无效的服务器身份确认")
	}
	return m.trust(id, host, fingerprint, accept)
}

func (m *SSHManager) trust(id, host, fingerprint string, accept bool) error {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil || s.pending == nil {
		m.mu.Unlock()
		if host != "" {
			return errors.New("此服务器指纹提示已失效")
		}
		return nil
	}
	pending := s.pending
	if host != "" && (pending.host != host || pending.fingerprint != fingerprint) {
		m.mu.Unlock()
		return errors.New("当前服务器指纹已变化，请核对新的提示")
	}
	s.pending = nil
	m.mu.Unlock()
	if accept {
		if err := m.store.Trust(pending.host, pending.fingerprint); err != nil {
			pending.response <- false
			m.stop(s, "failed", "无法保存服务器指纹")
			return err
		}
	}
	pending.response <- accept
	if !accept {
		m.stop(s, "disconnected", "已取消连接")
	}
	return nil
}

func (m *SSHManager) NewTerminal(id, sourceID string, profile Profile, initialPath *string) error {
	return m.newTerminal(id, sourceID, profile, initialPath, "")
}

func (m *SSHManager) NewTmuxTerminal(id, sourceID string, profile Profile, name string) error {
	if !validTmuxName(name) {
		return errors.New("tmux 会话名称须为 1–64 个字母、数字、短横线或下划线")
	}
	return m.newTerminal(id, sourceID, profile, nil, name)
}

func (m *SSHManager) newTerminal(id, sourceID string, profile Profile, initialPath *string, tmuxName string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if initialPath != nil {
		if err := ValidateRemotePath(*initialPath); err != nil {
			return err
		}
	}
	m.eventGate.Lock()
	defer m.eventGate.Unlock()
	m.mu.Lock()
	source := m.sessions[sourceID]
	if m.closed || source == nil || source.phase != "connected" {
		m.mu.Unlock()
		return errors.New("原连接已断开，请重新连接服务器")
	}
	if len(m.sessions) >= 100 {
		m.mu.Unlock()
		return errors.New("终端数量达到上限")
	}
	if ProfileIdentity(source.profile) != ProfileIdentity(profile) {
		m.mu.Unlock()
		return errors.New("连接身份已变化，请重新连接服务器")
	}
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		return errors.New("会话已存在")
	}
	ctx, cancel := context.WithCancel(source.transport.ctx)
	s := &managedSession{id: id, generation: NewID(), profile: profile, transport: source.transport, client: source.client, ctx: ctx, cancel: cancel, phase: "authenticating", tmuxName: tmuxName}
	m.sessions[id] = s
	m.mu.Unlock()
	m.emit(Event{"type": "state", "sessionId": s.id, "generation": s.generation, "phase": "authenticating", "message": "正在打开新的终端"})
	go func() {
		if err := m.openTerminal(s, initialPath, true); err != nil {
			m.stop(s, "failed", sshMessage(err))
		}
	}()
	return nil
}

func openSSHSession(ctx context.Context, client *ssh.Client) (*ssh.Session, error) {
	type result struct {
		session *ssh.Session
		err     error
	}
	done := make(chan result)
	go func() {
		session, err := client.NewSession()
		select {
		case done <- result{session, err}:
		case <-ctx.Done():
			if session != nil {
				_ = session.Close()
			}
		}
	}()
	select {
	case value := <-done:
		return value.session, value.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// The peer may ignore both EOF and CHANNEL_CLOSE while other channels on the
// same SSH transport remain useful. Each SFTP channel therefore owns locally
// cancellable read/write pipes. The library's Close can finish without a remote
// acknowledgement; only this channel is closed, never its shared transport.
//
// Stalled SSH readers/writers cannot be interrupted without closing the whole
// transport. Keep their slots until every actual worker exits. 128 bounds the
// whole process (100 terminal file trees plus transfer capacity), including
// malicious peers that repeatedly ignore channel closes.
const maxSFTPChannels = 128

var sftpChannelWorkers = make(chan struct{}, maxSFTPChannels)

type sftpWriteResult struct {
	n   int
	err error
}
type sftpWriteRequest struct {
	data []byte
	done chan sftpWriteResult
}
type sftpLocalChannel struct {
	reader        *io.PipeReader
	writer        *io.PipeWriter
	session       *ssh.Session
	writeRequests chan sftpWriteRequest
	done          chan struct{}
	closeDone     chan struct{}
	once          sync.Once
	cancel        context.CancelFunc
}

func (c *sftpLocalChannel) Write(data []byte) (int, error) {
	// sftp serializes packets, so at most one copied request per channel waits
	// behind its writer. The default packet size is 32 KiB.
	if len(data) > 2*1024*1024 {
		return 0, errors.New("SFTP 写入包超过大小限制")
	}
	request := sftpWriteRequest{data: append([]byte(nil), data...), done: make(chan sftpWriteResult, 1)}
	select {
	case <-c.done:
		return 0, io.ErrClosedPipe
	default:
	}
	select {
	case c.writeRequests <- request:
	case <-c.done:
		return 0, io.ErrClosedPipe
	}
	select {
	case result := <-request.done:
		return result.n, result.err
	case <-c.done:
		return 0, io.ErrClosedPipe
	}
}
func (c *sftpLocalChannel) Close() error {
	c.once.Do(func() {
		close(c.done)
		_ = c.reader.CloseWithError(io.ErrClosedPipe)
		_ = c.writer.CloseWithError(io.ErrClosedPipe)
		c.cancel()
		go func() { defer close(c.closeDone); _ = c.session.Close() }()
	})
	return nil
}

// ctx governs the returned client's lifetime as well as opening. Terminal
// setup has a shorter opening deadline and uses openSFTPWithLifetime below.
func openSFTP(ctx context.Context, client *ssh.Client) (*sftp.Client, error) {
	return openSFTPWithLifetime(ctx, ctx, client)
}

func openSFTPWithLifetime(openCtx, lifetimeCtx context.Context, client *ssh.Client) (*sftp.Client, error) {
	opening, cancelOpening := context.WithTimeout(openCtx, 10*time.Second)
	defer cancelOpening()
	if err := opening.Err(); err != nil {
		return nil, err
	}
	select {
	case sftpChannelWorkers <- struct{}{}:
	case <-opening.Done():
		return nil, opening.Err()
	}
	ctx, cancel := context.WithCancel(lifetimeCtx)
	type result struct {
		client *sftp.Client
		err    error
	}
	ready := make(chan result)
	go func() {
		handedOff := false
		defer func() {
			if !handedOff {
				<-sftpChannelWorkers
			}
		}()
		channel, err := client.NewSession()
		if err != nil {
			select {
			case ready <- result{err: err}:
			case <-opening.Done():
			}
			cancel()
			return
		}
		if opening.Err() != nil || ctx.Err() != nil {
			cancel()
			_ = channel.Close()
			return
		}
		stdin, err := channel.StdinPipe()
		if err != nil {
			cancel()
			_ = channel.Close()
			select {
			case ready <- result{err: err}:
			case <-opening.Done():
			}
			return
		}
		stdout, err := channel.StdoutPipe()
		if err != nil {
			cancel()
			_ = channel.Close()
			select {
			case ready <- result{err: err}:
			case <-opening.Done():
			}
			return
		}
		stderr, err := channel.StderrPipe()
		if err != nil {
			cancel()
			_ = channel.Close()
			select {
			case ready <- result{err: err}:
			case <-opening.Done():
			}
			return
		}
		reader, writer := io.Pipe()
		local := &sftpLocalChannel{reader: reader, writer: writer, session: channel, writeRequests: make(chan sftpWriteRequest), done: make(chan struct{}), closeDone: make(chan struct{}), cancel: cancel}
		stdoutDone, stderrDone, writerDone, openingDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		handedOff = true
		defer close(openingDone)
		go func() { defer close(stdoutDone); _, _ = io.Copy(writer, stdout); _ = local.Close() }()
		go func() { defer close(stderrDone); _, _ = io.Copy(io.Discard, stderr) }()
		go func() {
			defer close(writerDone)
			for {
				select {
				case <-local.done:
					return
				case request := <-local.writeRequests:
					select {
					case <-local.done:
						return
					default:
					}
					n, err := stdin.Write(request.data)
					request.done <- sftpWriteResult{n, err}
					if err != nil {
						_ = local.Close()
						return
					}
				}
			}
		}()
		go func() {
			<-stdoutDone
			<-stderrDone
			<-writerDone
			<-local.closeDone
			<-openingDone
			<-sftpChannelWorkers
		}()
		context.AfterFunc(ctx, func() { _ = local.Close() })
		stopOpening := context.AfterFunc(opening, func() { _ = local.Close() })
		err = channel.RequestSubsystem("sftp")
		var value *sftp.Client
		if err == nil {
			value, err = sftp.NewClientPipe(reader, local)
		}
		stopOpening() // Stop before handoff: caller cancels this opening context on success.
		if err != nil {
			_ = local.Close()
		}
		select {
		case ready <- result{value, err}:
		case <-opening.Done():
			_ = local.Close()
			if value != nil {
				_ = value.Close()
			}
		}
	}()
	select {
	case value := <-ready:
		return value.client, value.err
	case <-opening.Done():
		cancel()
		return nil, opening.Err()
	}
}

func (m *SSHManager) openTerminal(s *managedSession, initialPath *string, reused bool) error {
	ctx, cancel := context.WithTimeout(s.ctx, profileTimeout(s.profile))
	defer cancel()
	home := s.profile.InitialPath
	if home == "" {
		home = "."
	}
	fallback := false
	fileClient, fileErr := openSFTPWithLifetime(ctx, s.ctx, s.client)
	if fileErr == nil {
		opened := fileClient
		stopFileTimeout := context.AfterFunc(ctx, func() { _ = opened.Close() })
		defer stopFileTimeout()
		if initialPath != nil {
			home = *initialPath
		}
		resolved, resolveErr := fileClient.RealPath(home)
		fileErr = resolveErr
		if fileErr == nil {
			home = resolved
		}
		if fileErr == nil && (initialPath != nil || s.profile.InitialPath != "") {
			var info os.FileInfo
			info, fileErr = fileClient.Stat(home)
			if fileErr == nil && !info.IsDir() {
				fileErr = errors.New("目标不是目录")
			}
		}
		if fileErr == nil && initialPath == nil && s.profile.InitialPath != "" {
			// A searchable directory can still deny the SFTP listing used by the file pane.
			_, fileErr = fileClient.ReadDirContext(ctx, home)
		}
		if fileErr != nil && initialPath == nil && s.profile.InitialPath != "" {
			home, fileErr = fileClient.RealPath(".")
			fallback = fileErr == nil
		}
		if fileErr != nil {
			_ = fileClient.Close()
			fileClient = nil
		}
	}
	if initialPath != nil && fileErr != nil {
		return fmt.Errorf("无法在此目录打开终端：%w", fileErr)
	}
	channel, err := openSSHSession(ctx, s.client)
	if err != nil {
		if fileClient != nil {
			_ = fileClient.Close()
		}
		return err
	}
	m.mu.Lock()
	if m.sessions[s.id] != s {
		m.mu.Unlock()
		_ = channel.Close()
		if fileClient != nil {
			_ = fileClient.Close()
		}
		return context.Canceled
	}
	s.channel, s.sftp = channel, fileClient
	m.mu.Unlock()
	// Closing the channel cancels even a server that never answers a PTY/shell request.
	deadlineDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-deadlineDone:
			default:
				_ = channel.Close()
			}
		case <-deadlineDone:
		}
	}()
	defer close(deadlineDone)
	if err = channel.RequestPty("xterm-256color", 30, 100, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); err != nil {
		return err
	}
	stdin, err := channel.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := channel.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := channel.StderrPipe()
	if err != nil {
		return err
	}
	if s.tmuxName == "" {
		err = channel.Shell()
	} else {
		command := "exec tmux new-session -A -s " + ShellQuote(s.tmuxName)
		if fileErr == nil {
			command += " -c " + ShellQuote(home)
		}
		err = channel.Start(command)
	}
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.sessions[s.id] != s {
		m.mu.Unlock()
		return context.Canceled
	}
	s.stdin = stdin
	m.mu.Unlock()
	var readers sync.WaitGroup
	for _, reader := range []io.Reader{stdout, stderr} {
		readers.Add(1)
		go func(reader io.Reader) { defer readers.Done(); m.readTerminal(s, reader) }(reader)
	}
	go func() {
		err := channel.Wait()
		readers.Wait()
		message := "终端会话已结束"
		if err != nil {
			message += "：" + sshMessage(err)
		}
		m.stop(s, "disconnected", message)
	}()
	if s.tmuxName == "" && (initialPath != nil || s.profile.InitialPath != "" && !fallback) {
		command := "cd -- " + ShellQuote(home)
		if initialPath != nil {
			command += " || exit"
		}
		_, _ = io.WriteString(stdin, command+"\r")
	}
	extra := Event{"home": home}
	if fallback {
		extra["startupNotice"] = "启动目录不可用，已回到账户主目录"
	}
	if fileErr != nil {
		extra["sftpError"] = "文件浏览暂不可用：" + fileErr.Error()
	}
	m.state(s, "connected", "已连接", extra)
	go func() { _ = m.store.RecordConnected(s.profile.ID, ProfileIdentity(s.profile)) }()
	return nil
}

func (m *SSHManager) readTerminal(s *managedSession, reader io.Reader) {
	buffer := make([]byte, 32768)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			m.eventGate.Lock()
			m.mu.RLock()
			live := m.sessions[s.id] == s
			m.mu.RUnlock()
			if !live {
				m.eventGate.Unlock()
				return
			}
			ack := m.emitTerminal(Event{"type": "terminal", "sessionId": s.id, "generation": s.generation, "data": base64.StdEncoding.EncodeToString(buffer[:n])})
			m.eventGate.Unlock()
			// ACKs apply backpressure to this stream. A slow renderer must
			// leave the gate free for peer sessions and disconnect events.
			if ack != nil {
				select {
				case <-ack:
				case <-s.ctx.Done():
					return
				}
			}
			// Backpressure bounds terminal traffic without discarding bytes.
			delay := time.Duration(n) * time.Second / (8 * 1024 * 1024)
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		if err != nil {
			return
		}
	}
}

func (m *SSHManager) state(s *managedSession, phase, message string, extra Event) {
	m.eventGate.Lock()
	defer m.eventGate.Unlock()
	m.mu.Lock()
	if m.sessions[s.id] != s {
		m.mu.Unlock()
		return
	}
	s.phase = phase
	m.mu.Unlock()
	event := Event{"type": "state", "sessionId": s.id, "generation": s.generation, "phase": phase, "message": message}
	for key, value := range extra {
		event[key] = value
	}
	m.emit(event)
}

func (m *SSHManager) Input(id, data string) {
	if len(data) > 1024*1024 {
		return
	}
	m.mu.RLock()
	s := m.sessions[id]
	var stdin io.WriteCloser
	if s != nil {
		stdin = s.stdin
	}
	m.mu.RUnlock()
	if stdin != nil {
		s.inputMu.Lock()
		_, err := io.WriteString(stdin, data)
		s.inputMu.Unlock()
		if err != nil {
			m.stop(s, "disconnected", sshMessage(err))
		}
	}
}

func (m *SSHManager) Resize(id string, cols, rows int) {
	if cols < 2 || rows < 2 || cols > 1000 || rows > 1000 {
		return
	}
	m.mu.RLock()
	s := m.sessions[id]
	var channel *ssh.Session
	if s != nil {
		channel = s.channel
	}
	m.mu.RUnlock()
	if channel != nil {
		_ = channel.WindowChange(rows, cols)
	}
}

func (m *SSHManager) Client(id string) (*ssh.Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.sessions[id]
	if s == nil || s.phase != "connected" || s.client == nil {
		return nil, errors.New("请先连接服务器")
	}
	return s.client, nil
}

func (m *SSHManager) SFTP(id string) (*sftp.Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.sessions[id]
	if s == nil || s.phase != "connected" || s.sftp == nil {
		return nil, errors.New("文件连接不可用，请先连接服务器")
	}
	return s.sftp, nil
}

func (m *SSHManager) Identity(id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.sessions[id]
	if s == nil || s.phase != "connected" {
		return "", errors.New("连接已断开，请重新连接服务器")
	}
	value, _ := json.Marshal([]string{ProfileIdentity(s.profile), s.generation})
	return string(value), nil
}

// TransferResumeIdentity can survive reconnects, but changes if any verified host key or hop changes.
func (m *SSHManager) TransferResumeIdentity(id string) (string, error) {
	m.mu.RLock()
	s := m.sessions[id]
	if s == nil || s.phase != "connected" {
		m.mu.RUnlock()
		return "", errors.New("连接已断开，请重新连接服务器")
	}
	s.transport.mu.Lock()
	parts := append([]string{ProfileIdentity(s.profile)}, s.transport.fingerprints...)
	s.transport.mu.Unlock()
	m.mu.RUnlock()
	value, _ := json.Marshal(parts)
	return string(value), nil
}

func (m *SSHManager) Disconnect(id string) {
	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	if s != nil {
		m.stop(s, "disconnected", "已主动断开连接")
	}
}

func (m *SSHManager) stop(s *managedSession, phase, message string) {
	m.eventGate.Lock()
	m.mu.Lock()
	if m.sessions[s.id] != s {
		m.mu.Unlock()
		m.eventGate.Unlock()
		return
	}
	delete(m.sessions, s.id)
	pending, channel, fileClient := s.pending, s.channel, s.sftp
	s.pending = nil
	s.challenge = nil
	last := true
	for _, other := range m.sessions {
		if other.transport == s.transport {
			last = false
			break
		}
	}
	m.mu.Unlock()
	s.cancel()
	if last {
		s.transport.close()
	}
	m.emit(Event{"type": "state", "sessionId": s.id, "generation": s.generation, "phase": phase, "message": message})
	m.eventGate.Unlock()
	if pending != nil {
		select {
		case pending.response <- false:
		default:
		}
	}
	if channel != nil {
		go func() { _ = channel.Close() }()
	}
	if fileClient != nil {
		go func() { _ = fileClient.Close() }()
	}
}

func (m *SSHManager) stopTransport(t *sshTransport, phase, message string) {
	m.mu.RLock()
	var sessions []*managedSession
	for _, s := range m.sessions {
		if s.transport == t {
			sessions = append(sessions, s)
		}
	}
	m.mu.RUnlock()
	for _, s := range sessions {
		m.stop(s, phase, message)
	}
	t.close()
}

func (m *SSHManager) Close() {
	m.mu.Lock()
	m.closed = true
	var sessions []*managedSession
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		m.stop(s, "disconnected", "应用已关闭")
	}
}

func sshMessage(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "操作已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "连接超时，请检查网络和远程服务"
	}
	message := err.Error()
	if strings.Contains(message, "unable to authenticate") || strings.Contains(message, "no supported methods remain") {
		return "身份验证失败，请检查用户名、凭据和登录方式"
	}
	return message
}
