package core

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Cipher interface {
	Encrypt([]byte) (string, error)
	Decrypt(string) ([]byte, error)
}

type storeData struct {
	Version      int               `json:"version"`
	Profiles     []Profile         `json:"profiles"`
	Secrets      map[string]string `json:"secrets"`
	Hosts        map[string]string `json:"hosts"`
	Settings     Settings          `json:"settings"`
	MigratedFrom string            `json:"migratedFrom,omitempty"`
}

type Store struct {
	mu              sync.RWMutex
	directory       string
	cipher          Cipher
	data            storeData
	migrationNotice string
}

func emptyData() storeData {
	return storeData{Version: 2, Profiles: make([]Profile, 0), Secrets: map[string]string{}, Hosts: map[string]string{}, Settings: DefaultSettings()}
}

func readConfig(file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 8*1024*1024 {
		return nil, errors.New("配置文件超过大小限制")
	}
	return raw, nil
}

// The destination is only replaced after the complete file has been flushed.
func atomicWrite(file string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+"-*.tmp")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp, file)
}

func cloneProfile(p Profile) Profile { p.Tags = append([]string{}, p.Tags...); return p }
func cloneData(d storeData) storeData {
	n := d
	n.Profiles = make([]Profile, len(d.Profiles))
	for i, p := range d.Profiles {
		n.Profiles[i] = cloneProfile(p)
	}
	n.Secrets = map[string]string{}
	for k, v := range d.Secrets {
		n.Secrets[k] = v
	}
	n.Hosts = map[string]string{}
	for k, v := range d.Hosts {
		n.Hosts[k] = v
	}
	return n
}

const maxJumpHops = 5

func profileIndex(profiles []Profile) map[string]Profile {
	byID := make(map[string]Profile, len(profiles))
	for _, p := range profiles {
		byID[p.ID] = p
	}
	return byID
}

// ResolveJumpProfiles returns the target's immediate jump first and the outermost last.
func ResolveJumpProfiles(profiles []Profile, target Profile) ([]Profile, error) {
	return jumpRoute(profileIndex(profiles), target)
}

func jumpRoute(byID map[string]Profile, target Profile) ([]Profile, error) {
	if target.JumpProfileID == "" {
		return nil, nil
	}
	seen := map[string]bool{target.ID: true}
	var route []Profile
	for id := target.JumpProfileID; id != ""; {
		if seen[id] {
			return nil, errors.New("跳板机配置存在循环引用")
		}
		if len(route) >= maxJumpHops {
			return nil, errors.New("最多支持 5 跳 SSH 跳板机")
		}
		hop, ok := byID[id]
		if !ok {
			return nil, errors.New("跳板机配置不存在，请先保存跳板机")
		}
		seen[id] = true
		route = append(route, hop)
		id = hop.JumpProfileID
	}
	return route, nil
}

func validateProfileLinks(profiles []Profile) error {
	byID := profileIndex(profiles)
	if len(byID) != len(profiles) {
		return errors.New("重复的连接标识符")
	}
	for _, p := range profiles {
		if _, err := jumpRoute(byID, p); err != nil {
			return err
		}
	}
	return nil
}

func parseStoreData(raw []byte, version int) (storeData, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return storeData{}, errors.New("配置文件格式不正确")
	}
	if len(object["settings"]) == 0 || string(object["settings"]) == "null" {
		return storeData{}, errors.New("缺少界面设置")
	}
	d := emptyData()
	if err := json.Unmarshal(raw, &d); err != nil {
		return storeData{}, errors.New("配置文件字段格式不正确")
	}
	if d.Version != version || d.Profiles == nil || len(d.Profiles) > 2000 {
		return storeData{}, errors.New("不支持的配置文件版本或连接列表")
	}
	if len(object["profiles"]) == 0 {
		return storeData{}, errors.New("缺少连接列表")
	}
	// Versions before the sidebar rail had no panel field. Match its one-time
	// migration to the right sidebar while retaining the other preferences.
	var settingsFields map[string]json.RawMessage
	if err := json.Unmarshal(object["settings"], &settingsFields); err != nil {
		return storeData{}, errors.New("无效的界面设置")
	}
	if _, ok := settingsFields["sidebarPanel"]; !ok {
		d.Settings.SidebarSide = "right"
	}
	settings, err := ValidateSettings(d.Settings)
	if err != nil {
		return storeData{}, err
	}
	d.Settings = settings
	for i, p := range d.Profiles {
		if p.ID == "" {
			return storeData{}, errors.New("连接标识符缺失")
		}
		valid, err := ValidateProfile(p)
		if err != nil {
			return storeData{}, err
		}
		d.Profiles[i] = valid
	}
	if err := validateProfileLinks(d.Profiles); err != nil {
		return storeData{}, err
	}
	if d.Secrets == nil {
		d.Secrets = map[string]string{}
	}
	if d.Hosts == nil {
		d.Hosts = map[string]string{}
	}
	for id, value := range d.Secrets {
		if err := ValidateID(id); err != nil {
			return storeData{}, err
		}
		if len(value) > 256*1024 {
			return storeData{}, errors.New("凭据存储格式不正确")
		}
	}
	for host, fp := range d.Hosts {
		if err := validateTrustedHost(host, fp); err != nil {
			return storeData{}, err
		}
	}
	return d, nil
}

