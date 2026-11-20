package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type ftpFixture struct {
	port    int
	blocked chan struct{}
}

// A small RFC 959 / RFC 3659 fixture tests the real FTP control and data
// sockets using an isolated in-memory filesystem on loopback.
func startFTPFixture(t *testing.T, fallback bool) ftpFixture {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	fixture := ftpFixture{port: listener.Addr().(*net.TCPAddr).Port, blocked: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptTCP()
		if err != nil {
			done <- nil
			return
		}
		defer conn.Close()
		done <- fixture.serve(conn, fallback)
	}()
	t.Cleanup(func() {
		listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("FTP fixture: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("FTP fixture did not stop")
		}
	})
	return fixture
}

func (f ftpFixture) serve(conn net.Conn, fallback bool) error {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var dataListener *net.TCPListener
	defer func() {
		if dataListener != nil {
			dataListener.Close()
		}
	}()
	files := map[string][]byte{"/fixtures/seed.txt": []byte("existing file\n")}
	directory, renameFrom := "/fixtures", ""
	validUser, loggedIn := false, false
	reply := func(format string, args ...any) { fmt.Fprintf(conn, format+"\r\n", args...) }
	reply("220 Loopback FTP fixture")
	commands := bufio.NewScanner(conn)
	for commands.Scan() {
		command, argument, _ := strings.Cut(commands.Text(), " ")
		if !loggedIn && command != "USER" && command != "PASS" {
			reply("530 Login required")
			continue
		}
		switch command {
		case "USER":
			validUser = argument == "fixture"
			reply("331 Password required")
		case "PASS":
			loggedIn = validUser && argument == "fixture-pass"
			if loggedIn {
				reply("230 Logged in")
			} else {
				reply("530 Invalid credentials")
			}
		case "FEAT":
			reply("211-Features\r\n MLST type*;size*;modify*;\r\n UTF8\r\n211 End")
		case "TYPE", "OPTS":
			reply("200 OK")
		case "PWD":
			reply("257 %q", directory)
		case "CWD":
			if argument != "/fixtures" {
				reply("550 Directory not found")
				continue
			}
			directory = argument
			reply("250 Directory changed")
		case "EPSV", "PASV":
			if command == "EPSV" && fallback {
				reply("502 EPSV unavailable")
				continue
			}
			if dataListener != nil {
				dataListener.Close()
			}
			var err error
			dataListener, err = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				return err
			}
			_ = dataListener.SetDeadline(time.Now().Add(5 * time.Second))
			port := dataListener.Addr().(*net.TCPAddr).Port
			if command == "EPSV" {
				reply("229 Entering Extended Passive Mode (|||%d|)", port)
			} else {
				// Clients must use the control peer rather than trusting this address.
				reply("227 Entering Passive Mode (127,0,0,2,%d,%d)", port/256, port%256)
			}
		case "MLSD", "RETR", "STOR":
			if dataListener == nil {
				reply("425 Use passive mode")
				continue
			}
			data, err := dataListener.AcceptTCP()
			dataListener.Close()
			dataListener = nil
			if err != nil {
				return err
			}
			_ = data.SetDeadline(time.Now().Add(8 * time.Second))
			reply("150 Transfer starting")
			switch command {
			case "MLSD":
				fmt.Fprint(data, "type=dir;modify=20261109000000; example-dir\r\n")
				for name, content := range files {
					if path.Dir(name) == argument {
						fmt.Fprintf(data, "type=file;size=%d;modify=20261109000000; %s\r\n", len(content), path.Base(name))
					}
				}
			case "RETR":
				if argument == "/fixtures/blocked.bin" {
					close(f.blocked)
					_, err = io.Copy(io.Discard, data)
					data.Close()
					return err
				} else {
					_, err = data.Write(files[argument])
				}
			case "STOR":
				files[argument], err = io.ReadAll(io.LimitReader(data, 1024*1024))
			}
			data.Close()
			if err != nil {
				reply("426 Transfer interrupted")
			} else {
				reply("226 Transfer complete")
			}
		case "RNFR":
			if _, ok := files[argument]; !ok {
				reply("550 File not found")
				continue
			}
			renameFrom = argument
			reply("350 Ready to rename")
		case "RNTO":
			if renameFrom == "" {
				reply("503 RNFR required")
				continue
			}
			files[argument] = files[renameFrom]
			delete(files, renameFrom)
			renameFrom = ""
			reply("250 Rename complete")
		case "DELE":
			delete(files, argument)
			reply("250 Deleted")
		case "QUIT":
			reply("221 Goodbye")
			return nil
		default:
			reply("502 Command unsupported")
		}
	}
	return commands.Err()
}

func (f ftpFixture) config() FTPConfig {
	return FTPConfig{Host: "127.0.0.1", Port: f.port, Username: "fixture", Password: "fixture-pass"}
}

func TestFTPLoopbackTransfersAndOverwrite(t *testing.T) {
	for _, test := range []struct {
		name     string
		fallback bool
	}{{"EPSV", false}, {"PASV", true}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := startFTPFixture(t, test.fallback)
			client, err := ConnectFTP(t.Context(), fixture.config())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			directory, err := client.List("/fixtures")
			if err != nil || directory.Path != "/fixtures" || len(directory.Entries) != 2 || directory.Entries[0].Type != "directory" || directory.Entries[1].Name != "seed.txt" {
				t.Fatalf("unexpected FTP directory: %+v %v", directory, err)
			}
			local := t.TempDir()
			source, target := filepath.Join(local, "upload.bin"), filepath.Join(local, "download.bin")
			payload := bytes.Repeat([]byte{0, 1, 253, 254, 255, '\n'}, 1024)
			if err := os.WriteFile(source, payload, 0600); err != nil {
				t.Fatal(err)
			}
			if err := client.Upload("/fixtures", source, nil); err != nil {
				t.Fatal(err)
			}
			directory, err = client.List("")
			if err != nil || len(directory.Entries) != 3 || directory.Entries[2].Name != "upload.bin" || directory.Entries[2].Size != int64(len(payload)) {
				t.Fatalf("upload not published cleanly: %+v %v", directory, err)
			}
			if err := os.WriteFile(source, []byte("unapproved replacement"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := client.Upload("/fixtures", source, nil); err == nil {
				t.Fatal("FTP upload overwrote a file without confirmation")
			}
			if err := client.Download("/fixtures/upload.bin", target); err != nil {
				t.Fatal(err)
			}
			downloaded, err := os.ReadFile(target)
			if err != nil || sha256.Sum256(payload) != sha256.Sum256(downloaded) {
				t.Fatalf("FTP checksum mismatch: %v", err)
			}
		})
	}
}

func TestFTPCancelBlockedDownloadPreservesDestination(t *testing.T) {
	fixture := startFTPFixture(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client, err := ConnectFTP(ctx, fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	local := t.TempDir()
	target := filepath.Join(local, "original.bin")
	if err := os.WriteFile(target, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Download("/fixtures/blocked.bin", target) }()
	select {
	case <-fixture.blocked:
	case <-time.After(4 * time.Second):
		t.Fatal("FTP download did not reach data socket")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled FTP download succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("FTP download did not stop on cancellation")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "keep this file" {
		t.Fatalf("cancelled FTP download modified destination: %v", err)
	}
	entries, err := os.ReadDir(local)
	if err != nil || len(entries) != 1 {
		t.Fatalf("FTP staging file was retained: %v %v", entries, err)
	}
}
