package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

const GPUCommand = "nvidia-smi --query-gpu=index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,power.limit,fan.speed,pstate --format=csv,noheader,nounits"
const ResourceCommand = `LC_ALL=C; export LC_ALL
if [ ! -r /proc/stat ] || [ ! -r /proc/meminfo ]; then printf 'Resource monitoring requires Linux /proc.\n' >&2; exit 1; fi
printf '[cpu]\n'; head -n 1 /proc/stat || exit 1
printf '[memory]\n'; cat /proc/meminfo || exit 1
printf '[disk]\n'; df -Pk / || exit 1
df -Pk -l -x tmpfs -x devtmpfs 2>/dev/null | sed -n '2,128p'
printf '[network]\n'; cat /proc/net/dev || exit 1
printf '[uptime]\n'; cat /proc/uptime || exit 1
printf '[load]\n'; cat /proc/loadavg || exit 1`

type toolQuery struct {
	identity string
	done     chan struct{}
	cancel   context.CancelFunc
	result   any
	err      error
}
type logWatch struct {
	id, sessionID, identity string
	ctx                     context.Context
	cancel                  context.CancelFunc
	channel                 *ssh.Session
	once                    sync.Once
	mu                      sync.Mutex
	pending                 strings.Builder
	lineLength              int
	skipping                bool
	bytes                   int
	rateStart               time.Time
	diagnostic              string
}
type forwardTunnel struct {
	value            PortForward
	sequence         uint64
	identity         string
	listener         net.Listener
	ctx              context.Context
	cancel           context.CancelFunc
	active           bool
	sockets          map[net.Conn]struct{}
	localConnections int
}
type Tools struct {
	mu              sync.Mutex
	connections     Connections
	emit            Emit
	queries         map[string]*toolQuery
	watches         map[string]*logWatch
	tunnels         map[string]*forwardTunnel
	forwardSequence uint64
	closed          bool
}

func NewTools(connections Connections, emit Emit) *Tools {
	if emit == nil {
		emit = func(Event) {}
	}
	return &Tools{connections: connections, emit: emit, queries: map[string]*toolQuery{}, watches: map[string]*logWatch{}, tunnels: map[string]*forwardTunnel{}}
}

// Query ownership includes connection identity, so a result from a previous
// connection can never overwrite a new connection's monitor cards.
func (t *Tools) query(id, kind string, run func(context.Context, *ssh.Client) (any, error)) (any, error) {
	identity, err := t.connections.Identity(id)
	if err != nil {
		return nil, err
	}
	client, err := t.connections.Client(id)
	if err != nil {
		return nil, err
	}
	key := kind + ":" + id
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("监控服务已停止")
	}
	query := t.queries[key]
	if query != nil && query.identity != identity {
		query.cancel()
		query = nil
	}
	if query == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		query = &toolQuery{done: make(chan struct{}), cancel: cancel, identity: identity}
		t.queries[key] = query
		go func(q *toolQuery) {
			q.result, q.err = run(ctx, client)
			if current, e := t.connections.Identity(id); e != nil || current != identity {
				q.result = nil
				q.err = errors.New("连接已变化，已丢弃旧的采集结果")
			}
			cancel()
			t.mu.Lock()
			if t.queries[key] == q {
				delete(t.queries, key)
			}
			t.mu.Unlock()
			close(q.done)
		}(query)
	}
	t.mu.Unlock()
	<-query.done
	return query.result, query.err
}

func (t *Tools) cancelQuery(id, kind string) {
	t.mu.Lock()
	query := t.queries[kind+":"+id]
	delete(t.queries, kind+":"+id)
	t.mu.Unlock()
	if query != nil {
		query.cancel()
	}
}
func (t *Tools) CancelGPU(id string)      { t.cancelQuery(id, "gpu") }
func (t *Tools) CancelResource(id string) { t.cancelQuery(id, "resource") }

type boundedOutput struct {
	mu             sync.Mutex
	stdout, stderr bytes.Buffer
	size, limit    int
	exceeded       bool
	closed         bool
	close          func()
}
type outputWriter struct {
	output *boundedOutput
	stderr bool
}

