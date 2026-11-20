package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const sshTestResources = `[cpu]
cpu 1000 0 500 8500 0 0 0 0 50 0
[memory]
MemTotal: 33554432 kB
MemAvailable: 16777216 kB
[disk]
Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/sda1 104857600 20971520 83886080 20% /
[network]
eth0: 100000 10 0 0 0 0 0 0 200000 10 0 0 0 0 0 0
lo: 500 10 0 0 0 0 0 0 500 10 0 0 0 0 0 0
[uptime]
3661.9 1000
[load]
1.20 0.80 0.50 1/100 1000`

type sshTestStore struct {
	mu        sync.Mutex
	hosts     map[string]string
	connected int
}

func (s *sshTestStore) Fingerprint(host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts[host]
}
func (s *sshTestStore) Trust(host, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts[host] = fingerprint
	return nil
}
func (s *sshTestStore) RecordConnected(_, _ string) error {
	s.mu.Lock()
	s.connected++
	s.mu.Unlock()
	return nil
}

type sshTestEvents struct {
	mu     sync.Mutex
	events []Event
}

func (e *sshTestEvents) emit(event Event) {
	e.mu.Lock()
	e.events = append(e.events, event)
	e.mu.Unlock()
}
func (e *sshTestEvents) find(match func(Event) bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, event := range e.events {
		if match(event) {
			return true
		}
	}
	return false
}
func sshTestEventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for SSH state")
}
func (e *sshTestEvents) state(id, phase string) bool {
	return e.find(func(v Event) bool { return v["type"] == "state" && v["sessionId"] == id && v["phase"] == phase })
}
func (e *sshTestEvents) output(id, text string) bool {
	return e.find(func(v Event) bool {
		if v["type"] != "terminal" || v["sessionId"] != id {
			return false
		}
		b, _ := base64.StdEncoding.DecodeString(v["data"].(string))
		return strings.Contains(string(b), text)
	})
}

type sshTestSigner struct {
	mu         sync.RWMutex
	signer     ssh.Signer
	signatures atomic.Int32
}

func (s *sshTestSigner) PublicKey() ssh.PublicKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.signer.PublicKey()
}
func (s *sshTestSigner) Sign(random io.Reader, data []byte) (*ssh.Signature, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.signatures.Add(1)
	return s.signer.Sign(random, data)
}
func sshTestNewSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

type sshToolsFixture struct {
	listener        net.Listener
	signer          *sshTestSigner
	handlers        sftp.Handlers
	mu              sync.Mutex
	sockets         map[net.Conn]bool
	authentications atomic.Int32
	connections     atomic.Int32
	shells          atomic.Int32
	logs            atomic.Int32
	gpuQueries      atomic.Int32
	hangGPU         atomic.Bool
	pauseOnGPU      atomic.Bool
	pauseReads      atomic.Bool
	readGate        chan struct{}
	resumeOnce      sync.Once
	handleRequests  func(*ssh.ServerConn, <-chan *ssh.Request)
	rejectDirect    bool
}

type sshTestGatedConnection struct {
	net.Conn
	fixture *sshToolsFixture
}

func (c *sshTestGatedConnection) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	if c.fixture.pauseReads.Load() {
		<-c.fixture.readGate
	}
	return n, err
}

