//go:build !windows

package core

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func testAgentListener(t *testing.T) net.Listener {
	t.Helper()
	// The macOS runner's default temporary root can already be long enough
	// to overflow sockaddr_un when combined with a Go test directory name.
	directory, err := os.MkdirTemp("/tmp", "sshtool-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	address := filepath.Join(directory, "a.sock")
	listener, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", address)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}