func (w outputWriter) Write(data []byte) (int, error) {
	w.output.mu.Lock()
	if w.output.closed {
		w.output.mu.Unlock()
		return 0, context.Canceled
	}
	if w.output.size+len(data) > w.output.limit {
		w.output.exceeded = true
		w.output.mu.Unlock()
		w.output.close()
		return 0, errors.New("远程输出超过大小限制")
	}
	w.output.size += len(data)
	if w.stderr {
		_, _ = w.output.stderr.Write(data)
	} else {
		_, _ = w.output.stdout.Write(data)
	}
	w.output.mu.Unlock()
	return len(data), nil
}

// A blackholed transport may never finish NewSession/Run or acknowledge a
// channel close. Bound these workers independently of an individual RPC. A
// worker retains its capacity until the actual SSH operation exits.
var monitorExecutions = make(chan struct{}, 32)

func execBounded(ctx context.Context, client *ssh.Client, command string, limit int) (string, string, error) {
	select {
	case monitorExecutions <- struct{}{}:
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	output := &boundedOutput{limit: limit}
	done := make(chan error, 1)
	go func() {
		defer func() { <-monitorExecutions }()
		channel, err := client.NewSession()
		if err != nil {
			done <- err
			return
		}
		var once sync.Once
		closeChannel := func() { once.Do(func() { go func() { _ = channel.Close() }() }) }
		defer closeChannel()
		if err = ctx.Err(); err != nil {
			done <- err
			return
		}
		output.mu.Lock()
		output.close = closeChannel
		output.mu.Unlock()
		stop := context.AfterFunc(ctx, closeChannel)
		defer stop()
		channel.Stdout = outputWriter{output: output}
		channel.Stderr = outputWriter{output: output, stderr: true}
		done <- channel.Run(command)
	}()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	output.closed = true
	if output.exceeded {
		err = errors.New("远程输出超过大小限制，已停止采集")
	}
	return output.stdout.String(), output.stderr.String(), err
}

func (t *Tools) GPUStatus(id string) (GpuSnapshot, error) {
	value, err := t.query(id, "gpu", func(ctx context.Context, client *ssh.Client) (any, error) {
		output, diagnostic, err := execBounded(ctx, client, GPUCommand, 256*1024)
		if err != nil {
			return nil, fmt.Errorf("显卡状态采集失败：%s%s", sshMessage(err), diagnosticSuffix(diagnostic))
		}
		devices, err := ParseGPU(output)
		if err != nil {
			return nil, err
		}
		return GpuSnapshot{Devices: devices, CollectedAt: time.Now().UnixMilli(), Warning: clipText(strings.TrimSpace(diagnostic), 4000)}, nil
	})
	if err != nil {
		return GpuSnapshot{}, err
	}
	return value.(GpuSnapshot), nil
}

func (t *Tools) ResourceStatus(id string) (ResourceSnapshot, error) {
	value, err := t.query(id, "resource", func(ctx context.Context, client *ssh.Client) (any, error) {
		output, diagnostic, err := execBounded(ctx, client, ResourceCommand, 512*1024)
		if err != nil {
			return nil, fmt.Errorf("资源采集失败，当前支持 Linux：%s%s", sshMessage(err), diagnosticSuffix(diagnostic))
		}
		return ParseResources(output)
	})
	if err != nil {
		return ResourceSnapshot{}, err
	}
	return value.(ResourceSnapshot), nil
}

func clipText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return s
}
func diagnosticSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return "\n" + clipText(s, 4000)
}

var gpuUUIDPattern = regexp.MustCompile(`^GPU-\S+$`)
var performancePattern = regexp.MustCompile(`^P\d+$`)
var metricPattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

const maxSafeNumber = 9007199254740991