func sameDirectory(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	if aa == bb || (runtime.GOOS == "windows" && strings.EqualFold(aa, bb)) {
		return true
	}
	aInfo, errA := os.Stat(aa)
	bInfo, errB := os.Stat(bb)
	return errA == nil && errB == nil && os.SameFile(aInfo, bInfo)
}

func NewStore(directory, legacyDir string, cipher Cipher) (*Store, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("配置目录不能为空")
	}
	if legacyDir != "" && sameDirectory(directory, legacyDir) {
		return nil, errors.New("新版配置必须使用独立目录，旧版配置未被修改")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	s := &Store{directory: directory, cipher: cipher, data: emptyData()}
	raw, err := readConfig(filepath.Join(directory, "settings.json"))
	if err == nil {
		s.data, err = parseStoreData(raw, 2)
		if err != nil {
			return nil, fmt.Errorf("读取 settings.json 失败，原文件未被覆盖：%w", err)
		}
		return s, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取 settings.json 失败，原文件未被覆盖：%w", err)
	}
	if legacyDir != "" {
		legacy := emptyData()
		legacyRaw, readErr := readConfig(filepath.Join(legacyDir, "settings.json"))
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, fmt.Errorf("读取旧版配置失败，原文件未被覆盖：%w", readErr)
		}
		if readErr == nil {
			legacy, err = parseStoreData(legacyRaw, 1)
			if err != nil {
				return nil, fmt.Errorf("旧版配置迁移失败，原文件未被覆盖：%w", err)
			}
		}
		// Electron could save a global command before any settings or profiles.
		legacyCommands, commandErr := readConfig(filepath.Join(legacyDir, "commands.json"))
		if commandErr != nil && !errors.Is(commandErr, os.ErrNotExist) {
			return nil, fmt.Errorf("读取旧版命令失败：%w", commandErr)
		}
		if commandErr == nil {
			if _, err = parseCommands(legacyCommands); err != nil {
				return nil, fmt.Errorf("旧版命令迁移失败，原文件未被覆盖：%w", err)
			}
		}
		if legacyRaw != nil || legacyCommands != nil {
			// Validate everything before writing any new active configuration.
			backup := filepath.Join(directory, "migration-backup")
			if legacyRaw != nil {
				if err = atomicWrite(filepath.Join(backup, "settings.v1.json"), legacyRaw); err != nil {
					return nil, err
				}
			}
			if legacyCommands != nil {
				if err = atomicWrite(filepath.Join(backup, "commands.v1.json"), legacyCommands); err != nil {
					return nil, err
				}
				commandPath := filepath.Join(directory, "commands.json")
				if _, statErr := os.Stat(commandPath); errors.Is(statErr, os.ErrNotExist) {
					if err = atomicWrite(commandPath, legacyCommands); err != nil {
						return nil, err
					}
				} else if statErr != nil {
					return nil, statErr
				}
			}
			legacy.Version, legacy.MigratedFrom = 2, "electron-v1"
			legacy.Secrets = map[string]string{}
			for i := range legacy.Profiles {
				legacy.Profiles[i].RememberCredentials = false
				legacy.Profiles[i].HasCredentials = false
			}
			s.data = legacy
			if legacyRaw != nil {
				s.migrationNotice = "已导入旧版服务器、可信主机、偏好和命令收藏。旧版保存的密码或私钥口令需重新输入，并可重新选择记住；旧数据已保留。"
			} else {
				s.migrationNotice = "已导入旧版命令收藏。旧数据已保留。"
			}
		}
	}
	if err = s.persist(s.data); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) persist(data storeData) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.directory, "settings.json"), append(raw, '\n'))
}

func publicProfile(data storeData, p Profile) Profile {
	return profileWithIdentity(data, profileIndex(data.Profiles), p)
}

