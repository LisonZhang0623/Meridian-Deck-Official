package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
)

type FTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	TLS      bool   `json:"tls"`
}

// The FTP library serializes control commands but does not expose cancellation.
// Keep the data and control sockets so disconnect can unblock an active call.
type FTPClient struct {
	ctx     context.Context
	cancel  context.CancelFunc
	ops     sync.Mutex
	mu      sync.Mutex
	sockets map[*ftpSocket]struct{}
	control *ftpSocket
	server  *ftp.ServerConn
	until   time.Time
}

type ftpSocket struct {
	net.Conn
	owner *FTPClient
}

func (s *ftpSocket) Close() error {
	s.owner.mu.Lock()
	delete(s.owner.sockets, s)
	s.owner.mu.Unlock()
	return s.Conn.Close()
}

func ConnectFTP(ctx context.Context, config FTPConfig) (*FTPClient, error) {
	profile := Profile{ID: "ftp", Name: "FTP", Host: config.Host, Port: config.Port, Username: config.Username, AuthType: "password", Timeout: 20}
	valid, err := ValidateProfile(profile)
	if err != nil {
		return nil, err
	}
	if len(config.Password) > 4096 || strings.ContainsAny(config.Password, "\x00\r\n") {
		return nil, errors.New("FTP 密码不能包含控制字符")
	}
	host := strings.Trim(valid.Host, "[]")
	address := net.JoinHostPort(host, strconv.Itoa(valid.Port))
	lifetime, cancel := context.WithCancel(ctx)
	client := &FTPClient{ctx: lifetime, cancel: cancel, sockets: make(map[*ftpSocket]struct{}), until: time.Now().Add(20 * time.Second)}
	stop := context.AfterFunc(lifetime, client.closeSockets)
	defer func() {
		if client.server == nil {
			stop()
			client.Close()
		}
	}()
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	dialer := net.Dialer{Timeout: 15 * time.Second}
	dial := func(network, address string) (net.Conn, error) {
		raw, err := dialer.DialContext(lifetime, network, address)
		if err != nil {
			return nil, err
		}
		socket := &ftpSocket{Conn: raw, owner: client}
		client.mu.Lock()
		if lifetime.Err() != nil {
			client.mu.Unlock()
			_ = raw.Close()
			return nil, lifetime.Err()
		}
		isControl := client.control == nil
		if isControl {
			client.control = socket
		}
		client.sockets[socket] = struct{}{}
		_ = raw.SetDeadline(client.until)
		client.mu.Unlock()
		if config.TLS && !isControl {
			return tls.Client(socket, tlsConfig), nil
		}
		return socket, nil
	}
	options := []ftp.DialOption{ftp.DialWithDialFunc(dial), ftp.DialWithTrustPasvIP(false), ftp.DialWithShutTimeout(15 * time.Second)}
	if config.TLS {
		options = append(options, ftp.DialWithExplicitTLS(tlsConfig))
	}
	server, err := ftp.Dial(address, options...)
	if err != nil {
		return nil, fmt.Errorf("无法连接 FTP 服务器：%w", err)
	}
	if err = server.Login(valid.Username, config.Password); err != nil {
		return nil, errors.New("FTP 登录失败，请检查凭据和加密设置")
	}
	client.server = server
	return client, nil
}

func (c *FTPClient) closeSockets() {
	c.mu.Lock()
	sockets := make([]*ftpSocket, 0, len(c.sockets))
	for socket := range c.sockets {
		sockets = append(sockets, socket)
	}
	c.mu.Unlock()
	for _, socket := range sockets {
		_ = socket.Close()
	}
}

func (c *FTPClient) Close() {
	c.cancel()
	c.closeSockets()
}

func (c *FTPClient) run(timeout time.Duration, work func(*ftp.ServerConn) error) error {
	c.ops.Lock()
	defer c.ops.Unlock()
	if c.ctx.Err() != nil {
		return errors.New("FTP 连接已断开")
	}
	c.mu.Lock()
	c.until = time.Now().Add(timeout)
	_ = c.control.SetDeadline(c.until)
	c.mu.Unlock()
	return work(c.server)
}

func ftpPath(remote string) error {
	if err := ValidateRemotePath(remote); err != nil {
		return err
	}
	if !path.IsAbs(remote) || strings.ContainsRune(remote, '\\') {
		return errors.New("请输入 FTP 绝对路径")
	}
	return nil
}