func metric(value string, max float64) *float64 {
	if !metricPattern.MatchString(value) {
		return nil
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || n < 0 || n > max {
		return nil
	}
	return &n
}
func ParseGPU(output string) ([]GPUDevice, error) {
	devices := []GPUDevice{}
	output = strings.TrimSpace(output)
	if output == "" || strings.EqualFold(strings.TrimSuffix(output, "."), "No devices were found") {
		return devices, nil
	}
	reader := csv.NewReader(strings.NewReader(output))
	reader.FieldsPerRecord = 11
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, errors.New("显卡查询字段不完整或 CSV 损坏，无法生成卡片")
	}
	indexes := map[int]bool{}
	uuids := map[string]bool{}
	for _, fields := range rows {
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil || index < 0 || !gpuUUIDPattern.MatchString(fields[1]) || fields[2] == "" || indexes[index] || uuids[fields[1]] {
			return nil, errors.New("显卡查询返回无效或重复的 GPU 标识")
		}
		indexes[index] = true
		uuids[fields[1]] = true
		device := GPUDevice{Index: index, UUID: fields[1], Name: fields[2], Utilization: metric(fields[3], 100), MemoryUsedMiB: metric(fields[4], maxSafeNumber), MemoryTotalMiB: metric(fields[5], maxSafeNumber), TemperatureC: metric(fields[6], maxSafeNumber), PowerDrawW: metric(fields[7], maxSafeNumber), PowerLimitW: metric(fields[8], maxSafeNumber), FanPercent: metric(fields[9], 100)}
		if performancePattern.MatchString(fields[10]) {
			p := fields[10]
			device.PerformanceState = &p
		}
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Index < devices[j].Index })
	return devices, nil
}

func resourceUint(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n > maxSafeNumber {
		return 0, errors.New("资源数值无效或超过范围")
	}
	return n, nil
}
func resourceFloat(s string) (float64, error) {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 || n > maxSafeNumber || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, errors.New("资源数值无效")
	}
	return n, nil
}
func kbBytes(s string) (uint64, error) {
	n, err := resourceUint(s)
	if err != nil || n > maxSafeNumber/1024 {
		return 0, errors.New("资源容量超出范围")
	}
	return n * 1024, nil
}
func ParseResources(output string) (ResourceSnapshot, error) {
	result := ResourceSnapshot{CollectedAt: time.Now().UnixMilli(), Network: []ResourceNetwork{}}
	sections := map[string][]string{}
	name := ""
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if _, exists := sections[name]; exists {
				return result, errors.New("资源数据包含重复字段")
			}
			sections[name] = []string{}
		} else if line != "" && name != "" {
			sections[name] = append(sections[name], line)
		}
	}
	for _, required := range []string{"cpu", "memory", "disk", "network", "uptime", "load"} {
		if len(sections[required]) == 0 {
			return result, fmt.Errorf("资源数据缺少 %s", required)
		}
	}
	cpu := strings.Fields(sections["cpu"][0])
	if len(cpu) < 5 || cpu[0] != "cpu" {
		return result, errors.New("CPU 数据格式不正确")
	}
	for i, value := range cpu[1:] {
		if i >= 8 {
			break
		}
		n, err := resourceUint(value)
		if err != nil {
			return result, err
		}
		result.CPU.Total += n
		if i == 3 || i == 4 {
			result.CPU.Idle += n
		}
	}
	if result.CPU.Total == 0 || result.CPU.Total > maxSafeNumber {
		return result, errors.New("CPU 计数无效")
	}
	memory := map[string]uint64{}
	for _, row := range sections["memory"] {
		fields := strings.Fields(row)
		if len(fields) == 0 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if !strings.Contains("|MemTotal|MemAvailable|MemFree|Buffers|Cached|SReclaimable|Shmem|", "|"+key+"|") {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return result, errors.New("内存数据字段损坏")
		}
		if _, exists := memory[key]; exists {
			return result, errors.New("内存数据字段重复")
		}
		n, err := kbBytes(fields[1])
		if err != nil {
			return result, err
		}
		memory[key] = n
	}
	total := memory["MemTotal"]
	available, present := memory["MemAvailable"]
	if !present {
		for _, key := range []string{"MemFree", "Buffers", "Cached"} {
			if _, exists := memory[key]; !exists {
				return result, errors.New("内存可用容量字段缺失")
			}
		}
		available = memory["MemFree"] + memory["Buffers"] + memory["Cached"] + memory["SReclaimable"]
		if available < memory["Shmem"] {
			return result, errors.New("内存可用容量无效")
		}
		available -= memory["Shmem"]
	}
	if total == 0 || available > total {
		return result, errors.New("内存容量数据无效")
	}
	result.Memory = ResourceMemory{Total: total, Available: available}
	result.Disk, result.Disks = parseResourceDisks(sections["disk"])
	if result.Disk.Total == 0 {
		return result, errors.New("根分区容量数据无效")
	}
	seen := map[string]bool{}
	for _, row := range sections["network"] {
		name, values, found := strings.Cut(row, ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(values)
		if name == "" || seen[name] || len(fields) < 16 {
			return result, errors.New("网络接口数据无效")
		}
		seen[name] = true
		received, err := resourceUint(fields[0])
		if err != nil {
			return result, err
		}
		sent, err := resourceUint(fields[8])
		if err != nil {
			return result, err
		}
		if name != "lo" {
			result.Network = append(result.Network, ResourceNetwork{Name: name, Received: received, Sent: sent})
		}
	}
	var err error
	result.Uptime, err = resourceFloat(strings.Fields(sections["uptime"][0])[0])
	if err != nil {
		return result, err
	}
	load := strings.Fields(sections["load"][0])
	if len(load) < 3 {
		return result, errors.New("系统负载字段不完整")
	}
	for _, field := range load[:3] {
		n, e := resourceFloat(field)
		if e != nil {
			return result, e
		}
		result.Load = append(result.Load, n)
	}
	return result, nil
}

