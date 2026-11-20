package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// An unresponsive SSH peer can stall its global listen/cancel request. Keep
// each worker slot until the peer replies or the SSH transport actually ends.
var remoteForwardSlots = make(chan struct{}, 24)

type remoteForwardListener struct {
	net.Listener
	once sync.Once
}

func (l *remoteForwardListener) Close() error {
	l.once.Do(func() {
		go func() {
			defer func() { <-remoteForwardSlots }()
			_ = l.Listener.Close()
		}()
	})
	return nil
}

func listensOnRemote(mode string) bool {
	return mode == "remote" || mode == "remote-http" || mode == "remote-dynamic"
}

func localForwardListenError(address string, err error) error {
	// Winsock returns these codes from bind instead of the POSIX errno values.
	const (
		wsaAccessDenied syscall.Errno = 10013
		wsaAddressInUse syscall.Errno = 10048
	)
	var reason string
	switch {
	case errors.Is(err, syscall.EADDRINUSE), runtime.GOOS == "windows" && errors.Is(err, wsaAddressInUse):
		reason = "端口已被占用"
	case errors.Is(err, fs.ErrPermission), runtime.GOOS == "windows" && errors.Is(err, wsaAccessDenied):
		reason = "系统拒绝绑定该端口（可能被程序独占或由系统保留）"
	default:
		return fmt.Errorf("本地监听 %s 失败（%w）", address, err)
	}
	return fmt.Errorf("本地监听 %s 失败，%s。请换一个端口，或填 0 自动分配。若要共享已运行的 HTTP 代理，请选择“共享本机 HTTP 代理”。（%w）", address, reason, err)
}

func listenForward(ctx context.Context, client *ssh.Client, config ForwardConfig) (net.Listener, error) {
	if !listensOnRemote(config.Mode) {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.LocalPort))
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
		if err != nil {
			return nil, localForwardListenError(address, err)
		}
		return listener, nil
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.RemotePort))
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case remoteForwardSlots <- struct{}{}:
	default:
		return nil, errors.New("远程端口转发请求过多，请稍后重试")
	}
	type result struct {
		listener net.Listener
		err      error
	}
	done := make(chan result)
	go func() {
		listener, err := client.Listen("tcp", address)
		if err == nil {
			listener = &remoteForwardListener{Listener: listener}
		} else {
			<-remoteForwardSlots
		}
		select {
		case done <- result{listener, err}:
		case <-ctx.Done():
			if listener != nil {
				_ = listener.Close()
			}
		}
	}()
	select {
	case value := <-done:
		if value.err != nil {
			return nil, fmt.Errorf("服务器监听 %s 失败。请检查 SSH 服务端是否允许远程转发，以及监听端口是否可用。（%w）", address, value.err)
		}
		return value.listener, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("等待服务器监听 %s 超时（%w）", address, ctx.Err())
		}
		return nil, ctx.Err()
	}
}

func connectReverseSOCKS(parent context.Context, incoming net.Conn, password string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	// SSH forwarded channels do not implement deadlines. Closing the channel
	// interrupts stalled SOCKS negotiation without closing the SSH connection.
	stopIncoming := context.AfterFunc(ctx, func() { _ = incoming.Close() })
	defer stopIncoming()

	destination, err := acceptAuthenticatedSOCKSConnect(incoming, password)
	if err != nil {
		return nil, err
	}
	target, err := (&net.Dialer{}).DialContext(ctx, "tcp", destination)
	if err != nil {
		_ = writeSOCKSReply(incoming, 5)
		return nil, err
	}
	stopTarget := context.AfterFunc(ctx, func() { _ = target.Close() })
	defer stopTarget()
	if err = writeSOCKSReply(incoming, 0); err != nil {
		_ = target.Close()
		return nil, err
	}
	if !stopIncoming() || !stopTarget() || ctx.Err() != nil {
		_ = target.Close()
		return nil, ctx.Err()
	}
	return target, nil
}