func newSSHToolsFixture(t *testing.T, configure ...func(*sshToolsFixture, *ssh.ServerConfig)) *sshToolsFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &sshToolsFixture{listener: listener, signer: &sshTestSigner{signer: sshTestNewSigner(t)}, handlers: sftp.InMemHandler(), sockets: map[net.Conn]bool{}, readGate: make(chan struct{})}
	config := &ssh.ServerConfig{Config: ssh.Config{RekeyThreshold: 1024}, PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if metadata.User() != "test" || string(password) != "test-password" {
			return nil, errors.New("bad test login")
		}
		f.authentications.Add(1)
		return nil, nil
	}}
	config.PublicKeyCallback = func(metadata ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
		if metadata.User() != "test" {
			return nil, errors.New("bad test login")
		}
		f.authentications.Add(1)
		return nil, nil
	}
	config.AddHostKey(f.signer)
	for _, setup := range configure {
		setup(f, config)
	}
	go func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.sockets[socket] = true
			f.mu.Unlock()
			go f.serve(socket, config)
		}
	}()
	t.Cleanup(func() {
		f.resumeOnce.Do(func() { close(f.readGate) })
		_ = listener.Close()
		f.mu.Lock()
		for socket := range f.sockets {
			_ = socket.Close()
		}
		f.mu.Unlock()
	})
	return f
}
func (f *sshToolsFixture) profile() Profile {
	address := f.listener.Addr().(*net.TCPAddr)
	return Profile{ID: NewID(), Name: "loopback", Host: "127.0.0.1", Port: address.Port, Username: "test", AuthType: "password", Timeout: 5, Keepalive: true, Tags: []string{}}
}
func (f *sshToolsFixture) serve(socket net.Conn, config *ssh.ServerConfig) {
	defer func() { _ = socket.Close(); f.mu.Lock(); delete(f.sockets, socket); f.mu.Unlock() }()
	connection, channels, requests, err := ssh.NewServerConn(&sshTestGatedConnection{Conn: socket, fixture: f}, config)
	if err != nil {
		return
	}
	defer connection.Close()
	f.connections.Add(1)
	defer f.connections.Add(-1)
	if f.handleRequests != nil {
		go f.handleRequests(connection, requests)
	} else {
		go ssh.DiscardRequests(requests)
	}
	for next := range channels {
		switch next.ChannelType() {
		case "session":
			channel, requests, err := next.Accept()
			if err == nil {
				go f.session(channel, requests)
			}
		case "direct-tcpip":
			if f.rejectDirect {
				_ = next.Reject(ssh.Prohibited, "direct forwarding disabled for this fixture")
			} else {
				go f.forward(next)
			}
		default:
			_ = next.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}
func (f *sshToolsFixture) forward(next ssh.NewChannel) {
	var request struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if ssh.Unmarshal(next.ExtraData(), &request) != nil || request.Host != "127.0.0.1" {
		_ = next.Reject(ssh.Prohibited, "loopback only")
		return
	}
	remote, err := net.Dial("tcp", net.JoinHostPort(request.Host, fmt.Sprint(request.Port)))
	if err != nil {
		_ = next.Reject(ssh.ConnectionFailed, "unavailable")
		return
	}
	defer remote.Close()
	channel, requests, err := next.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(remote, channel)
		if closeWrite, ok := remote.(interface{ CloseWrite() error }); ok {
			_ = closeWrite.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() { _, _ = io.Copy(channel, remote); _ = channel.CloseWrite(); done <- struct{}{} }()
	<-done
	<-done
}
func (f *sshToolsFixture) session(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	done := make(chan struct{})
	defer close(done)
	for request := range requests {
		switch request.Type {
		case "pty-req", "window-change":
			_ = request.Reply(true, nil)
		case "subsystem":
			var value struct{ Name string }
			_ = ssh.Unmarshal(request.Payload, &value)
			if value.Name != "sftp" {
				_ = request.Reply(false, nil)
				continue
			}
			_ = request.Reply(true, nil)
			server := sftp.NewRequestServer(channel, f.handlers)
			_ = server.Serve()
			_ = server.Close()
			return
		case "shell":
			_ = request.Reply(true, nil)
			f.shells.Add(1)
			go func() { _, _ = io.WriteString(channel, "Welcome 测试\r\n"); _, _ = io.Copy(channel, channel) }()
		case "exec":
			var value struct{ Command string }
			_ = ssh.Unmarshal(request.Payload, &value)
			_ = request.Reply(true, nil)
			switch {
			case value.Command == GPUCommand:
				if f.pauseOnGPU.Load() {
					f.pauseReads.Store(true)
				}
				f.gpuQueries.Add(1)
				if !f.hangGPU.Load() {
					go func() {
						_, _ = io.WriteString(channel, "0, GPU-test-0, NVIDIA RTX 4090, 78, 12288, 24564, 62, 245.5, 450, 45, P2\n")
						sshTestExit(channel, 0)
					}()
				}
			case value.Command == ResourceCommand:
				go func() { _, _ = io.WriteString(channel, sshTestResources); sshTestExit(channel, 0) }()
			case strings.Contains(value.Command, "exec tail -n "):
				f.logs.Add(1)
				go func() {
					defer f.logs.Add(-1)
					_, _ = io.WriteString(channel, "INFO 开始\nERROR sample diagnostic\n")
					ticker := time.NewTicker(20 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-done:
							return
						case <-ticker.C:
							if _, err := io.WriteString(channel, "INFO epoch loss=0.1\n"); err != nil {
								return
							}
						}
					}
				}()
			default:
				go func() { _, _ = io.WriteString(channel, value.Command); sshTestExit(channel, 0) }()
			}
		case "signal":
			_ = request.Reply(true, nil)
			sshTestExit(channel, 0)
			return
		default:
			_ = request.Reply(false, nil)
		}
	}
}
func sshTestExit(channel ssh.Channel, status uint32) {
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
	_ = channel.CloseWrite()
	_ = channel.Close()
}
func sshTestConnect(t *testing.T, f *sshToolsFixture) (*SSHManager, *sshTestEvents, Profile) {
	t.Helper()
	events := &sshTestEvents{}
	store := &sshTestStore{hosts: map[string]string{}}
	manager := NewSSHManager(store, events.emit, nil)
	t.Cleanup(manager.Close)
	profile := f.profile()
	password := "test-password"
	if err := manager.Connect("first", profile, Credentials{Password: &password}, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("first", "verifying") })
	if err := manager.Trust("first", true); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("first", "connected") })
	return manager, events, profile
}