func LogCommand(options LogOptions) (string, error) {
	if err := ValidateRemotePath(options.Path); err != nil {
		return "", err
	}
	if !strings.HasPrefix(options.Path, "/") {
		return "", errors.New("请输入日志文件的绝对路径")
	}
	if options.Lines < 0 || options.Lines > 2000 {
		return "", errors.New("初始日志行数必须是 0–2000 的整数")
	}
	quoted := ShellQuote(options.Path)
	return fmt.Sprintf("if [ ! -f %s ] || [ ! -r %s ]; then printf '%%s\\n' '日志文件不存在或不可读' >&2; exit 1; fi; exec tail -n %d -F -- %s", quoted, quoted, options.Lines, quoted), nil
}

func (t *Tools) StartLog(id string, options LogOptions) (string, error) {
	command, err := LogCommand(options)
	if err != nil {
		return "", err
	}
	identity, err := t.connections.Identity(id)
	if err != nil {
		return "", err
	}
	client, err := t.connections.Client(id)
	if err != nil {
		return "", err
	}
	t.StopLog(id)
	ctx, cancel := context.WithCancel(context.Background())
	w := &logWatch{id: NewID(), sessionID: id, identity: identity, ctx: ctx, cancel: cancel, rateStart: time.Now()}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		cancel()
		return "", errors.New("日志服务已停止")
	}
	t.watches[id] = w
	t.mu.Unlock()
	startCtx, startCancel := context.WithTimeout(ctx, 10*time.Second)
	defer startCancel()
	channel, err := openSSHSession(startCtx, client)
	if err != nil {
		t.finishLog(w, "error", sshMessage(err))
		return "", err
	}
	w.mu.Lock()
	w.channel = channel
	w.mu.Unlock()
	stdout, err := channel.StdoutPipe()
	if err != nil {
		t.finishLog(w, "error", err.Error())
		return "", err
	}
	stderr, err := channel.StderrPipe()
	if err != nil {
		t.finishLog(w, "error", err.Error())
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- channel.Start(command) }()
	select {
	case err = <-done:
	case <-startCtx.Done():
		err = startCtx.Err()
		_ = channel.Close()
	}
	if err != nil {
		t.finishLog(w, "error", sshMessage(err))
		return "", err
	}
	t.mu.Lock()
	current := t.watches[id] == w && !t.closed
	t.mu.Unlock()
	if !current || ctx.Err() != nil {
		_ = channel.Close()
		return "", errors.New("日志追踪启动已取消")
	}
	var readers sync.WaitGroup
	for i, reader := range []io.Reader{stdout, stderr} {
		readers.Add(1)
		go func(reader io.Reader, isError bool) { defer readers.Done(); t.readLog(w, reader, isError) }(reader, i == 1)
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if identity, e := t.connections.Identity(id); e != nil || identity != w.identity {
					t.finishLog(w, "end", "连接已变化，日志追踪已停止")
					return
				}
				t.flushLog(w)
			}
		}
	}()
	go func() {
		err := channel.Wait()
		readers.Wait()
		if err == nil {
			t.finishLog(w, "end", "日志追踪已结束")
		} else {
			w.mu.Lock()
			diagnostic := w.diagnostic
			w.mu.Unlock()
			t.finishLog(w, "error", "日志命令执行失败："+sshMessage(err)+diagnosticSuffix(diagnostic))
		}
	}()
	return w.id, nil
}

