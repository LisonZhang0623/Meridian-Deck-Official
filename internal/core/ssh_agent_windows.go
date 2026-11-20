//go:build windows

package core

import (
	"context"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
)

func dialAgent(ctx context.Context) (net.Conn, error) {
	address := os.Getenv("SSH_AUTH_SOCK")
	if address == "" {
		address = `\\.\pipe\openssh-ssh-agent`
	}
	if strings.HasPrefix(strings.ToLower(address), `\\.\pipe\`) {
		return winio.DialPipeContext(ctx, address)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}