func TestSSHSharedTransportIndependentTerminalsAndRekey(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, events, profile := sshTestConnect(t, f)
	if err := manager.NewTerminal("second", "first", profile, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("second", "connected") })
	if f.authentications.Load() != 1 {
		t.Fatalf("expected one authentication, got %d", f.authentications.Load())
	}
	manager.Disconnect("first")
	signaturesBefore := f.signer.signatures.Load()
	manager.Input("second", strings.Repeat("x", 16384)+"after-rekey")
	manager.Resize("second", 101, 31)
	sshTestEventually(t, func() bool { return events.output("second", "after-rekey") })
	sshTestEventually(t, func() bool { return f.signer.signatures.Load() > signaturesBefore })
	if _, err := manager.Client("second"); err != nil {
		t.Fatal(err)
	}
	if err := manager.NewTerminal("third", "second", profile, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("third", "connected") })
	manager.Disconnect("second")
	manager.Input("third", "still-connected")
	sshTestEventually(t, func() bool { return events.output("third", "still-connected") })
	manager.Disconnect("third")
	sshTestEventually(t, func() bool { return f.connections.Load() == 0 })
}

func TestSSHRekeyHostKeyChangeDisconnectsRemainingTerminal(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, events, profile := sshTestConnect(t, f)
	if err := manager.NewTerminal("remaining", "first", profile, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("remaining", "connected") })
	manager.Disconnect("first")
	replacement := sshTestNewSigner(t)
	f.signer.mu.Lock()
	f.signer.signer = replacement
	f.signer.mu.Unlock()
	manager.Input("remaining", strings.Repeat("rekey-change", 2000))
	manager.Resize("remaining", 101, 31)
	sshTestEventually(t, func() bool { return events.state("remaining", "disconnected") || events.state("remaining", "failed") })
	if _, err := manager.Client("remaining"); err == nil {
		t.Fatal("host key change left transport usable")
	}
}

func TestSSHEncryptedPrivateKeyAuthenticationAndBadPassword(t *testing.T) {
	f := newSSHToolsFixture(t)
	events := &sshTestEvents{}
	manager := NewSSHManager(&sshTestStore{hosts: map[string]string{}}, events.emit, nil)
	t.Cleanup(manager.Close)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "loopback-test", []byte("test-key-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "test-key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	profile := f.profile()
	profile.AuthType = "key"
	profile.KeyPath = keyPath
	passphrase := "test-key-passphrase"
	if err := manager.Connect("key-session", profile, Credentials{Passphrase: &passphrase}, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("key-session", "verifying") })
	_ = manager.Trust("key-session", true)
	sshTestEventually(t, func() bool { return events.state("key-session", "connected") })
	manager.Input("key-session", "key-auth-working")
	sshTestEventually(t, func() bool { return events.output("key-session", "key-auth-working") })
	bad := f.profile()
	password := "bad-test-password"
	if err := manager.Connect("bad-password", bad, Credentials{Password: &password}, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("bad-password", "failed") })
}

