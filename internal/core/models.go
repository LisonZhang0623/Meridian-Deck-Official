package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Event map[string]any
type Emit func(Event)
type Profile struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Group               string   `json:"group"`
	Host                string   `json:"host"`
	Port                int      `json:"port"`
	Username            string   `json:"username"`
	AuthType            string   `json:"authType"`
	KeyPath             string   `json:"keyPath"`
	InitialPath         string   `json:"initialPath"`
	Timeout             int      `json:"timeout"`
	Keepalive           bool     `json:"keepalive"`
	RememberCredentials bool     `json:"rememberCredentials"`
	HasCredentials      bool     `json:"hasCredentials,omitempty"`
	Tags                []string `json:"tags"`
	LastConnectedAt     int64    `json:"lastConnectedAt,omitempty"`
	JumpProfileID       string   `json:"jumpProfileId,omitempty"`
	JumpIdentity        string   `json:"jumpIdentity,omitempty"`
	ProxyAddress        string   `json:"proxyAddress,omitempty"`
	KeyboardInteractive bool     `json:"keyboardInteractive,omitempty"`
}
type Credentials struct {
	Password   *string `json:"password,omitempty"`
	Passphrase *string `json:"passphrase,omitempty"`
}
type Jump struct {
	Profile     Profile
	Credentials Credentials
}
type Settings struct {
	SidebarSide           string `json:"sidebarSide"`
	SidebarCollapsed      bool   `json:"sidebarCollapsed"`
	SidebarPanel          string `json:"sidebarPanel"`
	SidebarWidth          int    `json:"sidebarWidth"`
	Theme                 string `json:"theme"`
	TerminalFontSize      int    `json:"terminalFontSize"`
	EditorFontSize        int    `json:"editorFontSize"`
	UIZoom                int    `json:"uiZoom"`
	GPURefreshSeconds     int    `json:"gpuRefreshSeconds"`
	StartupAnimation      string `json:"startupAnimation"`
	WindowCloseAction     string `json:"windowCloseAction"`
	CheckUpdatesOnStartup bool   `json:"checkUpdatesOnStartup"`
}

const (
	DefaultStartupAnimation = "echo"
	CloseAsk                = "ask"
	CloseTray               = "tray"
	CloseQuit               = "quit"
	defaultEditorFontSize   = 13
	defaultUIZoom           = 100
)

func DefaultSettings() Settings {
	return Settings{SidebarSide: "right", SidebarCollapsed: true, SidebarPanel: "servers", Theme: "system", TerminalFontSize: 13, EditorFontSize: defaultEditorFontSize, UIZoom: defaultUIZoom, GPURefreshSeconds: 2, StartupAnimation: DefaultStartupAnimation, WindowCloseAction: CloseAsk}
}

type RemoteEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	Modified   int64  `json:"modified"`
	Mode       uint32 `json:"mode"`
	LinkTarget string `json:"linkTarget,omitempty"`
	TargetType string `json:"targetType,omitempty"`
}
type Directory struct {
	Path    string        `json:"path"`
	Entries []RemoteEntry `json:"entries"`
}
type Preview struct {
	Path     string       `json:"path"`
	Kind     string       `json:"kind"`
	Content  string       `json:"content"`
	Mime     string       `json:"mime,omitempty"`
	Encoding string       `json:"encoding,omitempty"`
	Page     *PreviewPage `json:"page,omitempty"`
	Version  string       `json:"version"`
	Size     int64        `json:"size"`
	Modified int64        `json:"modified"`
}
type PreviewPage struct {
	Offset     int64 `json:"offset"`
	NextOffset int64 `json:"nextOffset,omitempty"`
}
type Transfer struct {
	ID          string  `json:"id"`
	SessionID   string  `json:"sessionId"`
	Name        string  `json:"name"`
	Direction   string  `json:"direction"`
	RemotePath  string  `json:"remotePath"`
	Purpose     string  `json:"purpose,omitempty"`
	DragRoot    string  `json:"dragRoot,omitempty"`
	Size        int64   `json:"size"`
	Transferred int64   `json:"transferred"`
	Speed       float64 `json:"speed"`
	Status      string  `json:"status"`
	Error       string  `json:"error,omitempty"`
	Resumable   bool    `json:"resumable,omitempty"`
}
type DragDownload struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	ExpiresAt int64  `json:"expiresAt"`
}
type GPUDevice struct {
	Index            int      `json:"index"`
	UUID             string   `json:"uuid"`
	Name             string   `json:"name"`
	Utilization      *float64 `json:"utilization"`
	MemoryUsedMiB    *float64 `json:"memoryUsedMiB"`
	MemoryTotalMiB   *float64 `json:"memoryTotalMiB"`
	TemperatureC     *float64 `json:"temperatureC"`
	PowerDrawW       *float64 `json:"powerDrawW"`
	PowerLimitW      *float64 `json:"powerLimitW"`
	FanPercent       *float64 `json:"fanPercent"`
	PerformanceState *string  `json:"performanceState"`
}
type GpuDevice = GPUDevice
type GpuSnapshot struct {
	Devices     []GPUDevice `json:"devices"`
	CollectedAt int64       `json:"collectedAt"`
	Warning     string      `json:"warning,omitempty"`
}
type ResourceCPU struct {
	Total uint64 `json:"total"`
	Idle  uint64 `json:"idle"`
}
type ResourceMemory struct {
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"`
}
type ResourceDisk struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	Available uint64 `json:"available"`
	Mount     string `json:"mount"`
}
type ResourceNetwork struct {
	Name     string `json:"name"`
	Received uint64 `json:"received"`
	Sent     uint64 `json:"sent"`
}
type ResourceSnapshot struct {
	CollectedAt int64             `json:"collectedAt"`
	CPU         ResourceCPU       `json:"cpu"`
	Memory      ResourceMemory    `json:"memory"`
	Disk        ResourceDisk      `json:"disk"`
	Disks       []ResourceDisk    `json:"disks,omitempty"`
	Network     []ResourceNetwork `json:"network"`
	Uptime      float64           `json:"uptime"`
	Load        []float64         `json:"load"`
}
type LogOptions struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
}
type ForwardConfig struct {
	Name       string `json:"name"`
	Mode       string `json:"mode,omitempty"`
	LocalHost  string `json:"localHost,omitempty"`
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
}
type PortForward struct {
	ForwardConfig
	ID            string `json:"id"`
	SessionID     string `json:"sessionId"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	SocksPassword string `json:"socksPassword,omitempty"`
}
type CommandSnippet struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Group       string `json:"group"`
	ProfileID   string `json:"profileId"`
	Command     string `json:"command"`
	Description string `json:"description"`
}
type Connections interface {
	Client(string) (*ssh.Client, error)
	SFTP(string) (*sftp.Client, error)
	Identity(string) (string, error)
}
type TrustStore interface {
	Fingerprint(string) string
	Trust(string, string) error
	RecordConnected(string, string) error
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	v := hex.EncodeToString(b[:])
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
}
func profileIdentityHost(host string) string {
	// Keep distinct Unicode host spellings separate from one another.
	return strings.Map(func(ch rune) rune {
		if ch >= 'A' && ch <= 'Z' {
			return ch + ('a' - 'A')
		}
		return ch
	}, host)
}

func ProfileIdentity(p Profile) string {
	key := ""
	if p.AuthType == "key" {
		key = p.KeyPath
	}
	parts := []any{p.ID, profileIdentityHost(p.Host), p.Port, p.Username, p.AuthType, key}
	if p.ProxyAddress != "" || p.KeyboardInteractive {
		parts = append(parts, p.ProxyAddress, p.KeyboardInteractive)
	}
	if p.JumpProfileID != "" {
		parts = append(parts, p.JumpProfileID, p.JumpIdentity)
	}
	b, _ := json.Marshal(parts)
	return string(b)
}
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
