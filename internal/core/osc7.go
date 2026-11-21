package core

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

//go:embed osc7_hook.sh
var osc7Hook string

type OSC7Status struct {
	Shell      string `json:"shell"`
	Path       string `json:"path,omitempty"`
	Configured bool   `json:"configured"`
}

const osc7ShellProbe = `printf '%s\000' "$SHELL" "$HOME"`
const osc7ZshDirProbe = `printf '%s' "${ZDOTDIR:-$HOME}"`

func (f *Files) osc7Target(id string) (OSC7Status, error) {
	var status OSC7Status
	if err := ValidateID(id); err != nil {
		return status, err
	}
	sshClient, err := f.conns.Client(id)
	if err != nil {
		return status, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, _, err := execBounded(ctx, sshClient, osc7ShellProbe, 8192)
	if err != nil {
		return status, errors.New("无法识别远端登录 Shell")
	}
	fields := strings.Split(output, "\x00")
	if len(fields) != 3 || fields[2] != "" || !path.IsAbs(fields[0]) {
		return status, errors.New("远端未报告登录 Shell")
	}
	status.Shell = path.Base(fields[0])
	dir := fields[1]
	switch status.Shell {
	case "bash":
	case "zsh":
		dir, _, err = execBounded(ctx, sshClient, osc7ZshDirProbe, 4096)
		if err != nil {
			return status, errors.New("无法定位 Zsh 配置目录")
		}
	default:
		return status, nil
	}
	if !path.IsAbs(dir) || ValidateRemotePath(dir) != nil {
		return status, errors.New("远端 Shell 配置目录无效")
	}
	status.Path = path.Join(dir, "."+status.Shell+"rc")
	return status, nil
}

func createOSC7Config(c *sftp.Client, remote string) error {
	// Standard SFTP rename publishes a complete file and refuses an existing destination.
	tmp := path.Join(path.Dir(remote), ".meridian-osc7-"+NewID()+".tmp")
	w, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	defer c.Remove(tmp)
	if err = w.Chmod(0600); err == nil {
		_, err = w.Write([]byte(osc7Hook))
	}
	closeErr := w.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return c.Rename(tmp, remote)
}

// A check only reads. An install must name the same startup file the UI showed.
func (f *Files) OSC7(id, expectedPath string) (OSC7Status, error) {
	status, err := f.osc7Target(id)
	if err != nil {
		return status, err
	}
	if expectedPath != "" && expectedPath != status.Path {
		return status, errors.New("远端 Shell 配置文件已变化或不受支持，请重新检查")
	}
	if status.Path == "" {
		return status, nil
	}
	if expectedPath != "" {
		f.osc7Mu.Lock()
		defer f.osc7Mu.Unlock()
	}
	c, err := f.client(id, status.Path)
	if err != nil {
		return status, err
	}
	_, err = c.Lstat(status.Path)
	if os.IsNotExist(err) {
		if expectedPath == "" {
			return status, nil
		}
		err = createOSC7Config(c, status.Path)
		status.Configured = err == nil
		return status, err
	}
	if err != nil {
		return status, err
	}

	info, err := c.Stat(status.Path)
	if err != nil {
		return status, err
	}
	if !info.Mode().IsRegular() || info.Size() > TextLimit {
		return status, errors.New("Shell 配置文件无法作为普通文本读取")
	}
	doc, err := f.Preview(id, status.Path)
	if err != nil {
		return status, err
	}
	if doc.Kind != "text" || doc.Encoding != "UTF-8" {
		return status, errors.New("Shell 配置文件需要是 UTF-8 文本")
	}
	// Also recognize the Bash hook documented in earlier releases, even before it emits.
	configured := strings.Contains(doc.Content, strings.TrimSuffix(osc7Hook, "\n"))
	if !configured && status.Shell == "bash" {
		var registration, reporter bool
		for line := range strings.SplitSeq(doc.Content, "\n") {
			line = strings.TrimSpace(line)
			registration = registration || line == "PROMPT_COMMAND+=(__sshtool_report_directory)"
			reporter = reporter || strings.HasPrefix(line, `printf '\033]7;file://`)
		}
		configured = registration && reporter && strings.Contains(doc.Content, "__sshtool_report_directory()")
	}
	if configured {
		status.Configured = true
		return status, nil
	}
	if strings.Contains(doc.Content, "# >>> Meridian Deck OSC 7 >>>") || strings.Contains(doc.Content, "# <<< Meridian Deck OSC 7 <<<") {
		return status, errors.New("已有 OSC 7 配置块被修改，请在编辑器中检查")
	}
	if expectedPath == "" {
		return status, nil
	}
	content := doc.Content
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if _, err = f.Save(id, status.Path, content+osc7Hook, doc.Version); err != nil {
		return status, err
	}
	status.Configured = true
	return status, nil
}
