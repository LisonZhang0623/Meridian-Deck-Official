package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sshtool/internal/core"
)

type UI struct {
	SelectFiles         func() ([]string, error)
	SelectDirectory     func(string) (string, error)
	ConfirmDeleteTree   func(string, int) bool
	SelectKey           func() (string, error)
	SelectConfig        func() (string, error)
	SaveConfig          func(string) (string, error)
	SaveFile            func(string) (string, error)
	ConfirmOverwrite    func(string) bool
	ConfirmDragTransfer func(direction string, sourceNames []string, destination string) bool
	OpenEditorWindow    func(string)
	TraySupported       bool
	OpenURL             func(string) error
	DragFile            func(string) error
}
type Result struct {
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}
type localGrant struct {
	path    string
	expires time.Time
	drop    os.FileInfo
}

// Service deliberately exposes one allowlisted, typed dispatcher. File paths
// entering from the renderer must have been granted by a native drop/dialog.
type Service struct {
	store        *core.Store
	commands     *core.CommandsStore
	ssh          *core.SSHManager
	tools        *core.Tools
	files        *core.Files
	transfers    *core.Transfers
	drag         *core.DragDownloads
	ui           UI
	emit         core.Emit
	closed       atomic.Bool
	dragging     atomic.Bool
	mu           sync.Mutex
	grants       map[string]localGrant
	filePlans    map[string]directoryPlan
	cacheRoot    string
	dirty        atomic.Bool
	frames       map[string]terminalFrame
	busy         map[string]bool
	ftpMu        sync.Mutex
	ftpClient    *core.FTPClient
	ftpCancel    context.CancelFunc
	ftpEpoch     uint64
	ftpBusy      atomic.Int32
	nas          nasSelection
	nasSelecting atomic.Bool
	updates      updateChecker
}
type Hooks struct {
	StartupSettings core.Settings
	Drop            func([]string) []string
	Close           func()
	Dirty           func() bool
	CloseAction     func() string
}

func New(directory, legacyDirectory, cacheRoot string, ui UI, output core.Emit) (*Service, Hooks, error) {
	store, err := core.NewStore(directory, legacyDirectory, core.NewSystemCipher())
	if err != nil {
		return nil, Hooks{}, err
	}
	commands, err := core.NewCommandsStore(directory)
	if err != nil {
		return nil, Hooks{}, err
	}
	s := &Service{store: store, commands: commands, ui: ui, grants: map[string]localGrant{}, cacheRoot: cacheRoot, frames: map[string]terminalFrame{}, busy: map[string]bool{}}
	s.nas.path, s.nas.loadErr = store.LoadNASRoot()
	if s.nas.path != "" {
		s.nas.id = core.NewID()
	}
	s.emit = func(event core.Event) {
		if event["type"] == "state" && event["phase"] != "connected" && s.tools != nil {
			if id, ok := event["sessionId"].(string); ok {
				s.cancelFrames(id)
				s.tools.StopSession(id)
			}
		}
		if transfer, ok := event["transfer"].(core.Transfer); ok {
			s.mu.Lock()
			if transfer.Status == "queued" || transfer.Status == "running" || transfer.Resumable {
				s.busy[transfer.ID] = true
			} else {
				delete(s.busy, transfer.ID)
			}
			s.mu.Unlock()
		}
		if !s.closed.Load() {
			output(event)
		}
	}
	s.ssh = core.NewSSHManager(store, s.emit, s.sendFrame)
	s.tools = core.NewTools(s.ssh, s.emit)
	s.files = core.NewFiles(s.ssh)
	s.transfers = core.NewTransfers(s.ssh, s.emit, ui.ConfirmOverwrite)
	s.drag = core.NewDragDownloads(cacheRoot, s.ssh, s.transfers)
	return s, Hooks{StartupSettings: store.Settings(), Drop: s.grantDroppedPaths, CloseAction: func() string { return s.store.Settings().WindowCloseAction }, Dirty: func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.dirty.Load() || len(s.busy) > 0 || s.ftpBusy.Load() > 0
	}, Close: func() {
		if s.closed.Swap(true) {
			return
		}
		s.cancelFrames("")
		s.disconnectFTP()
		s.tools.Close()
		s.transfers.Close()
		s.ssh.Close()
		_ = s.drag.Close()
	}}, nil
}
func (s *Service) grantFiles(files []string) []string {
	return s.grantLocalPaths(files, false)
}

func (s *Service) grantDroppedPaths(files []string) []string {
	return s.grantLocalPaths(files, true)
}