func TestSSHJumpFingerprintsAndCancellation(t *testing.T) {
	jumpFixture := newSSHToolsFixture(t)
	targetFixture := newSSHToolsFixture(t)
	events := &sshTestEvents{}
	manager := NewSSHManager(&sshTestStore{hosts: map[string]string{}}, events.emit, nil)
	t.Cleanup(manager.Close)
	jumpProfile := jumpFixture.profile()
	target := targetFixture.profile()
	target.JumpProfileID = jumpProfile.ID
	password := "test-password"
	if err := manager.Connect("jumped", target, Credentials{Password: &password}, &Jump{Profile: jumpProfile, Credentials: Credentials{Password: &password}}); err != nil {
		t.Fatal(err)
	}
	jumpHost := fmt.Sprintf("[127.0.0.1]:%d", jumpProfile.Port)
	targetHost := fmt.Sprintf("[127.0.0.1]:%d", target.Port)
	sshTestEventually(t, func() bool {
		return events.find(func(e Event) bool { return e["type"] == "hostkey" && e["host"] == jumpHost })
	})
	_ = manager.Trust("jumped", true)
	sshTestEventually(t, func() bool {
		return events.find(func(e Event) bool { return e["type"] == "hostkey" && e["host"] == targetHost })
	})
	_ = manager.Trust("jumped", true)
	sshTestEventually(t, func() bool { return events.state("jumped", "connected") })
	manager.Input("jumped", "jump-working")
	sshTestEventually(t, func() bool { return events.output("jumped", "jump-working") })
	manager.Disconnect("jumped")
	sshTestEventually(t, func() bool { return jumpFixture.connections.Load() == 0 && targetFixture.connections.Load() == 0 })
	other := newSSHToolsFixture(t)
	p := other.profile()
	if err := manager.Connect("cancelled", p, Credentials{Password: &password}, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("cancelled", "verifying") })
	_ = manager.Trust("cancelled", false)
	sshTestEventually(t, func() bool { return events.state("cancelled", "disconnected") })
	if other.authentications.Load() != 0 {
		t.Fatal("cancelled host key must not authenticate")
	}
}

func TestProfileStartupDirectoryFallsBackWithoutLosingSFTP(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, events, profile := sshTestConnect(t, f)
	client, err := manager.SFTP("first")
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Mkdir("/project"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id, initial, want string
		fallback          bool
	}{
		{"configured", "/project", "/project", false},
		{"default-dir", "", "/", false},
		{"fallback", "/missing", "/", true},
	} {
		profile.InitialPath = test.initial
		if err := manager.NewTerminal(test.id, "first", profile, nil); err != nil {
			t.Fatal(err)
		}
		sshTestEventually(t, func() bool {
			return events.find(func(e Event) bool {
				return e["sessionId"] == test.id && e["phase"] == "connected" && e["home"] == test.want &&
					e["sftpError"] == nil && (e["startupNotice"] != nil) == test.fallback
			})
		})
		c, err := manager.SFTP(test.id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.ReadDir(test.want); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSSHDirectoryTerminalRejectsMissingDirectoryWithoutChangingPeer(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, events, profile := sshTestConnect(t, f)
	missing := "/missing"
	if err := manager.NewTerminal("bad-dir", "first", profile, &missing); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("bad-dir", "failed") })
	manager.Input("first", "peer-untouched")
	sshTestEventually(t, func() bool { return events.output("first", "peer-untouched") })
	if f.shells.Load() != 1 {
		t.Fatalf("unexpected shell opened for missing directory: %d", f.shells.Load())
	}
}

func TestToolsMonitoringLogCancellationAndOriginalTerminal(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, events, _ := sshTestConnect(t, f)
	tools := NewTools(manager, events.emit)
	t.Cleanup(tools.Close)
	gpu, err := tools.GPUStatus("first")
	if err != nil {
		t.Fatal(err)
	}
	if len(gpu.Devices) != 1 || gpu.Devices[0].MemoryUsedMiB == nil || *gpu.Devices[0].MemoryUsedMiB != 12288 {
		t.Fatalf("bad GPU data: %+v", gpu)
	}
	resources, err := tools.ResourceStatus("first")
	if err != nil {
		t.Fatal(err)
	}
	if resources.Memory.Total != 33554432*1024 || resources.CPU.Total != 10000 || resources.Network[0].Sent != 200000 {
		t.Fatalf("bad resource data: %+v", resources)
	}
	watchID, err := tools.StartLog("first", LogOptions{Path: "/home/test/app.log", Lines: 100})
	if err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool {
		return events.find(func(e Event) bool {
			return e["type"] == "log" && e["watchId"] == watchID && e["kind"] == "data" && strings.Contains(e["data"].(string), "ERROR")
		})
	})
	tools.StopLog("first")
	sshTestEventually(t, func() bool { return f.logs.Load() == 0 })
	f.hangGPU.Store(true)
	result := make(chan error, 1)
	before := f.gpuQueries.Load()
	go func() { _, err := tools.GPUStatus("first"); result <- err }()
	sshTestEventually(t, func() bool { return f.gpuQueries.Load() > before })
	tools.CancelGPU("first")
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled query succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not close monitor channel")
	}
	manager.Input("first", "terminal-still-live")
	sshTestEventually(t, func() bool { return events.output("first", "terminal-still-live") })
}

