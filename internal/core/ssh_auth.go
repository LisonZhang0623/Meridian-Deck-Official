package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type sshChallenge struct {
	id       string
	count    int
	response chan []string
}

func (m *SSHManager) authMethods(s *managedSession, p Profile, creds Credentials, label string, timer *time.Timer) ([]ssh.AuthMethod, func(), error) {
	var methods []ssh.AuthMethod
	var socket net.Conn
	var cancelSocket func() bool
	closeAuth := func() {
		if cancelSocket != nil {
			cancelSocket()
		}
		if socket != nil {
			_ = socket.Close()
		}
	}
	switch p.AuthType {
	case "key":
		key, err := os.ReadFile(p.KeyPath)
		if err != nil {
			return nil, closeAuth, fmt.Errorf("读取私钥失败：%w", err)
		}
		defer clear(key)
		var signer ssh.Signer
		if creds.Passphrase != nil {
			passphrase := []byte(*creds.Passphrase)
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
			clear(passphrase)
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, closeAuth, errors.New("无法读取私钥，请检查私钥格式与口令")
		}
		methods = append(methods, ssh.PublicKeys(signer))
	case "agent":
		methods = append(methods, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			if socket == nil {
				ctx, cancel := context.WithTimeout(s.ctx, profileTimeout(p))
				defer cancel()
				var err error
				socket, err = dialAgent(ctx)
				if err != nil {
					return nil, fmt.Errorf("无法连接本机 SSH Agent，请检查服务及已加载的密钥：%w", err)
				}
				conn := socket
				cancelSocket = context.AfterFunc(s.ctx, func() { _ = conn.Close() })
			}
			if err := socket.SetDeadline(time.Now().Add(profileTimeout(p))); err != nil {
				return nil, err
			}
			return agent.NewClient(socket).Signers()
		}))
	case "interactive":
	case "password", "":
		password := ""
		if creds.Password != nil {
			password = *creds.Password
		}
		methods = append(methods, ssh.Password(password))
	default:
		return nil, closeAuth, errors.New("未知的身份验证方式")
	}
	if p.AuthType == "interactive" || p.KeyboardInteractive {
		methods = append(methods, ssh.KeyboardInteractive(func(_ string, instruction string, questions []string, echo []bool) ([]string, error) {
			if len(questions) == 0 {
				return []string{}, nil
			}
			timer.Stop()
			defer timer.Reset(profileTimeout(p))
			return m.askChallenge(s, p, label, instruction, questions, echo)
		}))
	}
	return methods, closeAuth, nil
}

func (m *SSHManager) askChallenge(s *managedSession, p Profile, label, instruction string, questions []string, echo []bool) ([]string, error) {
	if len(questions) > 16 || len(questions) != len(echo) || validateText(instruction, "服务器验证提示", 4096) != nil {
		return nil, errors.New("服务器验证提示超出允许范围")
	}
	for _, question := range questions {
		if validateText(question, "服务器验证问题", 1024) != nil {
			return nil, errors.New("服务器验证问题格式无效")
		}
	}
	challenge := &sshChallenge{id: NewID(), count: len(questions), response: make(chan []string, 1)}
	m.eventGate.Lock()
	m.mu.Lock()
	if m.sessions[s.id] != s || s.challenge != nil {
		m.mu.Unlock()
		m.eventGate.Unlock()
		return nil, context.Canceled
	}
	s.challenge = challenge
	m.mu.Unlock()
	m.emit(Event{
		"type": "auth-challenge", "sessionId": s.id, "generation": s.generation, "challengeId": challenge.id,
		"host": net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), "label": label, "instruction": instruction,
		"questions": questions, "echo": echo,
	})
	m.eventGate.Unlock()
	defer func() {
		m.mu.Lock()
		if s.challenge == challenge {
			s.challenge = nil
		}
		m.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	select {
	case answers := <-challenge.response:
		if answers == nil {
			return nil, errors.New("已取消交互式身份验证")
		}
		return answers, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *SSHManager) AnswerChallenge(id, challengeID string, answers []string) error {
	if err := ValidateID(challengeID); err != nil {
		return err
	}
	if len(answers) > 16 {
		return errors.New("身份验证回复过多")
	}
	for _, value := range answers {
		if err := validateText(value, "身份验证回复", 4096); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.challenge == nil || s.challenge.id != challengeID {
		return errors.New("身份验证提示已失效")
	}
	if answers != nil && len(answers) != s.challenge.count {
		return errors.New("身份验证回复数量不匹配")
	}
	challenge := s.challenge
	s.challenge = nil
	challenge.response <- answers
	return nil
}
