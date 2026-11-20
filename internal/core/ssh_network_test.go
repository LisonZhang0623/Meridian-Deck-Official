package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestSSHMultiHopProxyAndResumeIdentity(t *testing.T) {
	outer, middle, target := newSSHToolsFixture(t), newSSHToolsFixture(t), newSSHToolsFixture(t)
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	proxyDestination := make(chan string, 2)
	go func() {
		for {
			conn, err := proxy.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				destination, err := acceptSOCKSConnect(conn)
				if err != nil || destination != outer.listener.Addr().String() {
					return
				}
				upstream, err := net.DialTimeout("tcp", destination, time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				if err = writeSOCKSReply(conn, 0); err != nil {
					return
				}
				proxyDestination <- destination
				_ = conn.SetDeadline(time.Time{})
				go io.Copy(upstream, conn)
				_, _ = io.Copy(conn, upstream)
			}()
		}
	}()
	outerProfile, middleProfile, targetProfile := outer.profile(), middle.profile(), target.profile()
	outerProfile.ProxyAddress = proxy.Addr().String()
	middleProfile.JumpProfileID = outerProfile.ID
	middleProfile.JumpIdentity = ProfileIdentity(outerProfile)
	targetProfile.JumpProfileID = middleProfile.ID
	targetProfile.JumpIdentity = ProfileIdentity(middleProfile)
	password := "test-password"
	creds := Credentials{Password: &password}
	chain := []Jump{{Profile: middleProfile, Credentials: creds}, {Profile: outerProfile, Credentials: creds}}
	events := &sshTestEvents{}
	manager := NewSSHManager(&sshTestStore{hosts: map[string]string{}}, events.emit, nil)
	t.Cleanup(manager.Close)
	if err = manager.ConnectChain("multi-hop", targetProfile, creds, chain); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Profile{outerProfile, middleProfile, targetProfile} {
		host := fmt.Sprintf("[127.0.0.1]:%d", p.Port)
		var fingerprint string
		sshTestEventually(t, func() bool {
			return events.find(func(e Event) bool {
				if e["type"] == "hostkey" && e["host"] == host {
					fingerprint, _ = e["fingerprint"].(string)
				}
				return fingerprint != ""
			})
		})
		if err = manager.TrustHostKey("multi-hop", host, "SHA256:stale", true); err == nil {
			t.Fatal("stale jump fingerprint accepted")
		}
		if err = manager.TrustHostKey("multi-hop", host, fingerprint, true); err != nil {
			t.Fatal(err)
		}
	}
	sshTestEventually(t, func() bool { return events.state("multi-hop", "connected") })
	if outer.authentications.Load() != 1 || middle.authentications.Load() != 1 || target.authentications.Load() != 1 || len(proxyDestination) != 1 {
		t.Fatal("route did not authenticate through all jumps and the proxy")
	}
	stable, err := manager.TransferResumeIdentity("multi-hop")
	if err != nil || stable == "" {
		t.Fatal("missing transfer resume identity", err)
	}
	first, _ := manager.Identity("multi-hop")
	manager.Disconnect("multi-hop")
	if err = manager.ConnectChain("multi-hop-again", targetProfile, creds, chain); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("multi-hop-again", "connected") })
	resumed, _ := manager.TransferResumeIdentity("multi-hop-again")
	second, _ := manager.Identity("multi-hop-again")
	if resumed != stable || second == first {
		t.Fatal("reconnect did not preserve only the stable transfer identity")
	}
}

