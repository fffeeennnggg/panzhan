package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"panzhan/internal/quark"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPausedUncertainTransferCanBeReconciled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":200,"code":0,"data":{"list":[]}}`))
	}))
	defer srv.Close()
	a, _ := New(t.TempDir(), t.TempDir())
	a.newProvider = func(c string, cb func(string)) *quark.Client { return quark.NewAt(srv.URL, c, cb) }
	cipher, _ := a.seal("__puus=old")
	a.change(func(st *State) error {
		st.CookieCipher = cipher
		st.Jobs = []Job{{ID: "paused", Status: "paused", Uncertain: true, Batches: []Batch{{Intent: true, Dest: "dest", Count: 1}}}}
		return nil
	})
	if err := a.action(context.Background(), "paused", "resubmit", ""); err != nil {
		t.Fatal("paused uncertain task is stuck", err)
	}
	j, _ := a.job("paused")
	if j.Uncertain || j.Status != "queued" || j.Batches[0].Intent {
		t.Fatal(j)
	}
}

func TestWorkerReportsFailureInsteadOfLeavingQueued(t *testing.T) {
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.change(func(s *State) error { s.Jobs = append(s.Jobs, Job{ID: "bad", Status: "queued"}); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		j, _ := a.job("bad")
		if j.Status == "attention" && j.Error != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not persist error")
}

func TestTaskPipelineSavesRealFileAndReusesTransferredIDs(t *testing.T) {
	payload := "real file on NAS"
	saved := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(data any) { json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": data}) }
		switch r.URL.Path {
		case "/share/sharepage/token":
			write(map[string]any{"stoken": "st"})
		case "/share/sharepage/detail":
			write(map[string]any{"list": []any{map[string]any{"fid": "shared", "file_name": "hello.txt", "size": len(payload), "file": true, "share_fid_token": "ft"}}})
		case "/file/sort":
			p := r.URL.Query().Get("pdir_fid")
			var files []any
			if p == "batch-folder" {
				files = []any{map[string]any{"fid": "owned", "file_name": "hello.txt", "file": true, "size": len(payload)}}
			} else {
				files = []any{}
			}
			write(map[string]any{"list": files})
		case "/file":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			fid := "cloud"
			if strings.HasPrefix(b["file_name"].(string), "batch-") {
				fid = "batch-folder"
			}
			write(map[string]any{"fid": fid})
		case "/share/sharepage/save":
			saved++
			write(map[string]any{"task_id": "save-task"})
		case "/task":
			write(map[string]any{"status": 2, "save_as": map[string]any{"save_as_top_fids": []string{"owned"}}})
		case "/file/download":
			var b map[string][]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["fids"][0] != "owned" {
				t.Error("used share fid")
			}
			write([]any{map[string]any{"download_url": srv.URL + "/blob"}})
		case "/blob":
			var start, end int
			fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
			w.WriteHeader(206)
			w.Write([]byte(payload[start : end+1]))
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.newProvider = func(cookie string, cb func(string)) *quark.Client { return quark.NewAt(srv.URL, cookie, cb) }
	cipher, _ := a.seal("cookie")
	a.change(func(s *State) error { s.CookieCipher = cipher; return nil })
	job, err := a.createJob(context.Background(), "https://pan.quark.cn/s/abcd", "Test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := a.job(job.ID)
		if j.Status == "completed" {
			data, er := os.ReadFile(filepath.Join(a.rootPath, j.Directory, "hello.txt"))
			if er != nil || string(data) != payload {
				t.Fatal(er, string(data))
			}
			if saved != 1 {
				t.Fatal("duplicate save")
			}
			return
		}
		if j.Status == "attention" {
			t.Fatal(j.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("task timeout", a.snapshot().Jobs)
}
