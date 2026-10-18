package app

import (
	"bytes"
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

func TestDownloadProbeReadsOwnedFileAndCleansOnlyItsTemporaryFile(t *testing.T) {
	payload := bytes.Repeat([]byte("probe"), 1024)
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "private-cookie" {
			t.Error("missing matching cookie")
		}
		if r.URL.Path == "/file/download" {
			var in struct {
				FIDs []string `json:"fids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.FIDs) != 1 || in.FIDs[0] != "owned-fid" {
				t.Error("unexpected file resolution")
			}
			json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": []any{map[string]string{"download_url": upstream.URL + "/original?secret=signed"}}})
			return
		}
		if r.URL.Path != "/original" {
			t.Error("unexpected request")
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Range") != fmt.Sprintf("bytes=0-%d", len(payload)-1) {
			t.Error("invalid bounded range")
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)))
		w.WriteHeader(206)
		w.Write(payload)
	}))
	defer upstream.Close()
	root := t.TempDir()
	a, err := New(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	a.newProvider = func(cookie string, cb func(string)) *quark.Client { return quark.NewAt(upstream.URL, cookie, cb) }
	encrypted, err := a.seal("private-cookie")
	if err != nil {
		t.Fatal(err)
	}
	err = a.change(func(s *State) error {
		s.CookieCipher = encrypted
		s.Jobs = []Job{{ID: "job", Status: "paused", DownloadRoot: root, Files: []SavedFile{{ID: "owned-fid", Path: "unchanged", Size: int64(len(payload)), Downloaded: 77}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	a.sessions["probe-test"] = session{expires: time.Now().Add(time.Hour)}
	request := httptest.NewRequest("POST", "/api/tasks/job/diagnose", strings.NewReader(`{"file_index":0,"workers":3,"part_mib":1,"seconds":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "panzhan_session", Value: "probe-test"})
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var result downloadProbeResult
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Received != int64(len(payload)) || result.CompleteRanges != 1 || result.Failures != 0 || result.DiskBytes != 8<<20 || result.DiskError != "" {
		t.Fatalf("bad diagnostic: %+v", result)
	}
	for _, secret := range []string{"private-cookie", "secret=signed", upstream.URL, "owned-fid"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Error("diagnostic leaked credential, URL or file ID")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
		t.Fatal("diagnostic left temporary files or removed existing files", entries, err)
	}
	job, _ := a.job("job")
	if job.Status != "paused" || job.Files[0].Downloaded != 77 || a.diagnosing {
		t.Fatal("diagnostic altered task state or retained scheduler lock")
	}
}

func TestDownloadProbeRequiresLoginAndRejectsInvalidOrBusyRequests(t *testing.T) {
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/tasks/job/diagnose", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("anonymous diagnostic allowed", w.Code)
	}
	a.change(func(s *State) error {
		s.Jobs = []Job{{ID: "job", Status: "paused", Files: []SavedFile{{ID: "owned", Size: 100}}}}
		return nil
	})
	for _, options := range []downloadProbeOptions{{Workers: 9}, {PartMiB: 11}, {Seconds: 16}, {FileIndex: -1}, {FileIndex: 1}} {
		if _, err = a.diagnoseDownload(context.Background(), "job", options); err == nil {
			t.Fatal("invalid diagnostic accepted", options)
		}
	}
	a.active["running"] = func() {}
	if _, err = a.diagnoseDownload(context.Background(), "job", downloadProbeOptions{}); err == nil || !strings.Contains(err.Error(), "暂停") {
		t.Fatal("diagnostic raced active download", err)
	}
	delete(a.active, "running")
	a.diagnosing = true
	if _, err = a.diagnoseDownload(context.Background(), "job", downloadProbeOptions{}); err == nil {
		t.Fatal("concurrent diagnostic allowed")
	}
	a.diagnosing = false
	a.update("job", func(j *Job) error { j.Status = "queued"; return nil })
	if _, err = a.diagnoseDownload(context.Background(), "job", downloadProbeOptions{}); err == nil {
		t.Fatal("diagnostic accepted queued task")
	}
}