func (t *Tools) appendLog(w *logWatch, r rune, isError bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil {
		return context.Canceled
	}
	if time.Since(w.rateStart) >= time.Second {
		w.rateStart = time.Now()
		w.bytes = 0
	}
	w.bytes += utf8.RuneLen(r)
	if w.bytes > 512*1024 {
		return errors.New("日志每秒输出超过 512 KiB，已停止追踪；请过滤服务器日志后重试")
	}
	if isError {
		w.diagnostic += string(r)
		if len(w.diagnostic) > 8000 {
			w.diagnostic = clipTail(w.diagnostic, 4000)
		}
	}
	if r == '\n' || r == '\r' {
		w.pending.WriteRune(r)
		w.lineLength = 0
		w.skipping = false
	} else if !w.skipping {
		w.lineLength++
		if w.lineLength > 16*1024 {
			w.pending.WriteString("… [单行超过 16K 字符，已截断]\n")
			w.skipping = true
		} else {
			if r == 0 {
				r = utf8.RuneError
			}
			w.pending.WriteRune(r)
		}
	}
	if w.pending.Len() > 64*1024 {
		return errors.New("日志输出过快，已停止追踪；可减少初始行数后重试")
	}
	return nil
}

func clipTail(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[len(runes)-max:])
	}
	return s
}
func (t *Tools) readLog(w *logWatch, reader io.Reader, isError bool) {
	buffer := bufio.NewReaderSize(reader, 16*1024)
	for {
		r, _, err := buffer.ReadRune()
		if err != nil {
			return
		}
		if err = t.appendLog(w, r, isError); err != nil {
			if !errors.Is(err, context.Canceled) {
				t.finishLog(w, "error", err.Error())
			}
			return
		}
	}
}

func (t *Tools) flushLog(w *logWatch) {
	w.mu.Lock()
	data := w.pending.String()
	w.pending.Reset()
	w.mu.Unlock()
	if data != "" {
		t.emit(Event{"type": "log", "sessionId": w.sessionID, "watchId": w.id, "kind": "data", "data": data})
	}
}

func (t *Tools) finishLog(w *logWatch, kind, message string) {
	w.once.Do(func() {
		t.flushLog(w)
		w.cancel()
		t.mu.Lock()
		if t.watches[w.sessionID] == w {
			delete(t.watches, w.sessionID)
		}
		t.mu.Unlock()
		w.mu.Lock()
		channel := w.channel
		w.mu.Unlock()
		if channel != nil {
			go func() { _ = channel.Signal(ssh.SIGTERM); _ = channel.Close() }()
		}
		t.emit(Event{"type": "log", "sessionId": w.sessionID, "watchId": w.id, "kind": kind, "data": message})
	})
}

