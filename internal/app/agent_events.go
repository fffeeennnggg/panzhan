// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"time"
)

type AgentEvent struct {
	ID           uint64     `json:"id"`
	GrantID      string     `json:"grant_id"`
	TaskID       string     `json:"task_id"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	Context      string     `json:"context,omitempty"`
	Directory    string     `json:"directory"`
	Error        string     `json:"error,omitempty"`
	Created      time.Time  `json:"created_at"`
	Acknowledged *time.Time `json:"acknowledged_at,omitempty"`
}

func appendAgentEvents(st *State, previous map[string]string) {
	now := time.Now().UTC()
	for _, job := range st.Jobs {
		if job.AgentID == "" || previous[job.ID] == job.Status || (job.Status != "completed" && job.Status != "attention" && job.Status != "cancelled") {
			continue
		}
		st.AgentEventSeq++
		st.AgentEvents = append(st.AgentEvents, AgentEvent{ID: st.AgentEventSeq, GrantID: job.AgentID, TaskID: job.ID, Title: job.Title, Status: job.Status, Context: job.AgentContext, Directory: job.Directory, Error: job.Error, Created: now})
	}
	kept := st.AgentEvents[:0]
	for _, e := range st.AgentEvents {
		if e.Acknowledged == nil || now.Sub(*e.Acknowledged) < 24*time.Hour {
			kept = append(kept, e)
		}
	}
	st.AgentEvents = kept
}
func (a *App) agentEvents(ctx context.Context, g AgentGrant, wait int) ([]AgentEvent, error) {
	if wait < 0 || wait > 20 {
		return nil, errors.New("通知等待时长需为 0–20 秒")
	}
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	for {
		a.mu.Lock()
		valid := liveAgentScope(&a.state, g.ID, "events:read")
		events := []AgentEvent{}
		for _, event := range a.state.AgentEvents {
			if event.GrantID == g.ID && event.Acknowledged == nil {
				events = append(events, event)
				if len(events) >= 50 {
					break
				}
			}
		}
		a.mu.Unlock()
		if !valid {
			return nil, errors.New("Agent 通知授权已失效")
		}
		if len(events) > 0 || wait == 0 {
			return events, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return events, nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}
func (a *App) ackAgentEvent(g AgentGrant, id uint64) error {
	return a.change(func(st *State) error {
		if !liveAgentScope(st, g.ID, "events:read") {
			return errors.New("Agent 通知授权已失效")
		}
		for i := range st.AgentEvents {
			e := &st.AgentEvents[i]
			if e.ID == id && e.GrantID == g.ID {
				if e.Acknowledged == nil {
					now := time.Now().UTC()
					e.Acknowledged = &now
				}
				return nil
			}
		}
		return errors.New("通知不存在")
	})
}
