// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

var agentScopes = []string{"resources:read", "downloads:create", "tasks:read", "events:read"}

type AgentGrant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	Scopes    []string  `json:"scopes"`
	Root      string    `json:"root"`
	Directory string    `json:"directory"`
	Created   time.Time `json:"created_at"`
	Expires   time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
}
type AgentChoice struct {
	ID      string    `json:"id"`
	GrantID string    `json:"grant_id"`
	URL     string    `json:"url"`
	Code    string    `json:"code,omitempty"`
	Title   string    `json:"title"`
	Context string    `json:"context,omitempty"`
	Expires time.Time `json:"expires_at"`
	TaskID  string    `json:"task_id,omitempty"`
}

func (g AgentGrant) active() bool { return !g.Revoked && time.Now().Before(g.Expires) }
func liveAgentScope(st *State, id, scope string) bool {
	for _, g := range st.Agents {
		if g.ID == id && g.active() && slices.Contains(g.Scopes, scope) {
			return true
		}
	}
	return false
}
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (a *App) agentAuth(r *http.Request) (AgentGrant, bool) {
	var empty AgentGrant
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 256 {
		return empty, false
	}
	hash := tokenHash(parts[1])
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, g := range a.state.Agents {
		if constantEqual(g.TokenHash, hash) && g.active() {
			return g, true
		}
	}
	return empty, false
}
func (g AgentGrant) public() map[string]any {
	return map[string]any{"id": g.ID, "name": g.Name, "scopes": g.Scopes, "download_root": g.Root, "directory": g.Directory, "created_at": g.Created, "expires_at": g.Expires, "revoked": g.Revoked, "active": g.active()}
}
func (a *App) createAgent(name, dir string, scopes []string, days int) (AgentGrant, string, error) {
	scopes = append([]string(nil), scopes...)
	var empty AgentGrant
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 120 || days < 1 || days > 365 {
		return empty, "", errors.New("请输入授权名称和 1–365 天有效期")
	}
	if len(scopes) == 0 || len(scopes) > len(agentScopes) {
		return empty, "", errors.New("请选择 Agent 权限")
	}
	for _, s := range scopes {
		if !slices.Contains(agentScopes, s) {
			return empty, "", errors.New("未知 Agent 权限")
		}
	}
	settings := a.snapshot().Settings
	root := settings.DownloadRoot
	if root == "" {
		root = a.rootPath
	}
	if dir == "" {
		dir = settings.DefaultDir
	}
	r, err := a.directoryAt(root, dir)
	if err != nil {
		return empty, "", errors.New("授权下载目录不存在或无法访问")
	}
	r.Close()
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	token := "pz_agent_" + randomID() + randomID()
	g := AgentGrant{ID: randomID(), Name: name, TokenHash: tokenHash(token), Scopes: scopes, Root: root, Directory: dir, Created: time.Now().UTC(), Expires: time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)}
	err = a.change(func(st *State) error {
		if len(st.Agents) >= 100 {
			return errors.New("授权记录已达上限")
		}
		st.Agents = append(st.Agents, g)
		return nil
	})
	return g, token, err
}
func (a *App) agentAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/agents" && r.Method == http.MethodGet {
		items := []map[string]any{}
		for _, g := range a.snapshot().Agents {
			items = append(items, g.public())
		}
		writeJSON(w, 200, map[string]any{"agents": items, "mcp_path": "/mcp", "api_path": "/api/agent/v1"})
		return
	}
	if r.URL.Path == "/api/agents" && r.Method == http.MethodPost {
		var in struct {
			Name      string   `json:"name"`
			Directory string   `json:"directory"`
			Scopes    []string `json:"scopes"`
			Days      int      `json:"expires_days"`
		}
		if err := body(r, &in); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		g, token, err := a.createAgent(in.Name, in.Directory, in.Scopes, in.Days)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, map[string]any{"agent": g.public(), "token": token})
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/agents/") {
		id := strings.TrimPrefix(r.URL.Path, "/api/agents/")
		var in struct {
			Action string `json:"action"`
		}
		if err := body(r, &in); err != nil || in.Action != "revoke" {
			writeJSON(w, 400, map[string]string{"error": "无效授权操作"})
			return
		}
		err := a.change(func(st *State) error {
			for i := range st.Agents {
				if st.Agents[i].ID == id {
					st.Agents[i].Revoked = true
					now := time.Now().UTC()
					for n := range st.AgentEvents {
						if st.AgentEvents[n].GrantID == id && st.AgentEvents[n].Acknowledged == nil {
							st.AgentEvents[n].Acknowledged = &now
						}
					}
					return nil
				}
			}
			return errors.New("授权不存在")
		})
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	writeJSON(w, 405, map[string]string{"error": "不支持的授权请求"})
}