func (s *Service) grantLocalPaths(files []string, drop bool) []string {
	if s.dragging.Load() || s.closed.Load() {
		return []string{}
	}
	cacheRoot, err := resolveNASCachePath(s.cacheRoot)
	if err != nil {
		return []string{}
	}
	var grants []localGrant
	for _, file := range files {
		if len(grants) >= 100 {
			break
		}
		if !filepath.IsAbs(file) {
			continue
		}
		absolute, err := filepath.EvalSymlinks(file)
		if err != nil {
			continue
		}
		info, err := os.Lstat(absolute)
		if err != nil || (!info.Mode().IsRegular() && (!drop || info.Mode().Type() != os.ModeDir)) {
			continue
		}
		if nasPathContains(cacheRoot, absolute) || (info.IsDir() && nasPathContains(absolute, cacheRoot)) {
			continue
		}
		grant := localGrant{path: absolute}
		if drop {
			source, err := os.Lstat(file)
			if err != nil || source.Mode().Type() != info.Mode().Type() || !os.SameFile(source, info) {
				continue
			}
			grant.drop = info
		}
		grants = append(grants, grant)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dragging.Load() || s.closed.Load() {
		return []string{}
	}
	now := time.Now()
	for k, g := range s.grants {
		if !g.expires.After(now) {
			delete(s.grants, k)
		}
	}
	result := make([]string, 0, len(grants))
	for _, grant := range grants {
		token := core.NewID()
		grant.expires = now.Add(10 * time.Minute)
		s.grants[token] = grant
		result = append(result, token)
	}
	return result
}
func (s *Service) resolveFiles(tokens []string) ([]string, error) {
	if len(tokens) > 100 {
		return nil, errors.New("每次最多上传 100 个文件")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []string{}
	for _, token := range tokens {
		grant, ok := s.grants[token]
		if !ok || grant.drop != nil || !grant.expires.After(time.Now()) {
			return nil, errors.New("文件选择已过期，请重新选择或拖入")
		}
		result = append(result, grant.path)
	}
	return result, nil
}
func decode(args []json.RawMessage, values ...any) error {
	if len(args) != len(values) {
		return errors.New("参数数量无效")
	}
	for i, value := range values {
		if err := json.Unmarshal(args[i], value); err != nil {
			return fmt.Errorf("参数 %d 无效", i+1)
		}
	}
	return nil
}
func (s *Service) Call(operation string, args []json.RawMessage) (result Result) {
	if s.closed.Load() {
		return Result{Error: "应用正在退出"}
	}
	defer func() {
		if recover() != nil {
			result = Result{Error: "操作失败，请检查输入并重试"}
		}
	}()
	value, err := s.call(operation, args)
	if err != nil {
		var updateErr *updateCheckError
		if errors.As(err, &updateErr) {
			return Result{Error: updateErr.message, Code: updateErr.code}
		}
		message := err.Error()
		code := ""
		if strings.HasPrefix(message, "CONFLICT:") {
			message = strings.TrimPrefix(message, "CONFLICT:")
			code = "CONFLICT"
		}
		return Result{Error: message, Code: code}
	}
	return Result{OK: true, Value: value}
}
func (s *Service) call(op string, args []json.RawMessage) (any, error) {
	var id, a, b, c string
	var enabled bool
	switch op {
	case "ftp:connect", "ftp:disconnect", "ftp:list", "ftp:upload", "ftp:download":
		return s.callFTP(op, args)
	case "nas:select", "nas:list", "nas:upload", "nas:download":
		return s.callNAS(op, args)
	case "updates:check", "updates:open":
		return s.callUpdate(op, args)
	}
	switch op {
	case "bootstrap", "profile:save", "settings:save", "key:select", "commands:list", "commands:save", "forwards:list", "window:dirty":
	default:
		if len(args) > 0 {
			var value string
			if json.Unmarshal(args[0], &value) != nil || core.ValidateID(value) != nil {
				return nil, errors.New("无效的标识符")
			}
		}
	}
	switch op {
	case "terminal:osc7Check":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		return s.files.OSC7(id, "")
	case "terminal:osc7Configure":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		if a == "" {
			return nil, errors.New("请先检查 Shell 配置路径")
		}
		return s.files.OSC7(id, a)
	case "config:import", "config:export":
		return s.callConfig(op, args)
	case "window:editor":
		if err := decode(args, &id); err != nil || core.ValidateID(id) != nil || s.ui.OpenEditorWindow == nil {
			return nil, errors.New("无法打开独立编辑窗口")
		}
		s.ui.OpenEditorWindow(id)
		return nil, nil
	case "window:dirty":
		if err := decode(args, &enabled); err != nil {
			return nil, err
		}
		s.dirty.Store(enabled)
		return nil, nil
	case "terminal:ack":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		s.ackFrame(id, a)
		return nil, nil
	case "bootstrap":
		if err := decode(args); err != nil {
			return nil, err
		}
		if notice := s.store.MigrationNotice(); notice != "" {
			go s.emit(core.Event{"type": "notice", "message": notice})
		}
		return map[string]any{"profiles": s.store.List(), "settings": s.store.Settings(), "traySupported": s.ui.TraySupported}, nil
	case "profile:save":
		var p core.Profile
		var credentials core.Credentials
		if err := decode(args, &p, &credentials); err != nil {
			return nil, err
		}
		saved, err := s.store.SaveProfile(p, credentials)
		if err == nil {
			s.emit(core.Event{"type": "profiles", "profiles": s.store.List()})
		}
		return saved, err
	case "profile:delete":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		err := s.store.Delete(id)
		if err == nil {
			s.emit(core.Event{"type": "profiles", "profiles": s.store.List()})
		}
		return nil, err
	case "settings:save":
		var settings core.Settings
		if err := decode(args, &settings); err != nil {
			return nil, err
		}
		return nil, s.store.SaveSettings(settings)
	case "key:select":
		if err := decode(args); err != nil {
			return nil, err
		}
		file, err := s.ui.SelectKey()
		if file == "" {
			return nil, err
		}
		return file, err
	case "ssh:connect", "ssh:connectChain", "ssh:connectTmux":
		var input, jumpInput core.Credentials
		var hops map[string]core.Credentials
		var expectedIdentity *string
		fields := []any{&id, &a, &input}
		switch op {
		case "ssh:connect":
			fields = append(fields, &jumpInput)
		case "ssh:connectChain":
			fields = append(fields, &hops)
		case "ssh:connectTmux":
			fields = append(fields, &hops, &b)
		}
		if len(args) == len(fields)+1 {
			expectedIdentity = new(string)
			fields = append(fields, expectedIdentity)
		}
		if err := decode(args, fields...); err != nil {
			return nil, err
		}
		login, err := s.store.ResolveSSHLogin(a, input, jumpInput, hops, expectedIdentity)
		if err != nil {
			return nil, err
		}
		if op == "ssh:connectTmux" {
			return nil, s.ssh.ConnectTmux(id, login.Profile, login.Credentials, login.Jumps, b)
		}
		return nil, s.ssh.ConnectChain(id, login.Profile, login.Credentials, login.Jumps)
	case "ssh:answerChallenge":
		var answers []string
		if err := decode(args, &id, &a, &answers); err != nil {
			return nil, err
		}
		return nil, s.ssh.AnswerChallenge(id, a, answers)
	case "ssh:newTmuxTerminal":
		if err := decode(args, &id, &a, &b, &c); err != nil {
			return nil, err
		}
		p, err := s.store.Get(b)
		if err != nil {
			return nil, err
		}
		return nil, s.ssh.NewTmuxTerminal(id, a, p, c)
	case "ssh:newTerminal":
		var initial *string
		if err := decode(args, &id, &a, &b, &initial); err != nil {
			return nil, err
		}
		p, err := s.store.Get(b)
		if err != nil {
			return nil, err
		}
		return nil, s.ssh.NewTerminal(id, a, p, initial)
	case "ssh:disconnect":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		s.ssh.Disconnect(id)
		return nil, nil
	case "ssh:trust":
		if err := decode(args, &id, &enabled); err != nil {
			return nil, err
		}
		return nil, s.ssh.Trust(id, enabled)
	case "ssh:trustHostKey":
		if err := decode(args, &id, &a, &b, &enabled); err != nil {
			return nil, err
		}
		return nil, s.ssh.TrustHostKey(id, a, b, enabled)
	case "terminal:input":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		s.ssh.Input(id, a)
		return nil, nil
	case "terminal:resize":
		var cols, rows int
		if err := decode(args, &id, &cols, &rows); err != nil {
			return nil, err
		}
		s.ssh.Resize(id, cols, rows)
		return nil, nil
	case "gpu:status":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		return s.tools.GPUStatus(id)
	case "gpu:cancel":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		s.tools.CancelGPU(id)
		return nil, nil
	case "resources:status":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		return s.tools.ResourceStatus(id)
	case "resources:cancel":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		s.tools.CancelResource(id)
		return nil, nil
	case "commands:list":
		if err := decode(args); err != nil {
			return nil, err
		}
		return s.commands.List(), nil
	case "commands:save":
		var command core.CommandSnippet
		if err := decode(args, &command); err != nil {
			return nil, err
		}
		if command.ProfileID != "" {
			if _, err := s.store.Get(command.ProfileID); err != nil {
				return nil, err
			}
		}
		return s.commands.Save(command)
	case "commands:delete":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		return nil, s.commands.Delete(id)
	case "commands:render":
		var parameters map[string]string
		if err := decode(args, &id, &parameters); err != nil {
			return nil, err
		}
		return s.commands.Render(id, parameters)
	case "logs:start":
		var options core.LogOptions
		if err := decode(args, &id, &options); err != nil {
			return nil, err
		}
		return s.tools.StartLog(id, options)
	case "logs:stop":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		s.tools.StopLog(id)
		return nil, nil
	case "forwards:list":
		if err := decode(args); err != nil {
			return nil, err
		}
		return s.tools.ListForwards(), nil
	case "forwards:start":
		var config core.ForwardConfig
		if err := decode(args, &id, &config); err != nil {
			return nil, err
		}
		return s.tools.StartForward(id, config)
	case "forwards:stop":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		return nil, s.tools.StopForward(id)
	case "forwards:dismiss":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		return nil, s.tools.DismissForward(id)
	case "forwards:open":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		for _, forward := range s.tools.ListForwards() {
			if forward.ID == id && forward.Status == "listening" && forward.Mode == "local" {
				if _, err := s.ssh.Client(forward.SessionID); err != nil {
					return nil, err
				}
				return nil, s.ui.OpenURL(fmt.Sprintf("http://127.0.0.1:%d/", forward.LocalPort))
			}
		}
		return nil, errors.New("转发尚未就绪，请先启动转发")
	case "transfer:uploadDirectory", "transfer:downloadDirectory", "transfer:pause", "transfer:resume", "transfer:discardPartial", "files:deleteTree", "files:compareDirectory", "files:syncDirectory":
		return s.callFileFeature(op, args)
	case "files:list":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		return s.files.List(id, a)
	case "files:hardLinks":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		return s.files.HardLinks(id, a)
	case "files:preview":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		return s.files.Preview(id, a)
	case "files:previewPage":
		var offset int64
		if err := decode(args, &id, &a, &offset, &b); err != nil {
			return nil, err
		}
		return s.files.PreviewPage(id, a, offset, b)
	case "files:thumbnail":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		return s.files.Thumbnail(id, a)
	case "files:save":
		if err := decode(args, &id, &a, &b, &c); err != nil {
			return nil, err
		}
		return s.files.Save(id, a, b, c)
	case "files:create":
		if err := decode(args, &id, &a, &b, &enabled); err != nil {
			return nil, err
		}
		return nil, s.files.Create(id, a, b, enabled)
	case "files:rename":
		if err := decode(args, &id, &a, &b); err != nil {
			return nil, err
		}
		return nil, s.files.Rename(id, a, b)
	case "files:delete":
		if err := decode(args, &id, &a, &enabled); err != nil {
			return nil, err
		}
		return nil, s.files.Delete(id, a, enabled)
	case "transfer:upload":
		var tokens []string
		if err := decode(args, &id, &a, &tokens); err != nil {
			return nil, err
		}
		if _, err := s.ssh.SFTP(id); err != nil {
			return nil, err
		}
		if tokens == nil {
			paths, err := s.ui.SelectFiles()
			if err != nil {
				return nil, err
			}
			tokens = s.grantFiles(paths)
		}
		paths, err := s.resolveFiles(tokens)
		if err != nil {
			return nil, err
		}
		return nil, s.transfers.Upload(id, a, paths)
	case "transfer:dropUpload":
		var tokens []string
		if err := decode(args, &id, &a, &tokens); err != nil {
			return nil, err
		}
		return s.uploadDrop(id, a, tokens)
	case "transfer:download":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		if err := core.ValidateRemotePath(a); err != nil {
			return nil, err
		}
		if _, err := s.ssh.SFTP(id); err != nil {
			return nil, err
		}
		file, err := s.ui.SaveFile(path.Base(a))
		if err != nil || file == "" {
			return nil, err
		}
		_, err = s.transfers.Enqueue(id, "download", file, a, "")
		return nil, err
	case "transfer:cancel":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		s.transfers.Cancel(id)
		return nil, nil
	case "transfer:retry":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		return nil, s.transfers.Retry(id)
	case "drag:prepare":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		return s.drag.Prepare(id, a)
	case "drag:prepareDirectory":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		if runtime.GOOS != "windows" || s.ui.DragFile == nil {
			return nil, errors.New("当前平台请拖到 NAS 面板或使用下载文件夹菜单")
		}
		return s.drag.PrepareDirectory(id, a)
	case "drag:start":
		if err := decode(args, &id, &a); err != nil {
			return nil, err
		}
		defer s.emit(core.Event{"type": "drag-ended", "sessionId": id, "token": a})
		if s.ui.DragFile == nil {
			return nil, errors.New("当前平台未提供原生拖出")
		}
		if !s.dragging.CompareAndSwap(false, true) {
			return nil, errors.New("另一个拖拽正在进行")
		}
		defer s.dragging.Store(false)
		file, err := s.drag.File(id, a)
		if err != nil {
			return nil, err
		}
		return nil, s.ui.DragFile(file)
	default:
		return nil, errors.New("未知的桌面操作")
	}
}
