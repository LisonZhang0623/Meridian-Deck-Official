package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"path"

	"sshtool/internal/core"
)

func (s *Service) disconnectFTP() {
	s.ftpMu.Lock()
	s.ftpEpoch++
	client, cancel := s.ftpClient, s.ftpCancel
	s.ftpClient, s.ftpCancel = nil, nil
	s.ftpMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		client.Close()
	}
}

func (s *Service) connectFTP(config core.FTPConfig) (core.Directory, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s.ftpMu.Lock()
	previous, stopPrevious := s.ftpClient, s.ftpCancel
	s.ftpEpoch++
	epoch := s.ftpEpoch
	s.ftpClient, s.ftpCancel = nil, cancel
	s.ftpMu.Unlock()
	if stopPrevious != nil {
		stopPrevious()
	}
	if previous != nil {
		previous.Close()
	}
	client, err := core.ConnectFTP(ctx, config)
	if err != nil {
		cancel()
		return core.Directory{}, err
	}
	directory, err := client.List("")
	s.ftpMu.Lock()
	if err == nil && epoch == s.ftpEpoch && !s.closed.Load() {
		s.ftpClient = client
		s.ftpMu.Unlock()
		return directory, nil
	}
	s.ftpMu.Unlock()
	client.Close()
	cancel()
	if err == nil {
		err = context.Canceled
	}
	return core.Directory{}, err
}

func (s *Service) callFTP(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "ftp:connect":
		var config core.FTPConfig
		if err := decode(args, &config); err != nil {
			return nil, err
		}
		return s.connectFTP(config)
	case "ftp:disconnect":
		if err := decode(args); err != nil {
			return nil, err
		}
		s.disconnectFTP()
		return nil, nil
	}
	var remote string
	if err := decode(args, &remote); err != nil {
		return nil, err
	}
	s.ftpMu.Lock()
	client := s.ftpClient
	s.ftpMu.Unlock()
	if client == nil {
		return nil, errors.New("请先连接 FTP 服务器")
	}
	if op == "ftp:list" {
		return client.List(remote)
	}
	s.ftpBusy.Add(1)
	defer s.ftpBusy.Add(-1)
	if op == "ftp:download" {
		if s.ui.SaveFile == nil {
			return nil, errors.New("文件保存对话框不可用")
		}
		name := path.Base(remote)
		if err := core.ValidateEntryName(name); err != nil {
			return nil, err
		}
		local, err := s.ui.SaveFile(name)
		if err != nil || local == "" {
			return false, err
		}
		err = client.Download(remote, local)
		return err == nil, err
	}
	if s.ui.SelectFiles == nil {
		return nil, errors.New("文件选择对话框不可用")
	}
	files, err := s.ui.SelectFiles()
	if err != nil {
		return nil, err
	}
	if len(files) > 100 {
		return nil, errors.New("一次最多上传 100 个文件")
	}
	for _, file := range files {
		if err := client.Upload(remote, file, s.ui.ConfirmOverwrite); err != nil {
			return nil, err
		}
	}
	return len(files), nil
}
