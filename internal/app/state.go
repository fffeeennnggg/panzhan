// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"panzhan/internal/download"
	"panzhan/internal/quark"
	"panzhan/internal/search"
)

type Settings struct {
	DefaultDir                string            `json:"default_dir"`
	DownloadRoot              string            `json:"download_root,omitempty"`
	AgentCategoryDirs         map[string]string `json:"agent_category_dirs,omitempty"`
	Concurrency               int               `json:"concurrency"`
	ChunkMiB                  int               `json:"chunk_mib"`
	SearchPriorityConcurrency int               `json:"search_priority_concurrency"`
	SearchChannelConcurrency  int               `json:"search_channel_concurrency"`
	Sources                   []string          `json:"sources"`
}
type SavedFile struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	MD5        string `json:"md5,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Downloaded int64  `json:"downloaded"`
	Done       bool   `json:"done"`
}
type Batch struct {
	Offset int      `json:"offset"`
	Count  int      `json:"count"`
	Dest   string   `json:"dest"`
	Intent bool     `json:"intent"`
	TaskID string   `json:"task_id"`
	IDs    []string `json:"ids"`
	Done   bool     `json:"done"`
}
type Job struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Share        quark.Share   `json:"share"`
	Directory    string        `json:"directory"`
	DownloadRoot string        `json:"download_root,omitempty"`
	Status       string        `json:"status"`
	Error        string        `json:"error,omitempty"`
	RetryCount   int           `json:"retry_count,omitempty"`
	NextRetryAt  string        `json:"next_retry_at,omitempty"`
	Created      string        `json:"created"`
	CloudDir     string        `json:"cloud_dir"`
	Original     []quark.File  `json:"original"`
	Batches      []Batch       `json:"batches"`
	Files        []SavedFile   `json:"files"`
	Enumerated   bool          `json:"enumerated"`
	Progress     *TaskProgress `json:"progress,omitempty"`
	Uncertain    bool          `json:"uncertain"`
	AgentID      string        `json:"agent_id,omitempty"`
	AgentContext string        `json:"agent_context,omitempty"`
	AgentRequest string        `json:"agent_request,omitempty"`
	AgentChoice  string        `json:"agent_choice,omitempty"`
}
type State struct {
	Version         int           `json:"version"`
	Salt            string        `json:"salt"`
	PasswordHash    string        `json:"password_hash"`
	CookieCipher    string        `json:"cookie_cipher"`
	AccountRevision uint64        `json:"account_revision"`
	Settings        Settings      `json:"settings"`
	Jobs            []Job         `json:"jobs"`
	Agents          []AgentGrant  `json:"agents,omitempty"`
	AgentChoices    []AgentChoice `json:"agent_choices,omitempty"`
	AgentEvents     []AgentEvent  `json:"agent_events,omitempty"`
	AgentEventSeq   uint64        `json:"agent_event_seq,omitempty"`
}
type session struct{ expires time.Time }
type App struct {
	mu                sync.Mutex
	state             State
	dataDir, rootPath string
	key               []byte
	requestToken      string
	sessions          map[string]session
	failures          []time.Time
	search            *search.Engine
	newProvider       func(string, func(string)) *quark.Client
	checkShare        func(context.Context, string) quark.ShareCheck
	active            map[string]context.CancelFunc
	transfers         map[string]*transferState
	wake              chan struct{}
	running           bool
	diagnosing        bool
	agentMu           sync.Mutex
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func defaultSearchSources() []string {
	sources := search.New().Sources()
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	return ids
}

func migrateSearchSources(enabled []string) []string {
	if len(enabled) == 0 {
		return enabled
	}
	seen := make(map[string]struct{}, len(enabled))
	for _, id := range enabled {
		seen[id] = struct{}{}
	}
	for _, id := range defaultSearchSources() {
		if !strings.HasPrefix(id, "panplugin:") {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		enabled = append(enabled, id)
		seen[id] = struct{}{}
	}
	return enabled
}

func atomicFile(name string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".panzhan-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(tmp, name)
	}
	if err == nil {
		if d, e := os.Open(filepath.Dir(name)); e == nil {
			_ = d.Sync()
			d.Close()
		}
	}
	return err
}
func New(dataDir, rootPath string) (*App, error) {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	rootPath, err = filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(rootPath, 0750); err != nil {
		return nil, err
	}
	a := &App{dataDir: dataDir, rootPath: rootPath, sessions: map[string]session{}, active: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1), search: search.New(), newProvider: quark.New, state: State{Version: 1, Settings: Settings{DownloadRoot: rootPath, Concurrency: 3, ChunkMiB: 10, SearchPriorityConcurrency: 3, SearchChannelConcurrency: 8, Sources: defaultSearchSources()}, Jobs: []Job{}}}
	a.checkShare = quark.NewShareChecker().Check
	a.requestToken = randomID() + randomID()
	a.key, err = os.ReadFile(filepath.Join(dataDir, "master.key"))
	newInstall := os.IsNotExist(err)
	if err == nil {
		if _, stateErr := os.Stat(filepath.Join(dataDir, "state.json")); os.IsNotExist(stateErr) {
			return nil, errors.New("state.json 缺失，请从同一次备份恢复状态和密钥；不会重置已有安装")
		} else if stateErr != nil {
			return nil, stateErr
		}
	}
	if os.IsNotExist(err) {
		if _, stateErr := os.Stat(filepath.Join(dataDir, "state.json")); stateErr == nil {
			return nil, errors.New("master.key 缺失，请从同一次备份恢复密钥；不会创建替代密钥")
		} else if !os.IsNotExist(stateErr) {
			return nil, stateErr
		}
		a.key = make([]byte, 32)
		if _, err = rand.Read(a.key); err != nil {
			return nil, err
		}
		err = atomicFile(filepath.Join(dataDir, "master.key"), a.key)
	}
	if err != nil {
		return nil, err
	}
	if len(a.key) != 32 {
		return nil, errors.New("凭据密钥无效，请恢复 master.key")
	}
	b, err := os.ReadFile(filepath.Join(dataDir, "state.json"))
	if err == nil {
		if err = json.Unmarshal(b, &a.state); err != nil {
			return nil, fmt.Errorf("任务状态文件损坏，已停止启动以保护记录：%w", err)
		}
		if a.state.Version != 1 {
			return nil, errors.New("不支持的状态文件版本")
		}
		a.state.Settings.Sources = migrateSearchSources(a.state.Settings.Sources)
		if a.state.Settings.DownloadRoot == "" {
			a.state.Settings.DownloadRoot = rootPath
		}
		if a.state.Settings.SearchPriorityConcurrency == 0 {
			a.state.Settings.SearchPriorityConcurrency = 3
		}
		if a.state.Settings.SearchChannelConcurrency == 0 {
			a.state.Settings.SearchChannelConcurrency = 8
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if !newInstall {
		return nil, errors.New("state.json 缺失，请恢复配套备份")
	}
	for i := range a.state.Jobs {
		j := &a.state.Jobs[i]
		if legacyRetryableJob(*j) {
			j.Status = "retrying"
			j.RetryCount++
			j.NextRetryAt = time.Now().Add(taskRetryDelay(j.RetryCount)).Format(time.RFC3339)
		}
		if j.Status == "transferring" || j.Status == "downloading" || j.Status == "verifying" {
			j.Status = "queued"
		}
		if j.DownloadRoot == "" {
			j.DownloadRoot = rootPath
		}
	}
	if err = a.saveLocked(); err != nil {
		return nil, err
	}
	bootstrapPath := filepath.Join(dataDir, "bootstrap-password")
	bootstrapPassword, bootstrapErr := os.ReadFile(bootstrapPath)
	if bootstrapErr == nil {
		password := strings.TrimSuffix(string(bootstrapPassword), "\n")
		password = strings.TrimSuffix(password, "\r")
		if passwordLength := utf8.RuneCountInString(password); passwordLength < 8 || passwordLength > 128 {
			return nil, errors.New("飞牛安装或设置中的管理密码无效，请在应用中心重新配置")
		}
		// The package manager stages this file only after an explicit password
		// choice in its install/config wizard. Apply that choice even if fnOS
		// preserved an earlier state.json; leave every other state field intact.
		if err = a.change(func(st *State) error {
			st.Salt = randomID()
			st.PasswordHash = passwordHash(password, st.Salt)
			return nil
		}); err != nil {
			return nil, err
		}
		if err = os.Remove(bootstrapPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("无法清除安装向导中的临时密码：%w", err)
		}
		if a.isConfigured() {
			if err = os.Remove(filepath.Join(dataDir, "setup-token.txt")); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("无法清除旧初始化令牌：%w", err)
			}
		}
	} else if !os.IsNotExist(bootstrapErr) {
		return nil, bootstrapErr
	}
	if !a.isConfigured() {
		if _, err = os.Stat(filepath.Join(dataDir, "setup-token.txt")); os.IsNotExist(err) {
			if err = atomicFile(filepath.Join(dataDir, "setup-token.txt"), []byte(randomID()+"\n")); err != nil {
				return nil, err
			}
		}
	}
	a.search, err = search.NewPersistent(filepath.Join(dataDir, "resources.json"))
	if err != nil {
		// The catalog is an optimization; its corruption must not disable
		// authentication, task recovery, or direct Quark imports.
		a.search = search.New()
		a.search.SetCatalogWarning("本地资源库无法读取，搜索仍可继续，但本次结果不会写入；请检查 resources.json 并恢复备份")
	}
	return a, nil
}
func (a *App) saveLocked() error {
	b, err := json.MarshalIndent(a.state, "", "  ")
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(a.dataDir, "state.json"), b)
}
func (a *App) change(fn func(*State) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	old, _ := json.Marshal(a.state)
	restore := func() { var previous State; _ = json.Unmarshal(old, &previous); a.state = previous }
	previousJobs := make(map[string]string, len(a.state.Jobs))
	for _, job := range a.state.Jobs {
		previousJobs[job.ID] = job.Status
	}
	if err := fn(&a.state); err != nil {
		restore()
		return err
	}
	appendAgentEvents(&a.state, previousJobs)
	if err := a.saveLocked(); err != nil {
		restore()
		return errors.New("本地状态保存失败，操作未提交")
	}
	return nil
}
func (a *App) snapshot() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, _ := json.Marshal(a.state)
	var st State
	_ = json.Unmarshal(b, &st)
	return st
}
func (a *App) isConfigured() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.PasswordHash != ""
}
func (a *App) seal(value string) (string, error) {
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(value), []byte("panzhan-cookie-v1"))), nil
}
func (a *App) unseal(value string) (string, error) {
	if value == "" {
		return "", errors.New("请先连接夸克账号")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, _ := aes.NewCipher(a.key)
	gcm, _ := cipher.NewGCM(block)
	if len(data) < gcm.NonceSize() {
		return "", errors.New("凭据损坏")
	}
	b, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte("panzhan-cookie-v1"))
	if err != nil {
		return "", errors.New("凭据解密失败，请恢复密钥或重新配置")
	}
	return string(b), nil
}
func (a *App) provider() (*quark.Client, error) {
	st := a.snapshot()
	cookie, err := a.unseal(st.CookieCipher)
	if err != nil {
		return nil, err
	}
	return a.newProvider(cookie, func(value string) {
		encrypted, err := a.seal(value)
		if err == nil {
			_ = a.change(func(current *State) error {
				if current.AccountRevision == st.AccountRevision {
					current.CookieCipher = encrypted
				}
				return nil
			})
		}
	}), nil
}
func (a *App) directory(rel string) (*os.Root, error) {
	return a.directoryAt(a.rootPath, rel)
}

func (a *App) directoryAt(rootPath, rel string) (*os.Root, error) {
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	rootPath = filepath.Clean(rootPath)
	if rel != "" {
		if err := download.ValidatePath(rel); err != nil {
			return nil, err
		}
	}
	r, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return r, nil
	}
	child, err := r.OpenRoot(filepath.FromSlash(rel))
	r.Close()
	return child, err
}

func validateDownloadRoot(rootPath string, writable bool) (string, error) {
	if !filepath.IsAbs(rootPath) {
		return "", errors.New("请输入 NAS 上的绝对共享目录路径")
	}
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	rootPath = filepath.Clean(rootPath)
	r, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	f, err := r.Open(".")
	if err != nil {
		return "", err
	}
	info, statErr := f.Stat()
	f.Close()
	if statErr != nil || !info.IsDir() {
		return "", errors.New("授权路径不是可读目录")
	}
	if writable {
		name := ".panzhan-access-check-" + randomID()
		probe, createErr := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if createErr != nil {
			return "", errors.New("应用没有该目录的写入权限，请在飞牛应用设置中授予目录访问权限")
		}
		if err = probe.Close(); err != nil {
			_ = r.Remove(name)
			return "", err
		}
		if err = r.Remove(name); err != nil {
			return "", errors.New("无法清理目录权限检查文件")
		}
	}
	return rootPath, nil
}
func safeTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune("/\\:*?\"<>|", r) {
			return '-'
		}
		return r
	}, title)
	if len([]rune(title)) > 64 {
		title = string([]rune(title)[:64])
	}
	title = strings.Trim(title, ". ")
	if title == "" {
		title = "资源"
	}
	return title
}
