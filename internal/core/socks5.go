package core

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	socksVersion         = 5
	socksConnect         = 1
	socksIPv4            = 1
	socksDomain          = 3
	socksIPv6            = 4
	socksNoAuth          = 0
	socksUserPass        = 2
	socksNoMethod        = 0xff
	socksAuthVersion     = 1
	reverseSOCKSUsername = "sshtool"
)

func dialSSHAddress(ctx context.Context, previous *ssh.Client, proxy, address string) (net.Conn, error) {
	if previous != nil {
		if proxy != "" {
			return nil, errors.New("SOCKS5 代理只能用于最外层连接")
		}
		return previous.DialContext(ctx, "tcp", address)
	}
	dialer := &net.Dialer{}
	if proxy == "" {
		return dialer.DialContext(ctx, "tcp", address)
	}
	conn, err := dialer.DialContext(ctx, "tcp", proxy)
	if err != nil {
		return nil, fmt.Errorf("无法连接 SOCKS5 代理：%w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err = requestSOCKSConnect(conn, address); err != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("SOCKS5 代理无法建立连接：%w", err)
	}
	if !stop() {
		_ = conn.Close()
		return nil, ctx.Err()
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func requestSOCKSConnect(conn io.ReadWriter, destination string) error {
	host, portText, err := net.SplitHostPort(destination)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || port <= 0 || port > 65535 || !validTCPHost(host) {
		return errors.New("SOCKS5 目标地址无效")
	}
	if _, err = conn.Write([]byte{socksVersion, 1, 0}); err != nil {
		return err
	}
	var method [2]byte
	if _, err = io.ReadFull(conn, method[:]); err != nil {
		return err
	}
	if method != [2]byte{socksVersion, 0} {
		return errors.New("代理没有接受 SOCKS5 无认证模式")
	}
	request := []byte{socksVersion, socksConnect, 0}
	ip := net.ParseIP(host)
	switch {
	case ip.To4() != nil:
		request = append(append(request, socksIPv4), ip.To4()...)
	case ip.To16() != nil:
		request = append(append(request, socksIPv6), ip.To16()...)
	default:
		request = append(append(request, socksDomain, byte(len(host))), []byte(host)...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err = conn.Write(request); err != nil {
		return err
	}
	var reply [4]byte
	if _, err = io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[0] != socksVersion || reply[2] != 0 {
		return errors.New("SOCKS5 代理回复格式无效")
	}
	if reply[1] != 0 {
		return fmt.Errorf("代理拒绝目标连接（状态 %d）", reply[1])
	}
	_, _, err = readSOCKSAddress(conn, reply[3])
	return err
}

func readSOCKSAddress(reader io.Reader, addressType byte) (string, uint16, error) {
	length := 0
	switch addressType {
	case socksIPv4:
		length = net.IPv4len
	case socksIPv6:
		length = net.IPv6len
	case socksDomain:
		var prefix [1]byte
		if _, err := io.ReadFull(reader, prefix[:]); err != nil {
			return "", 0, err
		}
		length = int(prefix[0])
	default:
		return "", 0, errors.New("SOCKS5 地址类型无效")
	}
	value := make([]byte, length+2)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", 0, err
	}
	host := string(value[:length])
	if addressType != socksDomain {
		host = net.IP(value[:length]).String()
	}
	return host, binary.BigEndian.Uint16(value[length:]), nil
}

func acceptSOCKSConnect(conn io.ReadWriter) (string, error) {
	if err := selectSOCKSMethod(conn, socksNoAuth); err != nil {
		return "", err
	}
	return readSOCKSConnectRequest(conn)
}

func acceptAuthenticatedSOCKSConnect(conn io.ReadWriter, password string) (string, error) {
	if password == "" {
		return "", errors.New("SOCKS5 认证不可用")
	}
	if err := selectSOCKSMethod(conn, socksUserPass); err != nil {
		return "", err
	}
	if err := authenticateSOCKS(conn, password); err != nil {
		return "", err
	}
	return readSOCKSConnectRequest(conn)
}

func selectSOCKSMethod(conn io.ReadWriter, required byte) error {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header[0] != socksVersion || header[1] == 0 {
		return errors.New("无效的 SOCKS5 问候")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	method := byte(socksNoMethod)
	for _, candidate := range methods {
		if candidate == required {
			method = required
			break
		}
	}
	if _, err := conn.Write([]byte{socksVersion, method}); err != nil {
		return err
	}
	if method == socksNoMethod {
		return errors.New("SOCKS5 客户端没有提供所需认证方式")
	}
	return nil
}

func authenticateSOCKS(conn io.ReadWriter, expectedPassword string) error {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header[0] != socksAuthVersion || header[1] == 0 {
		_ = writeSOCKSAuthReply(conn, false)
		return errors.New("无效的 SOCKS5 认证请求")
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, username); err != nil {
		return err
	}
	var length [1]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return err
	}
	password := make([]byte, int(length[0]))
	if _, err := io.ReadFull(conn, password); err != nil {
		return err
	}
	userMatches := subtle.ConstantTimeCompare(username, []byte(reverseSOCKSUsername))
	passwordMatches := subtle.ConstantTimeCompare(password, []byte(expectedPassword))
	clear(password)
	accepted := userMatches&passwordMatches == 1
	if err := writeSOCKSAuthReply(conn, accepted); err != nil {
		return err
	}
	if !accepted {
		return errors.New("SOCKS5 认证失败")
	}
	return nil
}

func writeSOCKSAuthReply(writer io.Writer, accepted bool) error {
	status := byte(1)
	if accepted {
		status = 0
	}
	_, err := writer.Write([]byte{socksAuthVersion, status})
	return err
}

func readSOCKSConnectRequest(conn io.ReadWriter) (string, error) {
	var request [4]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil {
		return "", err
	}
	if request[0] != socksVersion || request[2] != 0 {
		return "", errors.New("无效的 SOCKS5 请求")
	}
	if request[1] != socksConnect {
		_ = writeSOCKSReply(conn, 7)
		return "", errors.New("SOCKS5 仅支持 TCP CONNECT")
	}
	host, port, err := readSOCKSAddress(conn, request[3])
	if err != nil || port == 0 || !validTCPHost(host) {
		_ = writeSOCKSReply(conn, 8)
		return "", errors.New("无效的 SOCKS5 目标地址")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func writeSOCKSReply(writer io.Writer, status byte) error {
	_, err := writer.Write([]byte{socksVersion, status, 0, socksIPv4, 0, 0, 0, 0, 0, 0})
	return err
}