func (t *Tools) StopLog(id string) {
	t.mu.Lock()
	w := t.watches[id]
	t.mu.Unlock()
	if w != nil {
		t.finishLog(w, "end", "日志追踪已停止")
	}
}

func ValidateForward(config ForwardConfig) (ForwardConfig, error) {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" || validateText(config.Name, "转发名称", 100) != nil || strings.ContainsAny(config.Name, "\r\n") {
		return config, errors.New("请填写有效的转发名称")
	}
	if config.LocalPort < 0 || config.LocalPort > 65535 || config.RemotePort < 0 || config.RemotePort > 65535 {
		return config, errors.New("端口必须是 0–65535 的整数")
	}
	switch config.Mode {
	case "", "local":
		config.Mode = "local"
		config.LocalHost = "127.0.0.1"
		config.RemoteHost = strings.TrimSpace(config.RemoteHost)
		if config.RemotePort == 0 || !validTCPHost(config.RemoteHost) {
			return config, errors.New("请填写有效的远程目标主机和端口")
		}
	case "remote":
		config.RemoteHost = "127.0.0.1"
		config.LocalHost = strings.TrimSpace(config.LocalHost)
		if config.LocalHost == "" {
			config.LocalHost = "127.0.0.1"
		}
		if config.LocalPort == 0 || !validTCPHost(config.LocalHost) {
			return config, errors.New("请填写有效的本地目标主机和端口")
		}
	case "remote-http":
		config.LocalHost, config.RemoteHost = "127.0.0.1", "127.0.0.1"
		if config.LocalPort == 0 {
			return config, errors.New("请填写已运行的本机 HTTP 代理端口（1–65535）")
		}
	case "dynamic":
		config.LocalHost = "127.0.0.1"
		config.RemoteHost, config.RemotePort = "", 0
	case "remote-dynamic":
		config.RemoteHost = "127.0.0.1"
		config.LocalHost, config.LocalPort = "", 0
	default:
		return config, errors.New("无效的端口转发类型")
	}
	return config, nil
}