func profileWithIdentity(data storeData, byID map[string]Profile, p Profile) Profile {
	p = cloneProfile(p)
	p.HasCredentials = data.Secrets[p.ID] != ""
	p.JumpIdentity = ""
	route, _ := jumpRoute(byID, p)
	for i := len(route) - 1; i >= 0; i-- {
		hop := route[i]
		hop.JumpIdentity = p.JumpIdentity
		p.JumpIdentity = ProfileIdentity(hop)
	}
	return p
}
func (s *Store) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Profile, 0, len(s.data.Profiles))
	byID := profileIndex(s.data.Profiles)
	for _, p := range s.data.Profiles {
		result = append(result, profileWithIdentity(s.data, byID, p))
	}
	return result
}
func (s *Store) Get(id string) (Profile, error) {
	if err := ValidateID(id); err != nil {
		return Profile{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.data.Profiles {
		if p.ID == id {
			return publicProfile(s.data, p), nil
		}
	}
	return Profile{}, errors.New("连接配置不存在")
}
func (s *Store) Settings() Settings { s.mu.RLock(); defer s.mu.RUnlock(); return s.data.Settings }
func (s *Store) MigrationNotice() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.migrationNotice
}
func (s *Store) SaveSettings(settings Settings) error {
	valid, err := ValidateSettings(settings)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneData(s.data)
	next.Settings = valid
	if err = s.persist(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

func sameAuthentication(a, b Profile) bool {
	return strings.EqualFold(a.Host, b.Host) && a.Port == b.Port && a.Username == b.Username && a.AuthType == b.AuthType && a.KeyPath == b.KeyPath
}
func (s *Store) SaveProfile(input Profile, credentials Credentials) (Profile, error) {
	p, err := ValidateProfile(input)
	if err != nil {
		return Profile{}, err
	}
	if err = ValidateCredentials(credentials); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneData(s.data)
	index := -1
	var old Profile
	for i, item := range s.data.Profiles {
		if item.ID == p.ID {
			index = i
			old = publicProfile(s.data, item)
			break
		}
	}
	p.LastConnectedAt = 0
	if index >= 0 && ProfileIdentity(old) == ProfileIdentity(publicProfile(next, p)) {
		p.LastConnectedAt = old.LastConnectedAt
	}
	if index < 0 {
		if len(next.Profiles) >= 2000 {
			return Profile{}, errors.New("最多保存 2000 个连接")
		}
		next.Profiles = append(next.Profiles, p)
	} else {
		next.Profiles[index] = p
	}
	if err = validateProfileLinks(next.Profiles); err != nil {
		return Profile{}, err
	}
	if index < 0 || !sameAuthentication(old, p) || !p.RememberCredentials {
		delete(next.Secrets, p.ID)
	}
	secret := credentials.Password
	if p.AuthType == "key" {
		secret = credentials.Passphrase
	}
	if p.RememberCredentials && secret != nil {
		if s.cipher == nil {
			return Profile{}, errors.New("系统安全存储不可用，请取消记住凭据后重试")
		}
		value := Credentials{}
		if p.AuthType == "key" {
			value.Passphrase = secret
		} else {
			value.Password = secret
		}
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return Profile{}, marshalErr
		}
		encrypted, cipherErr := s.cipher.Encrypt(raw)
		clear(raw)
		if cipherErr != nil {
			return Profile{}, errors.New("系统安全存储加密失败，请取消记住凭据后重试")
		}
		next.Secrets[p.ID] = encrypted
	}
	if index >= 0 && ProfileIdentity(old) != ProfileIdentity(publicProfile(next, p)) {
		before, after := profileIndex(s.data.Profiles), profileIndex(next.Profiles)
		for i := range next.Profiles {
			updated := next.Profiles[i]
			prior := before[updated.ID]
			if updated.JumpProfileID != "" && ProfileIdentity(profileWithIdentity(next, after, updated)) != ProfileIdentity(profileWithIdentity(s.data, before, prior)) {
				next.Profiles[i].LastConnectedAt = 0
			}
		}
	}
	if err = s.persist(next); err != nil {
		return Profile{}, err
	}
	s.data = next
	return publicProfile(next, p), nil
}

func (s *Store) Delete(id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneData(s.data)
	for _, p := range next.Profiles {
		if p.JumpProfileID == id {
			return errors.New("此连接正被其他服务器用作跳板机，请先移除引用")
		}
	}
	next.Profiles = next.Profiles[:0]
	for _, p := range s.data.Profiles {
		if p.ID != id {
			next.Profiles = append(next.Profiles, cloneProfile(p))
		}
	}
	delete(next.Secrets, id)
	if err := s.persist(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

func (s *Store) Credentials(id string) (Credentials, error) {
	if err := ValidateID(id); err != nil {
		return Credentials{}, err
	}
	s.mu.RLock()
	encrypted := s.data.Secrets[id]
	cipher := s.cipher
	s.mu.RUnlock()
	return decryptCredentials(cipher, encrypted)
}

func decryptCredentials(cipher Cipher, encrypted string) (Credentials, error) {
	if encrypted == "" {
		return Credentials{}, nil
	}
	if cipher == nil {
		return Credentials{}, errors.New("系统安全存储不可用，请重新输入凭据")
	}
	raw, err := cipher.Decrypt(encrypted)
	if err != nil {
		return Credentials{}, errors.New("读取保存的凭据失败，请重新输入")
	}
	defer clear(raw)
	var credentials Credentials
	if err = json.Unmarshal(raw, &credentials); err != nil {
		return Credentials{}, errors.New("保存的凭据格式不正确，请重新输入")
	}
	if err = ValidateCredentials(credentials); err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

type SSHLogin struct {
	Profile     Profile
	Credentials Credentials
	Jumps       []Jump
}

func (s *Store) ResolveSSHLogin(id string, supplied, legacyJump Credentials, hopInputs map[string]Credentials, expectedIdentity *string) (SSHLogin, error) {
	if err := ValidateID(id); err != nil {
		return SSHLogin{}, err
	}
	if len(hopInputs) > maxJumpHops {
		return SSHLogin{}, errors.New("跳板机凭据超过允许数量")
	}
	// Store writes publish a new copy, so the route and its saved credentials
	// below come from the same snapshot even if a profile is edited concurrently.
	s.mu.RLock()
	data, cipher := s.data, s.cipher
	s.mu.RUnlock()
	byID := profileIndex(data.Profiles)
	target, ok := byID[id]
	if !ok {
		return SSHLogin{}, errors.New("连接配置不存在")
	}
	route, err := jumpRoute(byID, target)
	if err != nil {
		return SSHLogin{}, err
	}
	profile := profileWithIdentity(data, byID, target)
	if expectedIdentity != nil && !matchesConnectionIdentity(profile, *expectedIdentity) {
		return SSHLogin{}, errors.New("连接配置已变化，请重新打开连接")
	}
	for id := range hopInputs {
		found := false
		for _, hop := range route {
			if hop.ID == id {
				found = true
				break
			}
		}
		if !found {
			return SSHLogin{}, errors.New("跳板机链路已变化，请重新确认登录凭据")
		}
	}
	merge := func(profile Profile, credentials Credentials) (Credentials, error) {
		if profile.AuthType == "agent" || profile.AuthType == "interactive" {
			return Credentials{}, nil
		}
		if credentials.Password != nil || credentials.Passphrase != nil {
			return credentials, ValidateCredentials(credentials)
		}
		return decryptCredentials(cipher, data.Secrets[profile.ID])
	}
	credentials, err := merge(target, supplied)
	if err != nil {
		return SSHLogin{}, err
	}
	login := SSHLogin{Profile: profile, Credentials: credentials}
	for i, hop := range route {
		provided := hopInputs[hop.ID]
		if i == 0 && hopInputs == nil {
			provided = legacyJump
		}
		value, err := merge(hop, provided)
		if err != nil {
			return SSHLogin{}, fmt.Errorf("跳板机 %s：%w", hop.Name, err)
		}
		login.Jumps = append(login.Jumps, Jump{Profile: hop, Credentials: value})
	}
	return login, nil
}

func matchesConnectionIdentity(profile Profile, expected string) bool {
	var fields []any
	if err := json.Unmarshal([]byte(expected), &fields); err != nil || len(fields) < 2 {
		return false
	}
	host, ok := fields[1].(string)
	if !ok {
		return false
	}
	// Use the same host normalization and JSON encoding as the stored identity.
	fields[1] = profileIdentityHost(host)
	canonical, err := json.Marshal(fields)
	return err == nil && string(canonical) == ProfileIdentity(profile)
}

func validateTrustedHost(host, fp string) error {
	if err := validateText(host, "主机身份", 512); err != nil {
		return err
	}
	if host == "" || strings.ContainsAny(host, "\r\n") {
		return errors.New("主机身份格式不正确")
	}
	if !strings.HasPrefix(fp, "SHA256:") {
		return errors.New("主机指纹格式不正确")
	}
	digest, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(fp, "SHA256:"))
	if err != nil || len(digest) != 32 {
		return errors.New("主机指纹格式不正确")
	}
	return nil
}
func (s *Store) Fingerprint(host string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Hosts[host]
}
func (s *Store) Trust(host, fp string) error {
	if err := validateTrustedHost(host, fp); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneData(s.data)
	next.Hosts[host] = fp
	if err := s.persist(next); err != nil {
		return err
	}
	s.data = next
	return nil
}
func (s *Store) RecordConnected(id, identity string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneData(s.data)
	for i, p := range next.Profiles {
		if p.ID != id {
			continue
		}
		if ProfileIdentity(publicProfile(next, p)) != identity {
			return nil
		}
		now := time.Now().UnixMilli()
		if now > next.Profiles[i].LastConnectedAt {
			next.Profiles[i].LastConnectedAt = now
		}
		if err := s.persist(next); err != nil {
			return err
		}
		s.data = next
		return nil
	}
	return nil
}
