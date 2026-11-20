package core

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

var identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,80}$`)

func validateText(value, name string, max int) error {
	if !utf8.ValidString(value) || len(utf16.Encode([]rune(value))) > max || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s格式不正确", name)
	}
	return nil
}

func ValidateID(value string) error {
	if !identifierPattern.MatchString(value) {
		return errors.New("无效的标识符")
	}
	return nil
}

func ValidateRemotePath(value string) error {
	if err := validateText(value, "远程路径", 4096); err != nil {
		return err
	}
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return errors.New("远程路径不能为空或包含换行")
	}
	return nil
}

func ValidateEntryName(value string) error {
	if err := validateText(value, "文件名", 255); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" || value == "." || value == ".." || strings.ContainsAny(value, "\\/\r\n") {
		return errors.New("名称不能为空，也不能包含路径分隔符")
	}
	return nil
}

func validateTags(values []string) ([]string, error) {
	if len(values) > 200 {
		return nil, errors.New("服务器标签最多 12 个")
	}
	result := make([]string, 0)
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if err := validateText(value, "服务器标签", 24); err != nil {
			return nil, err
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("服务器标签不能包含换行")
		}
		if value != "" && !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	if len(result) > 12 {
		return nil, errors.New("服务器标签最多 12 个")
	}
	return result, nil
}

func ValidateProfile(input Profile) (Profile, error) {
	p := input
	p.Host, p.Name, p.Username, p.Group = strings.TrimSpace(p.Host), strings.TrimSpace(p.Name), strings.TrimSpace(p.Username), strings.TrimSpace(p.Group)
	for _, field := range []struct {
		value, name string
		max         int
	}{
		{p.Host, "服务器地址", 255}, {p.Name, "连接名称", 100}, {p.Username, "用户名", 128}, {p.Group, "分组", 80}, {p.KeyPath, "私钥路径", 4096}, {p.InitialPath, "初始目录", 4096},
	} {
		if err := validateText(field.value, field.name, field.max); err != nil {
			return Profile{}, err
		}
	}
	if p.Host == "" || strings.ContainsAny(p.Host, "/\\") || strings.ContainsFunc(p.Host, unicode.IsSpace) {
		return Profile{}, errors.New("请输入有效的 IP 地址或域名")
	}
	if p.Name == "" || p.Username == "" || strings.ContainsAny(p.Username, "\r\n") {
		return Profile{}, errors.New("连接名称和用户名不能为空或包含换行")
	}
	if p.Port < 1 || p.Port > 65535 {
		return Profile{}, errors.New("端口必须是 1–65535 的整数")
	}
	if p.Timeout < 5 || p.Timeout > 120 {
		return Profile{}, errors.New("连接超时必须为 5–120 秒")
	}
	switch p.AuthType {
	case "password", "key", "agent", "interactive":
	default:
		return Profile{}, errors.New("未知的身份验证方式")
	}
	if p.AuthType == "key" && p.KeyPath == "" {
		return Profile{}, errors.New("请选择私钥文件")
	}
	if p.AuthType == "agent" || p.AuthType == "interactive" {
		p.RememberCredentials = false
	}
	if p.AuthType == "interactive" {
		p.KeyboardInteractive = false
	}
	p.ProxyAddress = strings.TrimSpace(p.ProxyAddress)
	if p.ProxyAddress != "" {
		host, port, err := net.SplitHostPort(p.ProxyAddress)
		number, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil || number < 1 || number > 65535 || !validTCPHost(host) {
			return Profile{}, errors.New("请输入 SOCKS5 代理的主机和端口，例如 127.0.0.1:1080")
		}
		if p.JumpProfileID != "" {
			return Profile{}, errors.New("代理只可配置在直连服务器或最外层跳板机")
		}
		p.ProxyAddress = net.JoinHostPort(strings.ToLower(host), strconv.Itoa(number))
	}
	if strings.ContainsAny(p.InitialPath, "\r\n") {
		return Profile{}, errors.New("初始目录不能包含换行")
	}
	if p.LastConnectedAt < 0 || p.LastConnectedAt > 9007199254740991 {
		return Profile{}, errors.New("最近连接时间格式不正确")
	}
	if p.ID == "" {
		p.ID = NewID()
	}
	if err := ValidateID(p.ID); err != nil {
		return Profile{}, err
	}
	if p.JumpProfileID != "" {
		if err := ValidateID(p.JumpProfileID); err != nil {
			return Profile{}, err
		}
	}
	tags, err := validateTags(p.Tags)
	if err != nil {
		return Profile{}, err
	}
	p.Tags, p.HasCredentials, p.JumpIdentity = tags, false, ""
	return p, nil
}

func validTCPHost(host string) bool {
	if host == "" || len(host) > 255 || strings.ContainsAny(host, "/\\[]") || strings.ContainsFunc(host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}
	return !strings.ContainsRune(host, ':') || net.ParseIP(host) != nil
}

func ValidateSettings(value Settings) (Settings, error) {
	if (value.SidebarSide != "left" && value.SidebarSide != "right") || (value.Theme != "light" && value.Theme != "dark" && value.Theme != "system") {
		return Settings{}, errors.New("无效的界面设置")
	}
	if value.TerminalFontSize < 10 || value.TerminalFontSize > 32 {
		return Settings{}, errors.New("终端字号必须是 10–32 的整数")
	}
	if value.EditorFontSize == 0 {
		value.EditorFontSize = defaultEditorFontSize
	}
	if value.EditorFontSize < 10 || value.EditorFontSize > 32 {
		return Settings{}, errors.New("编辑器字号必须是 10–32 的整数")
	}
	if value.UIZoom == 0 {
		value.UIZoom = defaultUIZoom
	}
	if value.UIZoom < 80 || value.UIZoom > 150 {
		return Settings{}, errors.New("工作台缩放必须是 80–150 的整数百分比")
	}
	if value.SidebarWidth != 0 && (value.SidebarWidth < 236 || value.SidebarWidth > 640) {
		return Settings{}, errors.New("侧栏宽度必须是 236–640 的整数，或使用自动宽度")
	}
	if value.GPURefreshSeconds < 1 || value.GPURefreshSeconds > 3600 {
		return Settings{}, errors.New("显卡刷新间隔必须是 1–3600 的整数秒")
	}
	switch value.SidebarPanel {
	case "servers", "files", "gpu", "commands", "logs", "forwards", "resources":
	default:
		return Settings{}, errors.New("无效的侧栏面板")
	}
	switch value.StartupAnimation {
	case "":
		value.StartupAnimation = DefaultStartupAnimation
	case DefaultStartupAnimation, "orbit", "random", "off":
	default:
		return Settings{}, errors.New("无效的启动动画设置")
	}
	switch value.WindowCloseAction {
	case "":
		value.WindowCloseAction = CloseAsk
	case CloseAsk, CloseTray, CloseQuit:
	default:
		return Settings{}, errors.New("无效的关闭窗口设置")
	}
	return value, nil
}

func ValidateCredentials(value Credentials) error {
	if value.Password != nil {
		if err := validateText(*value.Password, "密码", 16384); err != nil {
			return err
		}
	}
	if value.Passphrase != nil {
		if err := validateText(*value.Passphrase, "私钥口令", 16384); err != nil {
			return err
		}
	}
	return nil
}