func (t *Tools) ListForwards() []PortForward {
	t.mu.Lock()
	defer t.mu.Unlock()
	ordered := make([]*forwardTunnel, 0, len(t.tunnels))
	for _, tunnel := range t.tunnels {
		if tunnel.active && tunnel.listener == nil {
			continue
		}
		ordered = append(ordered, tunnel)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].sequence > ordered[j].sequence })
	values := make([]PortForward, 0, len(ordered))
	for _, tunnel := range ordered {
		values = append(values, tunnel.value)
	}
	return values
}
func (t *Tools) publishForwards() { t.emit(Event{"type": "forwards", "forwards": t.ListForwards()}) }
func (t *Tools) StartForward(id string, config ForwardConfig) (PortForward, error) {
	config, err := ValidateForward(config)
	if err != nil {
		return PortForward{}, err
	}
	identity, err := t.connections.Identity(id)
	if err != nil {
		return PortForward{}, err
	}
	client, err := t.connections.Client(id)
	if err != nil {
		return PortForward{}, err
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return PortForward{}, errors.New("端口转发服务已停止")
	}
	active := 0
	for _, tunnel := range t.tunnels {
		if tunnel.active {
			active++
		}
	}
	if active >= 20 {
		t.mu.Unlock()
		return PortForward{}, errors.New("最多同时运行 20 个端口转发")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.forwardSequence++
	tunnel := &forwardTunnel{
		value:    PortForward{ForwardConfig: config, ID: NewID(), SessionID: id, Status: "listening"},
		sequence: t.forwardSequence,
		identity: identity,
		ctx:      ctx,
		cancel:   cancel,
		active:   true,
		sockets:  map[net.Conn]struct{}{},
	}
	t.tunnels[tunnel.value.ID] = tunnel
	t.mu.Unlock()
	listener, err := listenForward(ctx, client, config)
	if err != nil {
		t.closeTunnel(tunnel, "failed", err.Error())
		return PortForward{}, err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port < 1 || address.Port > 65535 {
		_ = listener.Close()
		message := "未获得有效的监听端口"
		t.closeTunnel(tunnel, "failed", message)
		return PortForward{}, errors.New(message)
	}
	if current, e := t.connections.Identity(id); e != nil || current != identity {
		_ = listener.Close()
		t.closeTunnel(tunnel, "stopped", "连接已变化")
		return PortForward{}, errors.New("原连接已结束，端口转发已取消")
	}
	t.mu.Lock()
	if !tunnel.active || t.closed {
		t.mu.Unlock()
		_ = listener.Close()
		return PortForward{}, errors.New("端口转发启动已取消")
	}
	tunnel.listener = listener
	if listensOnRemote(config.Mode) {
		tunnel.value.RemotePort = address.Port
	} else {
		tunnel.value.LocalPort = address.Port
	}
	if config.Mode == "remote-dynamic" {
		tunnel.value.SocksPassword = rand.Text()
	}
	value := tunnel.value
	t.mu.Unlock()
	t.publishForwards()
	go t.acceptForward(tunnel)
	return value, nil
}

func (t *Tools) acceptForward(tunnel *forwardTunnel) {
	for {
		socket, err := tunnel.listener.Accept()
		if err != nil {
			if tunnel.ctx.Err() == nil {
				t.closeTunnel(tunnel, "failed", err.Error())
			}
			return
		}
		t.mu.Lock()
		if !tunnel.active || tunnel.localConnections >= 32 {
			t.mu.Unlock()
			_ = socket.Close()
			continue
		}
		tunnel.sockets[socket] = struct{}{}
		tunnel.localConnections++
		forward := tunnel.value
		t.mu.Unlock()
		go t.forwardConnection(tunnel, socket, forward)
	}
}

func (t *Tools) forwardConnection(tunnel *forwardTunnel, local net.Conn, forward PortForward) {
	var remote net.Conn
	defer func() {
		_ = local.Close()
		if remote != nil {
			_ = remote.Close()
		}
		t.mu.Lock()
		delete(tunnel.sockets, local)
		tunnel.localConnections--
		if remote != nil {
			delete(tunnel.sockets, remote)
		}
		t.mu.Unlock()
	}()
	identity, err := t.connections.Identity(forward.SessionID)
	if err != nil || identity != tunnel.identity {
		t.closeTunnel(tunnel, "failed", "原 SSH 会话已变化")
		return
	}
	if forward.Mode == "remote-dynamic" {
		remote, err = connectReverseSOCKS(tunnel.ctx, local, forward.SocksPassword)
		if err != nil {
			return
		}
	} else {
		client, err := t.connections.Client(forward.SessionID)
		if err != nil {
			t.closeTunnel(tunnel, "failed", err.Error())
			return
		}
		destination := net.JoinHostPort(forward.RemoteHost, strconv.Itoa(forward.RemotePort))
		if forward.Mode == "dynamic" {
			_ = local.SetDeadline(time.Now().Add(10 * time.Second))
			destination, err = acceptSOCKSConnect(local)
			if err != nil {
				return
			}
		}
		ctx, cancel := context.WithTimeout(tunnel.ctx, 10*time.Second)
		if listensOnRemote(forward.Mode) {
			destination = net.JoinHostPort(forward.LocalHost, strconv.Itoa(forward.LocalPort))
			remote, err = (&net.Dialer{}).DialContext(ctx, "tcp", destination)
		} else {
			remote, err = client.DialContext(ctx, "tcp", destination)
		}
		cancel()
		if forward.Mode == "dynamic" {
			status := byte(0)
			if err != nil {
				status = 5
			}
			_ = local.SetDeadline(time.Now().Add(3 * time.Second))
			if replyErr := writeSOCKSReply(local, status); replyErr != nil {
				return
			}
			_ = local.SetDeadline(time.Time{})
		}
		if err != nil {
			message := "无法访问远程服务：" + sshMessage(err)
			if forward.Mode == "remote-http" {
				message = "无法访问本机 HTTP 代理 " + destination + "。请确认代理已启动并检查端口。（" + sshMessage(err) + "）"
			} else if listensOnRemote(forward.Mode) {
				message = "无法访问本地服务：" + sshMessage(err)
			}
			t.mu.Lock()
			if tunnel.active {
				tunnel.value.Error = message
			}
			t.mu.Unlock()
			t.publishForwards()
			return
		}
	}
	t.mu.Lock()
	if !tunnel.active {
		t.mu.Unlock()
		return
	}
	tunnel.sockets[remote] = struct{}{}
	tunnel.value.Error = ""
	t.mu.Unlock()
	// io.Copy's fixed-size buffer and the SSH channel window apply backpressure.
	// Half-close each direction independently so a slow HTTP response is drained
	// after the requesting client has sent FIN.
	done := make(chan error, 2)
	go func() {
		_, e := io.Copy(remote, local)
		if closer, ok := remote.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		done <- e
	}()
	go func() {
		_, e := io.Copy(local, remote)
		if closer, ok := local.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		done <- e
	}()
	for i := 0; i < 2; i++ {
		select {
		case e := <-done:
			if e != nil {
				_ = local.Close()
				_ = remote.Close()
			}
		case <-tunnel.ctx.Done():
			return
		}
	}
}

func (t *Tools) closeTunnel(tunnel *forwardTunnel, status, message string) {
	t.mu.Lock()
	if !tunnel.active {
		t.mu.Unlock()
		return
	}
	tunnel.active = false
	tunnel.value.Status = status
	tunnel.value.SocksPassword = ""
	if message != "" {
		tunnel.value.Error = message
	}
	listener := tunnel.listener
	sockets := make([]net.Conn, 0, len(tunnel.sockets))
	for socket := range tunnel.sockets {
		sockets = append(sockets, socket)
	}
	tunnel.sockets = map[net.Conn]struct{}{}
	t.mu.Unlock()
	tunnel.cancel()
	if listener != nil {
		_ = listener.Close()
	}
	for _, socket := range sockets {
		_ = socket.Close()
	}
	t.publishForwards()
}
func (t *Tools) StopForward(id string) error {
	t.mu.Lock()
	tunnel := t.tunnels[id]
	t.mu.Unlock()
	if tunnel != nil {
		t.closeTunnel(tunnel, "stopped", "")
	}
	return nil
}
func (t *Tools) DismissForward(id string) error {
	t.mu.Lock()
	tunnel := t.tunnels[id]
	if tunnel == nil {
		t.mu.Unlock()
		return nil
	}
	if tunnel.active {
		t.mu.Unlock()
		return errors.New("请先停止端口转发，再移除记录")
	}
	delete(t.tunnels, id)
	t.mu.Unlock()
	t.publishForwards()
	return nil
}
func (t *Tools) StopSession(id string) {
	t.StopLog(id)
	t.CancelGPU(id)
	t.CancelResource(id)
	t.mu.Lock()
	var tunnels []*forwardTunnel
	for _, tunnel := range t.tunnels {
		if tunnel.value.SessionID == id && tunnel.active {
			tunnels = append(tunnels, tunnel)
		}
	}
	t.mu.Unlock()
	for _, tunnel := range tunnels {
		t.closeTunnel(tunnel, "stopped", "")
	}
}
func (t *Tools) Close() {
	t.mu.Lock()
	t.closed = true
	var watches []*logWatch
	for _, w := range t.watches {
		watches = append(watches, w)
	}
	var tunnels []*forwardTunnel
	for _, tunnel := range t.tunnels {
		tunnels = append(tunnels, tunnel)
	}
	var queries []*toolQuery
	for _, q := range t.queries {
		queries = append(queries, q)
	}
	t.queries = map[string]*toolQuery{}
	t.mu.Unlock()
	for _, w := range watches {
		t.finishLog(w, "end", "应用已关闭")
	}
	for _, q := range queries {
		q.cancel()
	}
	for _, tunnel := range tunnels {
		t.closeTunnel(tunnel, "stopped", "")
	}
}
