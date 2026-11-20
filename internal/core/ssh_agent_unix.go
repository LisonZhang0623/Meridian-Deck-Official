//go:build !windows

package core

import (
	"context"
	"errors"
	"net"
	"os"
)

func dialAgent(ctx context.Context) (net.Conn, error) {
	address := os.Getenv("SSH_AUTH_SOCK")
	if address == "" {
		return nil, errors.New("未设置 SSH_AUTH_SOCK")
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}
