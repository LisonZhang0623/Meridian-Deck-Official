package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type fileFixture struct {
	client        *ssh.Client
	sftp          *sftp.Client
	mu            sync.Mutex
	identity      string
	ignoreSFTPEOF *atomic.Bool
}

type fileKeepOpenChannel struct{ ssh.Channel }

func (c fileKeepOpenChannel) Close() error { return nil }

func (f *fileFixture) Client(id string) (*ssh.Client, error) {
	if id != "session" && id != "other" {
		return nil, errors.New("missing session")
	}
	return f.client, nil
}
func (f *fileFixture) SFTP(id string) (*sftp.Client, error) {
	if _, err := f.Client(id); err != nil {
		return nil, err
	}
	return f.sftp, nil
}
func (f *fileFixture) Identity(id string) (string, error) {
	if _, err := f.Client(id); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identity, nil
}
func (f *fileFixture) changeIdentity() { f.mu.Lock(); f.identity = "reconnected"; f.mu.Unlock() }

// A real loopback SSH server hosts one shared in-memory filesystem across its
// independent SFTP channels. No personal keys, hosts or local server files are used.
func newFileFixture(t *testing.T, configure ...func(sftp.Handlers) sftp.Handlers) *fileFixture {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == "test" && string(p) == "test-password" {
			return nil, nil
		}
		return nil, errors.New("bad test login")
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handlers := sftp.InMemHandler()
	for _, transform := range configure {
		handlers = transform(handlers)
	}
	ignoreSFTPEOF := &atomic.Bool{}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		socket, err := listener.Accept()
		if err != nil {
			return
		}
		server, channels, requests, err := ssh.NewServerConn(socket, config)
		if err != nil {
			socket.Close()
			return
		}
		defer server.Close()
		go func() {
			for request := range requests {
				request.Reply(true, nil)
			}
		}()
		var wg sync.WaitGroup
		for channel := range channels {
			if channel.ChannelType() != "session" {
				channel.Reject(ssh.UnknownChannelType, "session only")
				continue
			}
			stream, requests, err := channel.Accept()
			if err != nil {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer stream.Close()
				for request := range requests {
					if request.Type == "subsystem" && len(request.Payload) > 4 && string(request.Payload[4:]) == "sftp" {
						request.Reply(true, nil)
						keepOpen := ignoreSFTPEOF.Load()
						var subsystem io.ReadWriteCloser = stream
						if keepOpen {
							subsystem = fileKeepOpenChannel{stream}
						}
						server := sftp.NewRequestServer(subsystem, handlers)
						_ = server.Serve()
						server.Close()
						if keepOpen {
							for range requests {
							}
						}
						return
					}
					request.Reply(false, nil)
				}
			}()
		}
		wg.Wait()
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "test", Auth: []ssh.AuthMethod{ssh.Password("test-password")}, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 5 * time.Second})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	ftp, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		listener.Close()
		t.Fatal(err)
	}
	fixture := &fileFixture{client: client, sftp: ftp, identity: "generation-one", ignoreSFTPEOF: ignoreSFTPEOF}
	t.Cleanup(func() {
		ftp.Close()
		client.Close()
		listener.Close()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			t.Error("test SSH server did not stop")
		}
	})
	if err = ftp.MkdirAll("/home/test/images"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func fileWrite(t *testing.T, c *sftp.Client, remote string, data []byte) {
	t.Helper()
	w, err := c.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(data); err != nil {
		w.Close()
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
}
func fileRead(t *testing.T, c *sftp.Client, remote string) []byte {
	t.Helper()
	r, err := c.Open(remote)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFilesSymbolicLinksIdentifyTargetsAndPreserveLinkOnSave(t *testing.T) {
	f := newFileFixture(t)
	files := NewFiles(f)
	fileWrite(t, f.sftp, "/home/test/hello.txt", []byte("hello\n"))
	for name, target := range map[string]string{"readme-link": "hello.txt", "images-link": "images", "broken-link": "removed"} {
		if err := f.sftp.Symlink(target, "/home/test/"+name); err != nil {
			t.Fatal(err)
		}
	}
	listing, err := files.List("session", "/home/test")
	if err != nil {
		t.Fatal(err)
	}
	links := map[string]RemoteEntry{}
	for _, entry := range listing.Entries {
		if entry.Type == "link" {
			links[entry.Name] = entry
		}
	}
	if len(links) != 3 || links["readme-link"].LinkTarget != "hello.txt" || links["readme-link"].TargetType != "file" || links["images-link"].TargetType != "directory" || links["broken-link"].TargetType != "missing" {
		t.Fatalf("unexpected symbolic link metadata: %+v", links)
	}
	link := links["readme-link"]
	preview, err := files.Preview("session", link.Path)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := files.Save("session", link.Path, "updated\n", preview.Version)
	if err != nil || updated.Path != link.Path || updated.Content != "updated\n" {
		t.Fatalf("save through link: %+v %v", updated, err)
	}
	if target, err := f.sftp.ReadLink(link.Path); err != nil || target != "hello.txt" {
		t.Fatalf("symbolic link was replaced: %q %v", target, err)
	}
	if got := string(fileRead(t, f.sftp, "/home/test/hello.txt")); got != "updated\n" {
		t.Fatalf("symbolic link target was not updated: %q", got)
	}
}

func TestFilesListPreviewEditConflictAndSafeOperations(t *testing.T) {
	f := newFileFixture(t)
	files := NewFiles(f)
	fileWrite(t, f.sftp, "/home/test/hello.txt", []byte("hello 世界\n"))
	fileWrite(t, f.sftp, "/home/test/images/pixel.png", []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 1})
	list, err := files.List("session", "/home/test")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 || list.Entries[0].Type != "directory" || list.Entries[0].Name != "images" {
		t.Fatalf("unexpected listing: %+v", list)
	}
	preview, err := files.Preview("session", "/home/test/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Kind != "text" || preview.Content != "hello 世界\n" || len(preview.Version) != 64 {
		t.Fatalf("bad text preview: %+v", preview)
	}
	updated, err := files.Save("session", preview.Path, "edited 世界\n", preview.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "edited 世界\n" || updated.Version == preview.Version {
		t.Fatalf("save failed: %+v", updated)
	}
	if _, err = files.Save("session", preview.Path, "must not replace", preview.Version); err == nil || !strings.HasPrefix(err.Error(), "CONFLICT:") {
		t.Fatalf("expected conflict, got %v", err)
	}
	if got := string(fileRead(t, f.sftp, preview.Path)); got != "edited 世界\n" {
		t.Fatalf("conflict replaced original: %q", got)
	}
	image, err := files.Preview("session", "/home/test/images/pixel.png")
	if err != nil {
		t.Fatal(err)
	}
	if image.Kind != "image" || image.Mime != "image/png" || !strings.HasPrefix(image.Content, "data:image/png;base64,") {
		t.Fatalf("bad image: %+v", image)
	}
	if err = files.Create("session", "/home/test", "empty.txt", false); err != nil {
		t.Fatal(err)
	}
	if err = files.Create("session", "/home/test", "empty.txt", false); err == nil {
		t.Fatal("create silently overwrote existing file")
	}
	if err = files.Rename("session", "/home/test/empty.txt", "hello.txt"); err == nil {
		t.Fatal("rename silently overwrote existing file")
	}
	if err = files.Rename("session", "/home/test/empty.txt", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if err = files.Delete("session", "/home/test/renamed.txt", false); err != nil {
		t.Fatal(err)
	}
	if err = files.Delete("session", "/", true); err == nil {
		t.Fatal("root deletion permitted")
	}
	if err = files.Create("session", "/home/test", "../escape", false); err == nil {
		t.Fatal("path traversal filename permitted")
	}
	entries, err := f.sftp.ReadDir("/home/test")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".sshtool-") {
			t.Fatalf("temporary save file leaked: %s", e.Name())
		}
	}
}

func TestFilesRejectBinaryAndInvalidEncoding(t *testing.T) {
	f := newFileFixture(t)
	files := NewFiles(f)
	for name, data := range map[string][]byte{"binary.bin": {0, 1, 2}, "invalid.txt": {0xff, 0xfe, 0x20}, "large.bin": bytes.Repeat([]byte{0}, TextLimit+1)} {
		fileWrite(t, f.sftp, "/home/test/"+name, data)
		if _, err := files.Preview("session", "/home/test/"+name); err == nil {
			t.Fatalf("preview accepted %s", name)
		}
	}
	if _, err := files.Preview("session", "/home/test/images"); err == nil {
		t.Fatal("directory preview accepted")
	}
}