func (c *FTPClient) List(remote string) (Directory, error) {
	result := Directory{Entries: []RemoteEntry{}}
	err := c.run(25*time.Second, func(server *ftp.ServerConn) error {
		if remote != "" {
			if err := ftpPath(remote); err != nil {
				return err
			}
			if err := server.ChangeDir(remote); err != nil {
				return err
			}
		}
		base, err := server.CurrentDir()
		if err != nil {
			return err
		}
		if err = ftpPath(base); err != nil {
			return err
		}
		entries, err := server.List(base)
		if err != nil {
			return err
		}
		if len(entries) > 10000 {
			return errors.New("FTP 目录超过 10000 个条目，请选择更小的目录")
		}
		result.Path = base
		for _, entry := range entries {
			if ValidateEntryName(entry.Name) != nil || entry.Size > 1<<63-1 {
				continue
			}
			kind := "link"
			switch entry.Type {
			case ftp.EntryTypeFolder:
				kind = "directory"
			case ftp.EntryTypeFile:
				kind = "file"
			}
			result.Entries = append(result.Entries, RemoteEntry{Name: entry.Name, Path: path.Join(base, entry.Name), Type: kind, Size: int64(entry.Size), Modified: entry.Time.UnixMilli()})
		}
		sort.Slice(result.Entries, func(i, j int) bool {
			a, b := result.Entries[i], result.Entries[j]
			if (a.Type == "directory") != (b.Type == "directory") {
				return a.Type == "directory"
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
		return nil
	})
	return result, err
}

func (c *FTPClient) Download(remote, local string) error {
	if err := ftpPath(remote); err != nil {
		return err
	}
	if !filepath.IsAbs(local) {
		return errors.New("下载路径无效")
	}
	return c.run(15*time.Minute, func(server *ftp.ServerConn) error {
		file, err := os.CreateTemp(filepath.Dir(local), ".sshtool-ftp-*.part")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		defer file.Close()
		stream, err := server.Retr(remote)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, stream)
		if err = errors.Join(copyErr, stream.Close()); err != nil {
			return err
		}
		if err = errors.Join(file.Sync(), file.Close()); err != nil {
			return err
		}
		return os.Rename(file.Name(), local)
	})
}

func (c *FTPClient) Upload(directory, local string, overwrite func(string) bool) error {
	if err := ftpPath(directory); err != nil {
		return err
	}
	if !filepath.IsAbs(local) {
		return errors.New("上传路径无效")
	}
	name := filepath.Base(local)
	if err := ValidateEntryName(name); err != nil {
		return err
	}
	return c.run(15*time.Minute, func(server *ftp.ServerConn) error {
		file, err := os.Open(local)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("只支持上传普通文件")
		}
		remote := path.Join(directory, name)
		before, err := ftpDestination(server, directory, name)
		if err != nil {
			return err
		}
		if before != nil && (before.Type != ftp.EntryTypeFile || overwrite == nil || !overwrite(remote)) {
			return errors.New("已取消覆盖同名 FTP 条目")
		}
		staged := path.Join(directory, ".sshtool-"+NewID()+".part")
		renamed := false
		defer func() {
			if !renamed {
				_ = server.Delete(staged)
			}
		}()
		if err := server.Stor(staged, file); err != nil {
			return err
		}
		after, err := ftpDestination(server, directory, name)
		if err != nil {
			return err
		}
		if (before == nil) != (after == nil) || before != nil && (before.Type != after.Type || before.Size != after.Size || !before.Time.Equal(after.Time)) {
			return errors.New("远程目标在上传期间发生变化，请重新确认后重试")
		}
		err = server.Rename(staged, remote)
		renamed = err == nil
		return err
	})
}

func ftpDestination(server *ftp.ServerConn, directory, name string) (*ftp.Entry, error) {
	entries, err := server.List(directory)
	if err != nil {
		return nil, err
	}
	var exact *ftp.Entry
	for _, entry := range entries {
		if entry.Name == name {
			exact = entry
		} else if strings.EqualFold(entry.Name, name) {
			return nil, errors.New("FTP 目录已有仅大小写不同的名称，请选择其他名称")
		}
	}
	return exact, nil
}