func TestToolsLocalForwardDrainsResponseAfterHalfCloseAndStops(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, _, _ := sshTestConnect(t, f)
	tools := NewTools(manager, nil)
	t.Cleanup(tools.Close)
	remote, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	payload := bytes.Repeat([]byte("slow-response-"), 600000)
	go func() {
		socket, e := remote.Accept()
		if e != nil {
			return
		}
		defer socket.Close()
		_, _ = io.ReadAll(socket)
		_, _ = socket.Write(payload)
	}()
	forward, err := tools.StartForward("first", ForwardConfig{Name: "loopback", LocalPort: 0, RemoteHost: "127.0.0.1", RemotePort: remote.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	local, err := net.DialTCP("tcp", nil, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: forward.LocalPort})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	_ = local.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(local, "request")
	_ = local.CloseWrite()
	response, err := io.ReadAll(local)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, payload) {
		t.Fatalf("truncated forwarded response: got %d want %d", len(response), len(payload))
	}
	tools.StopSession("first")
	if tools.ListForwards()[0].Status != "stopped" {
		t.Fatal("forward did not stop")
	}
	if socket, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", forward.LocalPort), 200*time.Millisecond); err == nil {
		socket.Close()
		t.Fatal("stopped forward still accepts connections")
	}
}

func TestToolsForwardRecordsRetainOrderAndCanDismissFinishedRows(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, _, _ := sshTestConnect(t, f)
	events := &sshTestEvents{}
	tools := NewTools(manager, events.emit)
	t.Cleanup(tools.Close)
	first, err := tools.StartForward("first", ForwardConfig{Name: "first", Mode: "dynamic"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := tools.StartForward("first", ForwardConfig{Name: "second", Mode: "dynamic"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tools.DismissForward(first.ID); err == nil {
		t.Fatal("dismissed a running forward")
	}
	if err = tools.StopForward(second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tools.StartForward("first", ForwardConfig{Name: "occupied", Mode: "dynamic", LocalPort: first.LocalPort}); err == nil {
		t.Fatal("opened an occupied port")
	}
	values := tools.ListForwards()
	if len(values) != 3 || values[0].Name != "occupied" || values[0].Status != "failed" || values[1].ID != second.ID || values[1].Status != "stopped" || values[2].ID != first.ID || values[2].Status != "listening" {
		t.Fatalf("unexpected newest-first history: %+v", values)
	}
	for _, id := range []string{values[0].ID, second.ID, second.ID} {
		if err = tools.DismissForward(id); err != nil {
			t.Fatal(err)
		}
	}
	third, err := tools.StartForward("first", ForwardConfig{Name: "third", Mode: "dynamic"})
	if err != nil {
		t.Fatal(err)
	}
	values = tools.ListForwards()
	if len(values) != 2 || values[0].ID != third.ID || values[1].ID != first.ID {
		t.Fatalf("dismissed rows resurfaced after starting another forward: %+v", values)
	}
	if !events.find(func(event Event) bool {
		list, ok := event["forwards"].([]PortForward)
		return event["type"] == "forwards" && ok && len(list) == 1 && list[0].ID == first.ID
	}) {
		t.Fatal("dismissal did not publish the updated list")
	}
}

func TestToolsForwardStartupCannotRestoreDismissedRow(t *testing.T) {
	requested := make(chan struct{})
	reply := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(reply) }) }
	f := newSSHToolsFixture(t, func(f *sshToolsFixture, _ *ssh.ServerConfig) {
		f.handleRequests = func(_ *ssh.ServerConn, requests <-chan *ssh.Request) {
			for request := range requests {
				if request.Type == "tcpip-forward" {
					close(requested)
					<-reply
				}
				_ = request.Reply(false, nil)
			}
		}
	})
	manager, _, _ := sshTestConnect(t, f)
	tools := NewTools(manager, nil)
	t.Cleanup(tools.Close)
	t.Cleanup(release)
	started := make(chan error, 1)
	go func() {
		_, err := tools.StartForward("first", ForwardConfig{Name: "pending", Mode: "remote", LocalPort: 9})
		started <- err
	}()
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("remote listen request was not sent")
	}
	if values := tools.ListForwards(); len(values) != 0 {
		t.Fatalf("published an unopened listener: %+v", values)
	}
	tools.StopSession("first")
	values := tools.ListForwards()
	if len(values) != 1 || values[0].Status != "stopped" {
		t.Fatalf("pending listener stop was not retained: %+v", values)
	}
	if err := tools.DismissForward(values[0].ID); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("cancelled startup returned a listener")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled startup never returned")
	}
	if values = tools.ListForwards(); len(values) != 0 {
		t.Fatalf("cancelled startup restored a dismissed row: %+v", values)
	}
}

