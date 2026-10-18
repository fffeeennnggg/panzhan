package app

import (
	"encoding/json"
	"panzhan/internal/download"
	"strings"
	"testing"
	"time"
)

func TestTransferSpeedETAPauseAndStall(t *testing.T) {
	start := time.Now()
	tk := &transferState{started: start, fileIndex: 1, samples: []transferSample{{at: start}}, current: TaskProgress{CurrentFile: "file.bin"}}
	job := Job{Status: "downloading", Files: []SavedFile{{Size: 100, Done: true}, {Size: 1000}}}
	tk.observe(download.Progress{Downloaded: 600, Received: 200, Connections: 2, Phase: "downloading"}, start.Add(2*time.Second))
	p := tk.view(job, start.Add(2*time.Second))
	if p.SpeedBPS != 100 || p.DownloadedBytes != 700 || p.TotalBytes != 1100 || p.ETASeconds == nil || *p.ETASeconds != 4 || p.Connections != 2 {
		t.Fatal(p)
	}
	if stale := tk.view(job, start.Add(7*time.Second)); stale.SpeedBPS != 0 || stale.ETASeconds != nil {
		t.Fatal("stale speed", stale)
	}
	job.Status = "paused"
	if paused := tk.view(job, start.Add(2*time.Second)); paused.SpeedBPS != 0 || paused.ETASeconds != nil {
		t.Fatal(paused)
	}
	job.Status = "downloading"
	tk.current.Phase = "verifying"
	if verifying := tk.view(job, start.Add(2*time.Second)); verifying.SpeedBPS != 0 || verifying.ETASeconds != nil {
		t.Fatal(verifying)
	}
}

func TestTaskTelemetryIsNotStoredAndExcludesPausedJobs(t *testing.T) {
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.state.Jobs = []Job{{ID: "fixture", Status: "downloading", Files: []SavedFile{{Size: 1000}}}}
	a.beginTransfer("fixture")
	a.beginTransferFile("fixture", 0, a.state.Jobs[0].Files[0])
	a.observeTransfer("fixture", download.Progress{Downloaded: 100, Received: 100, Phase: "downloading"})
	views := a.withTransferProgress(a.snapshot().Jobs)
	if views[0].Progress == nil || views[0].Progress.DownloadedBytes != 100 {
		t.Fatal(views)
	}
	stateJSON, _ := json.Marshal(a.snapshot())
	if strings.Contains(string(stateJSON), `"progress"`) {
		t.Fatal("live metrics leaked into recovery state")
	}
	views[0].Status = "paused"
	if a.withTransferProgress(views)[0].Progress != nil {
		t.Fatal("paused job kept live metrics")
	}
	a.endTransfer("fixture")
	if a.withTransferProgress(a.snapshot().Jobs)[0].Progress != nil {
		t.Fatal("finished transfer kept live metrics")
	}
}