func TestSSHKeyboardInteractiveAfterPassword(t *testing.T) {
	f := newSSHToolsFixture(t, func(_ *sshToolsFixture, config *ssh.ServerConfig) {
		password := config.PasswordCallback
		config.PasswordCallback = func(meta ssh.ConnMetadata, input []byte) (*ssh.Permissions, error) {
			if _, err := password(meta, input); err != nil {
				return nil, err
			}
			return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
				KeyboardInteractiveCallback: func(meta ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
					answers, err := challenge(meta.User(), "Enter test verification code", []string{"Code:"}, []bool{false})
					if err != nil || len(answers) != 1 || answers[0] != "test-code" {
						return nil, errors.New("bad test verification code")
					}
					return nil, nil
				},
			}}
		}
	})
	events := &sshTestEvents{}
	manager := NewSSHManager(&sshTestStore{hosts: map[string]string{}}, events.emit, nil)
	t.Cleanup(manager.Close)
	p := f.profile()
	p.KeyboardInteractive = true
	password := "test-password"
	if err := manager.Connect("mfa", p, Credentials{Password: &password}, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("mfa", "verifying") })
	if err := manager.Trust("mfa", true); err != nil {
		t.Fatal(err)
	}
	var challengeID string
	sshTestEventually(t, func() bool {
		return events.find(func(event Event) bool {
			if event["type"] != "auth-challenge" || event["sessionId"] != "mfa" {
				return false
			}
			challengeID, _ = event["challengeId"].(string)
			return challengeID != ""
		})
	})
	if err := manager.AnswerChallenge("mfa", "stale", []string{"test-code"}); err == nil {
		t.Fatal("accepted stale authentication challenge")
	}
	if err := manager.AnswerChallenge("mfa", challengeID, []string{"extra", "test-code"}); err == nil {
		t.Fatal("accepted incorrect answer count")
	}
	if err := manager.AnswerChallenge("mfa", challengeID, []string{"test-code"}); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("mfa", "connected") })
}

func TestStoreMultiHopIdentityAndDepth(t *testing.T) {
	store, err := NewStore(t.TempDir(), "", testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	profiles := make([]Profile, 0, maxJumpHops+1)
	for i := 0; i <= maxJumpHops; i++ {
		p := storageTestProfile(fmt.Sprintf("hop-%d", i))
		if i > 0 {
			p.JumpProfileID = profiles[i-1].ID
		}
		p, err = store.SaveProfile(p, Credentials{})
		if err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, p)
	}
	target := profiles[len(profiles)-1]
	login, err := store.ResolveSSHLogin(target.ID, Credentials{}, Credentials{}, nil, nil)
	if err != nil || len(login.Jumps) != maxJumpHops || login.Jumps[maxJumpHops-1].Profile.ID != profiles[0].ID {
		t.Fatal("saved jump order or length incorrect", err)
	}
	identity := ProfileIdentity(login.Profile)
	if err = store.RecordConnected(target.ID, identity); err != nil {
		t.Fatal(err)
	}
	outer := profiles[0]
	outer.Host = "changed.test"
	if outer, err = store.SaveProfile(outer, Credentials{}); err != nil {
		t.Fatal(err)
	}
	current, err := store.Get(target.ID)
	if err != nil || ProfileIdentity(current) == identity || current.LastConnectedAt != 0 {
		t.Fatal("outer jump edit did not invalidate the dependent route and history", err)
	}
	tooDeep := storageTestProfile("beyond-limit")
	tooDeep.JumpProfileID = target.ID
	if _, err = store.SaveProfile(tooDeep, Credentials{}); err == nil {
		t.Fatal("accepted a chain beyond the hop limit")
	}
	outer.JumpProfileID = profiles[1].ID
	if _, err = store.SaveProfile(outer, Credentials{}); err == nil {
		t.Fatal("accepted a multi-hop cycle")
	}
}

func TestSSHAgent(t *testing.T) {
	listener := testAgentListener(t)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err = keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_ = agent.ServeAgent(keyring, conn)
		}
	}()
	f := newSSHToolsFixture(t, func(_ *sshToolsFixture, config *ssh.ServerConfig) {
		config.PasswordCallback = nil
		config.PublicKeyCallback = func(_ ssh.ConnMetadata, got ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(signer.PublicKey().Marshal(), got.Marshal()) {
				return nil, errors.New("unexpected agent key")
			}
			return nil, nil
		}
	})
	events := &sshTestEvents{}
	manager := NewSSHManager(&sshTestStore{hosts: map[string]string{}}, events.emit, nil)
	t.Cleanup(manager.Close)
	p := f.profile()
	p.AuthType = "agent"
	if err = manager.Connect("agent-auth", p, Credentials{}, nil); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("agent-auth", "verifying") })
	if err = manager.Trust("agent-auth", true); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool { return events.state("agent-auth", "connected") })
}

