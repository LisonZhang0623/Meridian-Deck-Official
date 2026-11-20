package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

const maxCommands = 500

var parameterPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)
var templatePattern = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]{0,31})\}\}`)
var unsupportedExpansion = regexp.MustCompile("`|\\$[({]|<<")

// Parameters are restricted to complete unquoted shell arguments. This parser
// intentionally rejects ambiguous contexts instead of pretending to parse shell.
func CommandParameters(command string) ([]string, error) {
	names := make([]string, 0)
	seen := map[string]bool{}
	quote := rune(0)
	escaped, comment := false, false
	chars := []rune(command)
	if strings.Contains(command, "{{") && unsupportedExpansion.MatchString(command) {
		return nil, errors.New("含参数的命令不支持命令替换、变量展开、反引号或 here-document")
	}
	for i := 0; i < len(chars); i++ {
		ch := chars[i]
		if ch == '{' && i+1 < len(chars) && chars[i+1] == '{' {
			end := i + 2
			for end+1 < len(chars) && !(chars[end] == '}' && chars[end+1] == '}') {
				end++
			}
			if end+1 >= len(chars) || !parameterPattern.MatchString(string(chars[i+2:end])) {
				return nil, errors.New("参数格式应为 {{name}}，名称使用字母、数字和下划线，并以字母或下划线开头")
			}
			if quote != 0 || escaped || comment || (i > 0 && !unicode.IsSpace(chars[i-1])) || (end+2 < len(chars) && !unicode.IsSpace(chars[end+2])) {
				return nil, errors.New("参数必须是独立、未加引号的参数，例如 tail -n {{lines}} -- {{path}}")
			}
			name := string(chars[i+2 : end])
			if !seen[name] {
				names = append(names, name)
				seen[name] = true
			}
			if len(names) > 24 {
				return nil, errors.New("每条命令最多使用 24 个参数")
			}
			i = end + 1
			continue
		}
		if comment {
			if ch == '\n' {
				comment = false
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
		} else if ch == '#' && (i == 0 || unicode.IsSpace(chars[i-1])) {
			comment = true
		}
	}
	return names, nil
}

func validateCommand(input CommandSnippet) (CommandSnippet, error) {
	c := input
	c.Name, c.Group, c.Description = strings.TrimSpace(c.Name), strings.TrimSpace(c.Group), strings.TrimSpace(c.Description)
	c.Command = strings.ReplaceAll(c.Command, "\r\n", "\n")
	for _, field := range []struct {
		value, name string
		max         int
	}{{c.Name, "命令名称", 80}, {c.Group, "命令分组", 80}, {c.Description, "命令说明", 500}, {c.Command, "命令内容", 16384}} {
		if err := validateText(field.value, field.name, field.max); err != nil {
			return CommandSnippet{}, err
		}
	}
	if c.Name == "" || strings.TrimSpace(c.Command) == "" {
		return CommandSnippet{}, errors.New("请填写命令名称和内容")
	}
	for _, r := range c.Command {
		if (r < 32 && r != '\n' && r != '\t') || r == 127 {
			return CommandSnippet{}, errors.New("命令内容不能包含终端控制字符")
		}
	}
	if _, err := CommandParameters(c.Command); err != nil {
		return CommandSnippet{}, err
	}
	if c.ID == "" {
		c.ID = NewID()
	}
	if err := ValidateID(c.ID); err != nil {
		return CommandSnippet{}, err
	}
	if c.ProfileID != "" {
		if err := ValidateID(c.ProfileID); err != nil {
			return CommandSnippet{}, err
		}
	}
	return c, nil
}

func RenderCommand(command string, parameters map[string]string) (string, error) {
	names, err := CommandParameters(command)
	if err != nil {
		return "", err
	}
	if len(parameters) > 24 {
		return "", errors.New("命令参数格式不正确")
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		value, ok := parameters[name]
		if !ok {
			return "", fmt.Errorf("请填写参数：%s", name)
		}
		if err := validateText(value, "参数 "+name, 4096); err != nil {
			return "", err
		}
		for _, r := range value {
			if r < 32 || r == 127 {
				return "", fmt.Errorf("参数 %s 不能包含换行或控制字符", name)
			}
		}
		values[name] = ShellQuote(value)
	}
	for name := range parameters {
		if _, ok := values[name]; !ok {
			return "", fmt.Errorf("未知参数：%s", name)
		}
	}
	output := templatePattern.ReplaceAllStringFunc(command, func(match string) string { return values[match[2:len(match)-2]] })
	if err := validateText(output, "展开后的命令", 65536); err != nil {
		return "", err
	}
	return output, nil
}

type commandData struct {
	Version  int              `json:"version"`
	Commands []CommandSnippet `json:"commands"`
}
type CommandsStore struct {
	mu        sync.RWMutex
	directory string
	commands  []CommandSnippet
}

func parseCommands(raw []byte) ([]CommandSnippet, error) {
	var data commandData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, errors.New("无效的命令收藏格式")
	}
	if data.Version != 1 || data.Commands == nil || len(data.Commands) > maxCommands {
		return nil, errors.New("无效的命令收藏格式")
	}
	seen := map[string]bool{}
	for i, c := range data.Commands {
		if c.ID == "" {
			return nil, errors.New("命令标识符缺失")
		}
		valid, err := validateCommand(c)
		if err != nil {
			return nil, err
		}
		if seen[valid.ID] {
			return nil, errors.New("重复的命令标识符")
		}
		seen[valid.ID] = true
		data.Commands[i] = valid
	}
	return data.Commands, nil
}

func NewCommandsStore(directory string) (*CommandsStore, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	s := &CommandsStore{directory: directory, commands: make([]CommandSnippet, 0)}
	raw, err := readConfig(filepath.Join(directory, "commands.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 commands.json 失败，原文件未被覆盖：%w", err)
	}
	s.commands, err = parseCommands(raw)
	if err != nil {
		return nil, fmt.Errorf("读取 commands.json 失败，原文件未被覆盖：%w", err)
	}
	return s, nil
}
func (s *CommandsStore) List() []CommandSnippet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]CommandSnippet{}, s.commands...)
}
func (s *CommandsStore) Save(input CommandSnippet) (CommandSnippet, error) {
	c, err := validateCommand(input)
	if err != nil {
		return CommandSnippet{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := append([]CommandSnippet{}, s.commands...)
	index := -1
	for i, item := range next {
		if item.ID == c.ID {
			index = i
			break
		}
	}
	if index < 0 {
		if len(next) >= maxCommands {
			return CommandSnippet{}, errors.New("最多保存 500 条命令")
		}
		next = append(next, c)
	} else {
		next[index] = c
	}
	if err = s.persist(next); err != nil {
		return CommandSnippet{}, err
	}
	s.commands = next
	return c, nil
}
func (s *CommandsStore) Delete(id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]CommandSnippet, 0, len(s.commands))
	for _, c := range s.commands {
		if c.ID != id {
			next = append(next, c)
		}
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.commands = next
	return nil
}
func (s *CommandsStore) Render(id string, parameters map[string]string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	s.mu.RLock()
	var command string
	found := false
	for _, c := range s.commands {
		if c.ID == id {
			command = c.Command
			found = true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		return "", errors.New("命令收藏不存在")
	}
	return RenderCommand(command, parameters)
}
func (s *CommandsStore) persist(commands []CommandSnippet) error {
	raw, err := json.MarshalIndent(commandData{Version: 1, Commands: commands}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.directory, "commands.json"), append(raw, '\n'))
}