func TestToolsParsersRejectCorruptDataAndPreserveUnavailableMetrics(t *testing.T) {
	rows, err := ParseGPU("1, GPU-b, \"RTX, test\", N/A, 0, 24564, N/A, 0, 450, N/A, P8\n0, GPU-a, RTX, 0, 1, 24564, 40, 2, 450, 0, P0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Index != 0 || rows[1].Utilization != nil || rows[1].MemoryUsedMiB == nil || *rows[1].MemoryUsedMiB != 0 {
		t.Fatalf("bad metric handling: %+v", rows)
	}
	for _, bad := range []string{"0, GPU-a, incomplete", "0, GPU-a, RTX, 0, 1, 2, 3, 4, 5, 6, P0\n0, GPU-b, RTX, 0, 1, 2, 3, 4, 5, 6, P0"} {
		if _, err := ParseGPU(bad); err == nil {
			t.Fatal("corrupt GPU data accepted")
		}
	}
	if _, err := ParseResources(strings.Replace(sshTestResources, "MemAvailable: 16777216", "MemAvailable: 999999999", 1)); err == nil {
		t.Fatal("impossible memory accepted")
	}
	if _, err := ParseResources(sshTestResources + "\n[cpu]\ncpu 1 2 3 4"); err == nil {
		t.Fatal("duplicate resource section accepted")
	}
	if _, err := LogCommand(LogOptions{Path: "relative.log", Lines: 100}); err == nil {
		t.Fatal("relative log path accepted")
	}
	if _, err := ValidateForward(ForwardConfig{Name: "bad", RemoteHost: "host\ncommand", RemotePort: 80}); err == nil {
		t.Fatal("invalid forward target accepted")
	}
}

type queryIdentityConnections struct {
	Connections
	mu       sync.Mutex
	identity string
}

func (c *queryIdentityConnections) Identity(_ string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.identity, nil
}

func TestToolsReconnectDoesNotReuseOrDeletePreviousGenerationQuery(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, _, _ := sshTestConnect(t, f)
	connections := &queryIdentityConnections{Connections: manager, identity: "old"}
	tools := NewTools(connections, nil)
	t.Cleanup(tools.Close)
	oldStarted, oldRelease := make(chan struct{}), make(chan struct{})
	newStarted, newRelease := make(chan struct{}), make(chan struct{})
	oldDone := make(chan error, 1)
	go func() {
		_, err := tools.query("first", "gpu", func(context.Context, *ssh.Client) (any, error) { close(oldStarted); <-oldRelease; return "old", nil })
		oldDone <- err
	}()
	<-oldStarted
	connections.mu.Lock()
	connections.identity = "new"
	connections.mu.Unlock()
	type result struct {
		value any
		err   error
	}
	newDone := make(chan result, 1)
	go func() {
		value, err := tools.query("first", "gpu", func(context.Context, *ssh.Client) (any, error) { close(newStarted); <-newRelease; return "new", nil })
		newDone <- result{value, err}
	}()
	select {
	case <-newStarted:
	case <-time.After(time.Second):
		close(oldRelease)
		t.Fatal("new connection reused an old query")
	}
	close(oldRelease)
	if err := <-oldDone; err == nil {
		t.Fatal("old connection result was accepted")
	}
	tools.mu.Lock()
	query := tools.queries["gpu:first"]
	kept := query != nil && query.identity == "new"
	tools.mu.Unlock()
	close(newRelease)
	value := <-newDone
	if !kept {
		t.Fatal("old completion removed the new query")
	}
	if value.err != nil || value.value != "new" {
		t.Fatalf("new query result: %+v", value)
	}
}

func TestMonitorDeadlineReturnsWithoutPeerCloseAcknowledgement(t *testing.T) {
	f := newSSHToolsFixture(t)
	manager, _, _ := sshTestConnect(t, f)
	f.hangGPU.Store(true)
	f.pauseOnGPU.Store(true)
	client, err := manager.Client("first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := execBounded(ctx, client, GPUCommand, 256*1024); done <- err }()
	sshTestEventually(t, func() bool { return f.pauseReads.Load() })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		f.resumeOnce.Do(func() { close(f.readGate) })
		t.Fatal("monitor waited for an unresponsive peer after cancellation")
	}
	f.pauseReads.Store(false)
	f.resumeOnce.Do(func() { close(f.readGate) })
}