func TestToolsDynamicAndRemoteForwards(t *testing.T) {
	f := newSSHToolsFixture(t, func(f *sshToolsFixture, _ *ssh.ServerConfig) { f.handleRequests = acceptTestRemoteForward })
	manager, _, _ := sshTestConnect(t, f)
	tools := NewTools(manager, nil)
	t.Cleanup(tools.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "forwarded test") }))
	t.Cleanup(server.Close)
	host, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	localPort, _ := strconv.Atoi(port)
	for _, mode := range []string{"dynamic", "remote"} {
		config := ForwardConfig{Name: mode, Mode: mode}
		if mode == "remote" {
			config.LocalHost, config.LocalPort = host, localPort
		}
		forward, err := tools.StartForward("first", config)
		if err != nil {
			t.Fatal(err)
		}
		if forward.SocksPassword != "" {
			t.Fatalf("%s forwarding unexpectedly issued SOCKS credentials", mode)
		}
		transport := &http.Transport{}
		target := server.URL
		port := forward.RemotePort
		if mode == "dynamic" {
			port = forward.LocalPort
			transport.Proxy = http.ProxyURL(&url.URL{Scheme: "socks5", Host: fmt.Sprintf("127.0.0.1:%d", port)})
		} else {
			target = fmt.Sprintf("http://127.0.0.1:%d", port)
		}
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, err := client.Get(target)
		if err != nil {
			t.Fatalf("%s forwarding failed: %v", mode, err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || string(body) != "forwarded test" {
			t.Fatalf("%s forwarding returned %q: %v", mode, body, err)
		}
		_ = tools.StopForward(forward.ID)
		sshTestEventually(t, func() bool {
			socket, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
			if socket != nil {
				socket.Close()
			}
			return err != nil
		})
	}
}

func TestToolsShareExistingHTTPProxy(t *testing.T) {
	fixture := newSSHToolsFixture(t, func(f *sshToolsFixture, _ *ssh.ServerConfig) {
		f.handleRequests = acceptTestRemoteForward
		f.rejectDirect = true
	})
	manager, _, _ := sshTestConnect(t, fixture)
	const content = "HTTPS through existing proxy"
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, content)
	}))
	t.Cleanup(target.Close)
	proxy := newTestConnectProxy(t, target.Listener.Addr().String())
	proxyAddress := proxy.Listener.Addr().String()
	proxyPort := proxy.Listener.Addr().(*net.TCPAddr).Port
	tools := NewTools(manager, nil)
	t.Cleanup(tools.Close)

	if _, err := ValidateForward(ForwardConfig{Name: "missing proxy port", Mode: "remote-http"}); err == nil {
		t.Fatal("sharing an HTTP proxy requires its existing local port")
	}
	occupied, err := tools.StartForward("first", ForwardConfig{Name: "occupied listener", Mode: "dynamic", LocalPort: proxyPort})
	if err == nil {
		_ = tools.StopForward(occupied.ID)
		t.Fatal("dynamic forwarding bound the existing proxy port")
	}
	if message := err.Error(); !strings.Contains(message, proxyAddress) || !strings.Contains(message, "填 0") ||
		!strings.Contains(message, "共享本机 HTTP 代理") {
		t.Fatalf("local bind error should identify the port and explain the available choices: %v", err)
	}

	forward, err := tools.StartForward("first", ForwardConfig{
		Name:       "existing HTTP proxy",
		Mode:       "remote-http",
		LocalHost:  "192.0.2.1",
		LocalPort:  proxyPort,
		RemoteHost: "0.0.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if forward.Mode != "remote-http" || forward.LocalHost != "127.0.0.1" || forward.LocalPort != proxyPort ||
		forward.RemoteHost != "127.0.0.1" || forward.RemotePort <= 0 {
		t.Fatalf("unexpected HTTP proxy forward: %+v", forward)
	}
	remoteAddress := net.JoinHostPort(forward.RemoteHost, strconv.Itoa(forward.RemotePort))
	fetchViaProxy := func(address string) {
		t.Helper()
		transport := target.Client().Transport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: address})
		transport.DisableKeepAlives = true
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, err := client.Get(target.URL)
		if err != nil {
			t.Fatalf("HTTPS request via %s failed: %v", address, err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK || string(body) != content {
			t.Fatalf("HTTPS via %s returned status %d and body %q: %v", address, response.StatusCode, body, err)
		}
	}
	fetchViaProxy(remoteAddress)
	if err := tools.StopForward(forward.ID); err != nil {
		t.Fatal(err)
	}
	sshTestEventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", remoteAddress, 50*time.Millisecond)
		if conn != nil {
			_ = conn.Close()
		}
		return err != nil
	})
	fetchViaProxy(proxyAddress)
}

