//go:build windows

package core

import (
	"net"
	"testing"

	"github.com/Microsoft/go-winio"
)

func testAgentListener(t *testing.T) net.Listener {
	t.Helper()
	address := `\\.\pipe\sshtool-test-` + NewID()
	listener, err := winio.ListenPipe(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", address)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}