func TestSSHReusedIDWaitsForPreviousGenerationEmission(t *testing.T) {
	f := newSSHToolsFixture(t)
	events := &sshTestEvents{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	emit := func(e Event) {
		if e["type"] == "terminal" {
			raw, _ := base64.StdEncoding.DecodeString(e["data"].(string))
			if strings.Contains(string(raw), "block-old-generation") {
				once.Do(func() { close(entered) })
				<-release
			}
		}
		events.emit(e)
	}
	profile := f.profile()
	store := &sshTestStore{hosts: map[string]string{fmt.Sprintf("[127.0.0.1]:%d", profile.Port): ssh.FingerprintSHA256(f.signer.PublicKey())}}
	manager := NewSSHManager(store, emit, nil)
	t.Cleanup(manager.Close)
	password := "test-password"
	if err := manager.Connect("reused", profile, Credentials{Password: &password}, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("reused", "connected") })
	oldIdentity, _ := manager.Identity("reused")
	manager.Input("reused", "block-old-generation")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old terminal emission did not block")
	}
	done := make(chan error, 1)
	go func() {
		manager.Disconnect("reused")
		done <- manager.Connect("reused", profile, Credentials{Password: &password}, nil)
	}()
	select {
	case err := <-done:
		close(release)
		t.Fatalf("reused ID changed while previous emit was in flight: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { identity, err := manager.Identity("reused"); return err == nil && identity != oldIdentity })
	events.mu.Lock()
	defer events.mu.Unlock()
	newGeneration := ""
	for _, event := range events.events {
		if event["type"] == "state" && event["phase"] == "connecting" {
			if newGeneration == "" {
				newGeneration = event["generation"].(string)
			} else {
				newGeneration = event["generation"].(string)
			}
		} else if newGeneration != "" && event["sessionId"] == "reused" && event["generation"] != newGeneration {
			t.Fatal("stale generation emitted after a new connection was installed")
		}
	}
}

func TestSSHTerminalAcknowledgementDoesNotBlockPeerOrDisconnect(t *testing.T) {
	f := newSSHToolsFixture(t)
	events := &sshTestEvents{}
	profile := f.profile()
	store := &sshTestStore{hosts: map[string]string{fmt.Sprintf("[127.0.0.1]:%d", profile.Port): ssh.FingerprintSHA256(f.signer.PublicKey())}}
	frameSent, acknowledged := make(chan struct{}), make(chan struct{})
	holdFirst := true
	manager := NewSSHManager(store, events.emit, func(event Event) <-chan struct{} {
		events.emit(event)
		if event["sessionId"] == "stalled" && holdFirst {
			holdFirst = false
			close(frameSent)
			return acknowledged
		}
		return nil
	})
	t.Cleanup(manager.Close)
	// Let cleanup finish even if this test detects a regression in the gate.
	defer close(acknowledged)
	password := "test-password"
	if err := manager.Connect("stalled", profile, Credentials{Password: &password}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-frameSent:
	case <-time.After(5 * time.Second):
		t.Fatal("the first terminal frame was not published")
	}
	sshTestEventually(t, func() bool { return events.state("stalled", "connected") })
	opened := make(chan error, 1)
	go func() { opened <- manager.NewTerminal("peer", "stalled", profile, nil) }()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opening a peer terminal waited for an unrelated frame acknowledgement")
	}
	sshTestEventually(t, func() bool { return events.state("peer", "connected") })
	closed := make(chan struct{})
	go func() { manager.Disconnect("stalled"); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect waited for a missing frame acknowledgement")
	}
	if !events.state("stalled", "disconnected") {
		t.Fatal("disconnect did not publish its state")
	}
	manager.Input("peer", "peer-still-works\r\n")
	sshTestEventually(t, func() bool { return events.output("peer", "peer-still-works") })
}