func newTestConnectProxy(t *testing.T, target string) *httptest.Server {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != target {
			http.Error(w, "unexpected proxy request", http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
		if err != nil {
			http.Error(w, "target unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		deadline := time.Now().Add(10 * time.Second)
		_ = client.SetDeadline(deadline)
		_ = upstream.SetDeadline(deadline)
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		requestDone := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.(*net.TCPConn).CloseWrite()
			close(requestDone)
		}()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-requestDone
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

func TestToolsAuthenticatedReverseSOCKS(t *testing.T) {
	fixture := newSSHToolsFixture(t, func(f *sshToolsFixture, _ *ssh.ServerConfig) {
		f.handleRequests = acceptTestRemoteForward
		f.rejectDirect = true
	})
	manager, _, _ := sshTestConnect(t, fixture)
	const content = "reverse SOCKS response"
	upstreamClosed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, content)
		w.(http.Flusher).Flush()
		// Leave the response open so stopping the tunnel has to close a live stream.
		<-r.Context().Done()
		upstreamClosed <- struct{}{}
	}))
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	tools := NewTools(manager, nil)
	t.Cleanup(tools.Close)

	forward, err := tools.StartForward("first", ForwardConfig{
		Name:       "reverse SOCKS",
		Mode:       "remote-dynamic",
		RemoteHost: "0.0.0.0",
		LocalHost:  "127.0.0.1",
		LocalPort:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if forward.RemotePort <= 0 || forward.RemoteHost != "127.0.0.1" || forward.LocalHost != "" || forward.LocalPort != 0 {
		t.Fatal("reverse SOCKS did not allocate a remote loopback port")
	}
	if forward.SocksPassword == "" || len(forward.SocksPassword) > 255 {
		t.Fatal("reverse SOCKS did not issue a usable SOCKS5 password")
	}
	address := net.JoinHostPort(forward.RemoteHost, strconv.Itoa(forward.RemotePort))
	openSocket := func(t *testing.T) net.Conn {
		t.Helper()
		conn, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	t.Run("requires authentication", func(t *testing.T) {
		conn := openSocket(t)
		testSOCKSExchange(t, conn, []byte{5, 1, 0}, []byte{5, 255})
		testSOCKSClosed(t, conn)
	})
	badPassword := "wrong-password"
	if badPassword == forward.SocksPassword {
		badPassword += "!"
	}
	for _, auth := range []struct {
		name, username, password string
	}{
		{"rejects incorrect username", "wrong-user", forward.SocksPassword},
		{"rejects incorrect password", "sshtool", badPassword},
	} {
		t.Run(auth.name, func(t *testing.T) {
			conn := openSocket(t)
			testSOCKSExchange(t, conn, []byte{5, 1, 2}, []byte{5, 2})
			request := append([]byte{1, byte(len(auth.username))}, auth.username...)
			request = append(request, byte(len(auth.password)))
			request = append(request, auth.password...)
			testSOCKSExchange(t, conn, request, []byte{1, 1})
			testSOCKSClosed(t, conn)
		})
	}

	// The fixture denies direct-tcpip channels. An authenticated SOCKS5h
	// request can reach this hostname only if the client resolves and dials it.
	proxy := &url.URL{Scheme: "socks5h", Host: address, User: url.UserPassword("sshtool", forward.SocksPassword)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	targetPort := server.Listener.Addr().(*net.TCPAddr).Port
	response, err := client.Get(fmt.Sprintf("http://localhost:%d", targetPort))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body := make([]byte, len(content))
	if _, err := io.ReadFull(response.Body, body); err != nil || string(body) != content || response.StatusCode != http.StatusOK {
		t.Fatalf("reverse SOCKS returned status %d and body %q: %v", response.StatusCode, body, err)
	}

	pending := openSocket(t)
	testSOCKSExchange(t, pending, []byte{5, 1, 2}, []byte{5, 2})
	if err := tools.StopForward(forward.ID); err != nil {
		t.Fatal(err)
	}
	testSOCKSClosed(t, response.Body)
	testSOCKSClosed(t, pending)
	select {
	case <-upstreamClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("stopped reverse SOCKS kept its upstream connection open")
	}
	sshTestEventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if conn != nil {
			_ = conn.Close()
		}
		return err != nil
	})
	stopped := tools.ListForwards()
	if len(stopped) != 1 || stopped[0].Status != "stopped" || stopped[0].SocksPassword != "" {
		t.Fatal("stopped reverse SOCKS forward still exposes active credentials")
	}
}

func testSOCKSExchange(t *testing.T, conn net.Conn, request, expected []byte) {
	t.Helper()
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len(expected))
	if _, err := io.ReadFull(conn, reply); err != nil || !bytes.Equal(reply, expected) {
		t.Fatalf("SOCKS reply was %v, want %v: %v", reply, expected, err)
	}
}

func testSOCKSClosed(t *testing.T, reader io.Reader) {
	t.Helper()
	var one [1]byte
	var timeout net.Error
	if n, err := reader.Read(one[:]); n != 0 || err == nil || (errors.As(err, &timeout) && timeout.Timeout()) {
		t.Fatalf("SOCKS connection did not close: %d bytes, %v", n, err)
	}
}

func acceptTestRemoteForward(sshConn *ssh.ServerConn, requests <-chan *ssh.Request) {
	var listener net.Listener
	defer func() {
		if listener != nil {
			listener.Close()
		}
	}()
	for request := range requests {
		var bind struct {
			Host string
			Port uint32
		}
		if ssh.Unmarshal(request.Payload, &bind) != nil || bind.Host != "127.0.0.1" {
			_ = request.Reply(false, nil)
			continue
		}
		switch request.Type {
		case "tcpip-forward":
			if listener != nil {
				_ = request.Reply(false, nil)
				continue
			}
			opened, err := net.Listen("tcp", net.JoinHostPort(bind.Host, fmt.Sprint(bind.Port)))
			if err != nil {
				_ = request.Reply(false, nil)
				continue
			}
			listener = opened
			port := uint32(opened.Addr().(*net.TCPAddr).Port)
			_ = request.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
			go func() {
				for {
					socket, err := opened.Accept()
					if err != nil {
						return
					}
					go testForwardRemoteSocket(sshConn, socket, port)
				}
			}()
		case "cancel-tcpip-forward":
			if listener != nil {
				listener.Close()
				listener = nil
			}
			_ = request.Reply(true, nil)
		default:
			_ = request.Reply(false, nil)
		}
	}
}

func testForwardRemoteSocket(sshConn *ssh.ServerConn, socket net.Conn, port uint32) {
	defer socket.Close()
	origin := socket.RemoteAddr().(*net.TCPAddr)
	data := ssh.Marshal(struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}{"127.0.0.1", port, origin.IP.String(), uint32(origin.Port)})
	channel, requests, err := sshConn.OpenChannel("forwarded-tcpip", data)
	if err != nil {
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	go func() { _, _ = io.Copy(channel, socket); _ = channel.CloseWrite() }()
	_, _ = io.Copy(socket, channel)
}
