package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"panzhan/internal/download"
	"panzhan/internal/quark"
)

func (a *App) job(id string) (Job, error) {
	st := a.snapshot()
	for _, j := range st.Jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return Job{}, errors.New("任务不存在")
}
func (a *App) update(id string, fn func(*Job) error) error {
	return a.change(func(st *State) error {
		for i := range st.Jobs {
			if st.Jobs[i].ID == id {
				return fn(&st.Jobs[i])
			}
		}
		return errors.New("任务不存在")
	})
}
func (a *App) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}
func (a *App) preview(ctx context.Context, raw, code string) (quark.Share, []quark.File, error) {
	s, err := quark.ParseShare(raw)
	if err != nil {
		return s, nil, err
	}
	if code != "" {
		s.Code = code
	}
	c, err := a.provider()
	if err != nil {
		return s, nil, err
	}
	token, err := c.Token(ctx, s)
	if err != nil {
		return s, nil, err
	}
	fs, err := c.ShareFiles(ctx, s, token)
	return s, fs, err
}
func (a *App) createJob(ctx context.Context, raw, title, dir, code string) (Job, error) {
	r, err := a.directory(dir)
	if err != nil {
		return Job{}, fmt.Errorf("保存目录不可访问：%w", err)
	}
	r.Close()
	s, files, err := a.preview(ctx, raw, code)
	if err != nil {
		return Job{}, err
	}
	if len(files) == 0 {
		return Job{}, errors.New("分享目录为空")
	}
	id := randomID()
	if title == "" {
		title = files[0].Name
		if len(files) > 1 {
			title += " 等资源"
		}
	}
	title = safeTitle(title)
	// Each task owns a distinct local/cloud directory; concurrent tasks cannot collide.
	j := Job{ID: id, Title: title, Share: s, Directory: path.Join(dir, title+"-"+id[:8]), Status: "queued", Created: time.Now().Format(time.RFC3339), Original: files, Files: []SavedFile{}, Batches: []Batch{}}
	err = a.change(func(st *State) error { st.Jobs = append([]Job{j}, st.Jobs...); return nil })
	if err == nil {
		a.signal()
	}
	return j, err
}
func (a *App) Run(ctx context.Context) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return
	}
	a.running = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.running = false; a.mu.Unlock() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		var next string
		a.mu.Lock()
		for _, j := range a.state.Jobs {
			if j.Status == "queued" {
				next = j.ID
				break
			}
		}
		var child context.Context
		var cancel context.CancelFunc
		if next != "" {
			child, cancel = context.WithCancel(ctx)
			a.active[next] = cancel
		}
		a.mu.Unlock()
		if next != "" {
			err := a.runJob(child, next)
			wasCancelled := child.Err() != nil
			cancel()
			if err != nil && !wasCancelled {
				_ = a.update(next, func(j *Job) error {
					if j.Status != "paused" && j.Status != "cancelled" {
						j.Status = "attention"
						j.Error = err.Error()
					}
					return nil
				})
			}
			a.mu.Lock()
			delete(a.active, next)
			a.mu.Unlock()
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-a.wake:
		}
	}
}
func (a *App) runJob(ctx context.Context, id string) error {
	c, err := a.provider()
	if err != nil {
		return err
	}
	j, err := a.job(id)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	stage := func(status string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return a.update(id, func(j *Job) error {
			if j.Status == "paused" || j.Status == "cancelled" {
				return context.Canceled
			}
			j.Status = status
			j.Error = ""
			return nil
		})
	}
	if err = stage("transferring"); err != nil {
		return err
	}
	if j.CloudDir == "" {
		parent, er := c.EnsureFolder(ctx, "0", "盘栈转存")
		if er != nil {
			return er
		}
		dest, er := c.EnsureFolder(ctx, parent, j.ID)
		if er != nil {
			return er
		}
		if er = a.update(id, func(j *Job) error { j.CloudDir = dest; return nil }); er != nil {
			return er
		}
	}
	j, _ = a.job(id)
	if len(j.Batches) == 0 {
		err = a.update(id, func(j *Job) error {
			for offset := 0; offset < len(j.Original); offset += 100 {
				j.Batches = append(j.Batches, Batch{Offset: offset, Count: min(100, len(j.Original)-offset)})
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	j, _ = a.job(id)
	for bi := range j.Batches {
		current, _ := a.job(id)
		b := current.Batches[bi]
		if b.Done {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if b.Intent && b.TaskID == "" {
			_ = a.update(id, func(j *Job) error { j.Uncertain = true; return nil })
			return errors.New("转存响应不确定，请在任务详情核对云端批次后继续；不会自动重复转存")
		}
		if b.Dest == "" {
			dest, er := c.EnsureFolder(ctx, current.CloudDir, fmt.Sprintf("batch-%03d", bi+1))
			if er != nil {
				return er
			}
			if er = a.update(id, func(j *Job) error { j.Batches[bi].Dest = dest; return nil }); er != nil {
				return er
			}
			b.Dest = dest
		}
		if b.TaskID == "" {
			token, er := c.Token(ctx, current.Share)
			if er != nil {
				return er
			}
			latest, er := c.ShareFiles(ctx, current.Share, token)
			if er != nil {
				return er
			}
			byID := map[string]quark.File{}
			for _, f := range latest {
				byID[f.ID] = f
			}
			batch := []quark.File{}
			for _, old := range current.Original[b.Offset : b.Offset+b.Count] {
				f, ok := byID[old.ID]
				if !ok {
					return errors.New("分享内容已变化，请重新解析后创建任务")
				}
				batch = append(batch, f)
			}
			if er = a.update(id, func(j *Job) error { j.Batches[bi].Intent = true; return nil }); er != nil {
				return er
			}
			taskID, er := c.StartSave(ctx, current.Share, token, batch, b.Dest)
			if er != nil {
				_ = a.update(id, func(j *Job) error { j.Uncertain = true; return nil })
				return errors.New("转存请求未确认，请在任务详情核对云端结果后继续")
			}
			if er = a.update(id, func(j *Job) error { j.Batches[bi].TaskID = taskID; return nil }); er != nil {
				return er
			}
			b.TaskID = taskID
		}
		pollCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		var ids []string
		for {
			done, result, er := c.SaveStatus(pollCtx, b.TaskID)
			if er != nil {
				cancel()
				return er
			}
			if done {
				ids = result
				break
			}
			select {
			case <-pollCtx.Done():
				cancel()
				return errors.New("转存仍未结束，请稍后重试以查询同一个任务")
			case <-time.After(time.Second):
			}
		}
		cancel()
		if len(ids) != b.Count {
			return errors.New("云端转存结果数量不一致，已保留 task_id，请核对后重试")
		}
		if err = a.update(id, func(j *Job) error {
			j.Batches[bi].Done = true
			j.Batches[bi].IDs = ids
			j.Uncertain = false
			return nil
		}); err != nil {
			return err
		}
	}
	j, _ = a.job(id)
	if !j.Enumerated {
		all := []SavedFile{}
		seen := map[string]bool{}
		visited := map[string]bool{}
		var walk func(string, string, int) error
		walk = func(parent, prefix string, depth int) error {
			if depth > 32 || visited[parent] {
				return errors.New("网盘目录过深或存在循环")
			}
			visited[parent] = true
			items, er := c.Files(ctx, parent)
			if er != nil {
				return er
			}
			for _, f := range items {
				if er = download.ValidatePath(f.Name); er != nil || strings.Contains(f.Name, "/") {
					return errors.New("分享含不安全文件名，请在网盘更名后重试")
				}
				rel := path.Join(prefix, f.Name)
				key := strings.ToLower(rel)
				if seen[key] {
					return errors.New("分享中有同名冲突文件，请在网盘整理后重试")
				}
				seen[key] = true
				if f.IsFile {
					all = append(all, SavedFile{ID: f.ID, Path: rel, Size: f.Size, MD5: f.MD5})
				} else if er = walk(f.ID, rel, depth+1); er != nil {
					return er
				}
				if len(all) > 100000 {
					return errors.New("文件数量超过当前任务上限")
				}
			}
			return nil
		}
		for _, b := range j.Batches {
			items, er := c.Files(ctx, b.Dest)
			if er != nil {
				return er
			}
			byID := map[string]quark.File{}
			for _, f := range items {
				byID[f.ID] = f
			}
			for _, fid := range b.IDs {
				f, ok := byID[fid]
				if !ok {
					return errors.New("转存文件 ID 已不在目标目录，不能继续下载")
				}
				if er = download.ValidatePath(f.Name); er != nil || strings.Contains(f.Name, "/") {
					return errors.New("转存文件名不安全")
				}
				key := strings.ToLower(f.Name)
				if seen[key] {
					return errors.New("顶层文件存在同名冲突")
				}
				seen[key] = true
				if f.IsFile {
					all = append(all, SavedFile{ID: f.ID, Path: f.Name, Size: f.Size, MD5: f.MD5})
				} else if er = walk(f.ID, f.Name, 0); er != nil {
					return er
				}
			}
		}
		if len(all) == 0 {
			return errors.New("转存后没有可下载文件，空目录暂不创建下载")
		}
		if err = a.update(id, func(j *Job) error { j.Files = all; j.Enumerated = true; return nil }); err != nil {
			return err
		}
	}
	if err = stage("downloading"); err != nil {
		return err
	}
	j, _ = a.job(id)
	root, err := os.OpenRoot(a.rootPath)
	if err != nil {
		return err
	}
	if err = root.MkdirAll(j.Directory, 0750); err != nil {
		root.Close()
		return errors.New("NAS 目标目录不可写，请检查目录授权")
	}
	dest, err := root.OpenRoot(j.Directory)
	root.Close()
	if err != nil {
		return err
	}
	defer dest.Close()
	st := a.snapshot()
	engine := download.Engine{Concurrency: st.Settings.Concurrency, ChunkSize: int64(st.Settings.ChunkMiB) << 20}
	for i, item := range j.Files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if item.Done {
			f, er := dest.Open(item.Path)
			if er != nil {
				return errors.New("已完成的文件丢失，请检查 NAS 目录")
			}
			h := sha256.New()
			n, er := io.Copy(h, f)
			f.Close()
			if er != nil || n != item.Size || hex.EncodeToString(h.Sum(nil)) != item.SHA256 {
				return errors.New("已完成文件发生变化，已停止任务以避免覆盖")
			}
			continue
		}
		err = engine.Fetch(ctx, dest, item.Path, id+":"+item.ID, item.Size, item.MD5, func(ctx context.Context) (download.Link, error) {
			link, er := c.DownloadLink(ctx, item.ID)
			return download.Link{URL: link.URL, Header: link.Header}, er
		}, func(n int64) {
			a.mu.Lock()
			for ji := range a.state.Jobs {
				job := &a.state.Jobs[ji]
				if job.ID == id && i < len(job.Files) {
					job.Files[i].Downloaded = n
				}
			}
			a.mu.Unlock()
		})
		if err != nil {
			return err
		}
		if err = stage("verifying"); err != nil {
			return err
		}
		f, er := dest.Open(item.Path)
		if er != nil {
			return er
		}
		h := sha256.New()
		_, er = io.Copy(h, f)
		f.Close()
		if er != nil {
			return er
		}
		digest := hex.EncodeToString(h.Sum(nil))
		if err = a.update(id, func(j *Job) error {
			j.Files[i].Done = true
			j.Files[i].Downloaded = item.Size
			j.Files[i].SHA256 = digest
			return nil
		}); err != nil {
			return err
		}
		_ = dest.Remove(item.Path + ".pz.receipt")
		if err = stage("downloading"); err != nil {
			return err
		}
	}
	return stage("completed")
}
func (a *App) action(ctx context.Context, id, action, dir string) error {
	j, err := a.job(id)
	if err != nil {
		return err
	}
	if action == "reconcile" || action == "resubmit" {
		if (j.Status != "attention" && j.Status != "paused") || !j.Uncertain {
			return errors.New("此任务不需要核对转存")
		}
		c, er := a.provider()
		if er != nil {
			return er
		}
		for bi, b := range j.Batches {
			if !b.Intent || b.TaskID != "" || b.Done {
				continue
			}
			fs, er := c.Files(ctx, b.Dest)
			if er != nil {
				return er
			}
			if action == "resubmit" {
				if len(fs) != 0 {
					return errors.New("批次目录不是空目录，不能重新转存")
				}
				if er = a.update(id, func(j *Job) error { j.Batches[bi].Intent = false; return nil }); er != nil {
					return er
				}
				continue
			}
			if len(fs) != b.Count {
				return errors.New("云端文件数与原批次不一致，不能确认转存完成")
			}
			byName := map[string]quark.File{}
			for _, f := range fs {
				if _, ok := byName[f.Name]; ok {
					return errors.New("云端存在同名冲突")
				}
				byName[f.Name] = f
			}
			ids := []string{}
			for _, old := range j.Original[b.Offset : b.Offset+b.Count] {
				f, ok := byName[old.Name]
				if !ok || f.IsFile != old.IsFile || (f.IsFile && f.Size != old.Size) {
					return errors.New("云端文件名、类型或大小不匹配，需手动核对")
				}
				ids = append(ids, f.ID)
			}
			if er = a.update(id, func(j *Job) error { j.Batches[bi].IDs = ids; j.Batches[bi].Done = true; return nil }); er != nil {
				return er
			}
		}
		if er = a.update(id, func(j *Job) error { j.Uncertain = false; j.Error = ""; j.Status = "queued"; return nil }); er != nil {
			return er
		}
		a.signal()
		return nil
	}
	if dir != "" {
		r, er := a.directory(dir)
		if er != nil {
			return er
		}
		r.Close()
		for _, f := range j.Files {
			if f.Downloaded > 0 || f.Done {
				return errors.New("任务已写入文件，不能直接换目录；请保留原任务并新建")
			}
		}
	}
	err = a.update(id, func(job *Job) error {
		switch action {
		case "pause":
			if job.Status != "queued" && job.Status != "transferring" && job.Status != "downloading" && job.Status != "verifying" {
				return errors.New("当前任务不能暂停")
			}
			job.Status = "paused"
		case "cancel":
			if job.Status == "completed" {
				return errors.New("已完成任务不需要取消")
			}
			job.Status = "cancelled"
		case "resume", "retry":
			if job.Status != "paused" && job.Status != "attention" {
				return errors.New("当前任务不需要恢复")
			}
			if job.Uncertain {
				return errors.New("请先核对不确定的转存结果")
			}
			job.Status = "queued"
			job.Error = ""
			if dir != "" {
				job.Directory = path.Join(dir, safeTitle(job.Title)+"-"+job.ID[:8])
			}
		default:
			return errors.New("未知任务操作")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if action == "pause" || action == "cancel" {
		a.mu.Lock()
		if cancel := a.active[id]; cancel != nil {
			cancel()
		}
		a.mu.Unlock()
	}
	a.signal()
	return nil
}