// ssh2 intentionally leaves SFTP CHANNEL_CLOSE acknowledgement to the
// application's subsystem-end handler. Omitting that handler reproduces a peer
// that ignores close while continuing to serve other channels. This optional
// cross-implementation regression uses the project's existing Node test tool;
// production Go code never launches Node.
func TestSFTPLocalCloseIsBoundedWhenPeerNeverAcknowledgesChannelClose(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("cross-implementation regression requires Node.js")
	}
	if err := exec.Command(node, "-p", "require.resolve('ssh2')").Run(); err != nil {
		t.Skip("cross-implementation regression requires npm ci / ssh2")
	}
	sshTestEventually(t, func() bool { return len(sftpChannelWorkers) == 0 })
	script := `
const {Server}=require('ssh2');
const {generateKeyPairSync}=require('node:crypto');
const key=generateKeyPairSync('rsa',{modulusLength:2048,privateKeyEncoding:{type:'pkcs1',format:'pem'},publicKeyEncoding:{type:'pkcs1',format:'pem'}}).privateKey;
const server=new Server({hostKeys:[key]},client=>{
 client.on('error',()=>{});
 client.on('authentication',ctx=>ctx.method==='password'&&ctx.username==='test'&&ctx.password==='test-password'?ctx.accept():ctx.reject(['password']));
 client.on('ready',()=>client.on('session',accept=>{
  const session=accept();session.on('error',()=>{});
  session.on('pty',accept=>accept());
  session.on('shell',accept=>{const channel=accept();channel.on('error',()=>{});channel.on('data',data=>channel.write(data));});
  session.on('sftp',accept=>{
   const channel=accept();channel.on('error',()=>{});
   channel.on('REALPATH',(id,path)=>channel.name(id,[{filename:'/',longname:'/',attrs:{mode:0o40755,size:0,uid:1000,gid:1000,atime:1,mtime:1}}]));
   // Deliberately no session.close/end -> channel.end() handler.
  });
 }));
});
server.listen(0,'127.0.0.1',()=>console.log(JSON.stringify({port:server.address().port})));
`
	command := exec.Command(node, "-e", script)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	type metadata struct {
		Port int `json:"port"`
	}
	metadataReady := make(chan metadata, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, _ := reader.ReadString('\n')
		var value metadata
		_ = json.Unmarshal([]byte(line), &value)
		metadataReady <- value
	}()
	var port int
	select {
	case value := <-metadataReady:
		port = value.Port
	case <-time.After(5 * time.Second):
		t.Fatal("Node fault fixture did not start")
	}
	if port == 0 {
		t.Fatal("invalid fault fixture port")
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &ssh.ClientConfig{User: "test", Auth: []ssh.AuthMethod{ssh.Password("test-password")}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	tree, err := openSFTP(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxSFTPChannels; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		fileClient, err := openSFTP(ctx, client)
		if err != nil {
			cancel()
			t.Fatalf("open channel %d: %v", i, err)
		}
		if i%2 == 0 {
			cancel()
		}
		closed := make(chan error, 1)
		go func() { closed <- fileClient.Close() }()
		select {
		case <-closed:
		case <-time.After(time.Second):
			cancel()
			_ = client.Close()
			t.Fatal("SFTP Close waited for peer acknowledgement")
		}
		cancel()
	}
	if len(sftpChannelWorkers) != maxSFTPChannels {
		t.Fatalf("fault fixture should retain bounded workers until transport closes, got %d", len(sftpChannelWorkers))
	}
	extraCtx, cancelExtra := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelExtra()
	if _, err := openSFTP(extraCtx, client); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exhausted worker budget must honor caller deadline, got %v", err)
	}
	if len(sftpChannelWorkers) != maxSFTPChannels {
		t.Fatal("exhausted worker budget created another worker")
	}
	if path, err := tree.RealPath("."); err != nil || path != "/" {
		t.Fatalf("shared file tree stopped: %q %v", path, err)
	}
	terminal, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	input, _ := terminal.StdinPipe()
	output, _ := terminal.StdoutPipe()
	if err := terminal.Shell(); err != nil {
		t.Fatal(err)
	}
	message := "other-terminal-still-live"
	if _, err := io.WriteString(input, message); err != nil {
		t.Fatal(err)
	}
	read := make(chan string, 1)
	go func() { raw := make([]byte, len(message)); _, _ = io.ReadFull(output, raw); read <- string(raw) }()
	select {
	case actual := <-read:
		if actual != message {
			t.Fatalf("unexpected terminal output: %q", actual)
		}
	case <-time.After(time.Second):
		t.Fatal("SFTP cancellation stopped a peer terminal")
	}
	closedTree := make(chan struct{})
	go func() { _ = tree.Close(); close(closedTree) }()
	select {
	case <-closedTree:
	case <-time.After(time.Second):
		t.Fatal("tree Close waited for peer")
	}
	_ = client.Close()
	sshTestEventually(t, func() bool { return len(sftpChannelWorkers) == 0 })
}
