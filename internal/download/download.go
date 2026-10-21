// SPDX-License-Identifier: AGPL-3.0-or-later
// Range/concurrency approach informed by AList; persistence and chunk verification are native to this app.
package download

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Link struct {
	URL    string
	Header http.Header
}

// Only transient transport/body failures are retried. Invalid ranges, auth
// failures and disk errors must still fail without writing unverified data.
type retryableTransferError struct{ error }

// Expired CDN credentials require resolving a new link at the task level,
// rather than repeatedly requesting the same link inside retryTransfer.
type refreshLinkError struct{ error }

func IsRetryable(err error) bool {
	var transient *retryableTransferError
	var expired *refreshLinkError
	return errors.As(err, &transient) || errors.As(err, &expired)
}

func retryTransfer(ctx context.Context, operation func() error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		var transient *retryableTransferError
		if err == nil || !errors.As(err, &transient) || attempt >= 2 {
			return err
		}
		timer := time.NewTimer((250 * time.Millisecond) << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type Engine struct {
	HTTP        *http.Client
	ChunkSize   int64
	Concurrency int
	Observe     func(Progress)
}
type journal struct {
	Identity string         `json:"identity"`
	Size     int64          `json:"size"`
	Chunk    int64          `json:"chunk"`
	Parts    map[int]string `json:"parts"`
}
type receipt struct {
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

func verifyReceipt(root *os.Root, rel, identity string, size int64) error {
	b, err := root.ReadFile(rel + ".pz.receipt")
	if err != nil {
		return errors.New("目标文件已存在且没有本任务的发布记录，不会覆盖")
	}
	var rec receipt
	if json.Unmarshal(b, &rec) != nil || rec.Identity != identity || rec.Size != size {
		return errors.New("目标文件发布记录不匹配，不会覆盖")
	}
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != rec.SHA256 {
		return errors.New("已发布文件发生变化，已停止以避免覆盖")
	}
	return nil
}

func ValidatePath(p string) error {
	if p == "" || !filepath.IsLocal(p) || strings.ContainsAny(p, "\\:\x00") || strings.HasPrefix(p, "/") {
		return errors.New("文件名或路径不安全")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part || strings.HasSuffix(part, ".") {
			return errors.New("文件名包含无效路径部分")
		}
		for _, suffix := range []string{".pz.part", ".pz.json", ".pz.json.tmp", ".pz.receipt"} {
			if strings.HasSuffix(strings.ToLower(part), suffix) {
				return errors.New("文件名占用下载恢复记录的保留后缀，请在网盘更名后重试")
			}
		}
	}
	return nil
}
func saveJournal(root *os.Root, path string, j journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	f, err := root.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = root.Rename(path+".tmp", path)
	}
	return err
}
func (e *Engine) Fetch(ctx context.Context, root *os.Root, rel, identity string, size int64, expectedMD5 string, resolve func(context.Context) (Link, error), progress func(int64)) error {
	if err := ValidatePath(rel); err != nil {
		return err
	}
	if size < 0 {
		return errors.New("文件大小无效")
	}
	if _, err := root.Stat(rel); err == nil {
		return verifyReceipt(root, rel, identity, size)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(filepath.FromSlash(rel)), 0750); err != nil {
		return err
	}
	chunk := e.ChunkSize
	if chunk <= 0 {
		chunk = 10 << 20
	}
	workers := e.Concurrency
	if workers < 1 {
		workers = 3
	}
	if workers > 8 {
		workers = 8
	}
	temp, meta := rel+".pz.part", rel+".pz.json"
	j := journal{Identity: identity, Size: size, Chunk: chunk, Parts: map[int]string{}}
	if b, err := root.ReadFile(meta); err == nil {
		var prev journal
		if json.Unmarshal(b, &prev) != nil || prev.Identity != identity || prev.Size != size || prev.Chunk <= 0 || prev.Chunk > 64<<20 {
			return errors.New("续传记录与当前文件不匹配，请保留记录并核对任务")
		}
		chunk = prev.Chunk
		j = prev
		if j.Parts == nil {
			j.Parts = map[int]string{}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	count := int((size + chunk - 1) / chunk)
	var done int64
	// Never trust file length or the bitmap alone after a crash or manual edit.
	for i, h := range j.Parts {
		if i < 0 || i >= count {
			delete(j.Parts, i)
			continue
		}
		n := min(chunk, size-int64(i)*chunk)
		buf := make([]byte, n)
		nr, er := f.ReadAt(buf, int64(i)*chunk)
		sum := sha256.Sum256(buf)
		if er != nil || int64(nr) != n || hex.EncodeToString(sum[:]) != h {
			delete(j.Parts, i)
		} else {
			done += n
		}
	}
	if progress != nil {
		progress(done)
	}
	tracker := newTransferTracker(size, done, e.Observe)
	if err = ctx.Err(); err != nil {
		return err
	}
	client := e.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	link, err := resolve(ctx)
	if err != nil {
		return err
	}
	u, err := url.Parse(link.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("无效下载地址")
	}
	// Check the first response headers before starting parallel Range readers.
	var pending []int
	for i := 0; i < count; i++ {
		if _, ok := j.Parts[i]; !ok {
			pending = append(pending, i)
		}
	}
	var mu sync.Mutex
	dirtyParts := 0
	checkpointLocked := func() error {
		if dirtyParts == 0 {
			return nil
		}
		// Flush file data before publishing the grouped recovery checkpoint.
		if er := f.Sync(); er != nil {
			return er
		}
		if er := saveJournal(root, meta, j); er != nil {
			return er
		}
		dirtyParts = 0
		return nil
	}
	writePart := func(i int, buf []byte) error {
		if _, er := f.WriteAt(buf, int64(i)*chunk); er != nil {
			return er
		}
		hash := sha256.Sum256(buf)
		mu.Lock()
		defer mu.Unlock()
		j.Parts[i] = hex.EncodeToString(hash[:])
		dirtyParts++
		done += int64(len(buf))
		tracker.commit(i, int64(len(buf)))
		// Group disk flushes instead of making every parallel reader wait for
		// its own file and journal sync. Resume still re-hashes all saved parts.
		if dirtyParts >= workers {
			if er := checkpointLocked(); er != nil {
				return er
			}
		}
		if progress != nil {
			progress(done)
		}
		return nil
	}
	request := func(c context.Context, i int) (*http.Response, error) {
		start := int64(i) * chunk
		end := min(size-1, start+chunk-1)
		req, er := http.NewRequestWithContext(c, "GET", link.URL, nil)
		if er != nil {
			return nil, errors.New("下载请求无效")
		}
		req.Header = link.Header.Clone()
		if req.Header == nil {
			req.Header = http.Header{}
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		req.Header.Set("Accept-Encoding", "identity")
		res, er := client.Do(req)
		if er != nil {
			if c.Err() != nil {
				return nil, c.Err()
			}
			return nil, &retryableTransferError{errors.New("下载网络异常，分片重试后仍未恢复，可重试以重新获取直链")}
		}
		return res, nil
	}
	readRange := func(res *http.Response, i int) error {
		defer res.Body.Close()
		start := int64(i) * chunk
		n := min(chunk, size-start)
		want := fmt.Sprintf("bytes %d-%d/%d", start, start+n-1, size)
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusGone {
			return &refreshLinkError{errors.New("下载直链已失效，将自动重新获取直链")}
		}
		if res.StatusCode == http.StatusRequestTimeout || res.StatusCode == http.StatusTooManyRequests || res.StatusCode == 500 || res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504 {
			return &retryableTransferError{fmt.Errorf("下载服务暂时不可用（HTTP %d）", res.StatusCode)}
		}
		if res.StatusCode != 206 || res.Header.Get("Content-Range") != want {
			return fmt.Errorf("分片响应不匹配（HTTP %d），未写入该分片", res.StatusCode)
		}
		tracker.begin()
		defer tracker.end()
		buf, er := io.ReadAll(io.LimitReader(progressReader{res.Body, tracker, i}, n+1))
		if er != nil || int64(len(buf)) != n {
			tracker.discard(i)
			failure := errors.New("分片长度不完整，可重试续传")
			if er != nil || int64(len(buf)) < n {
				return &retryableTransferError{failure}
			}
			return failure
		}
		return writePart(i, buf)
	}
	fetchRange := func(c context.Context, i int, initial *http.Response) error {
		defer func() {
			if initial != nil {
				initial.Body.Close()
			}
		}()
		return retryTransfer(c, func() error {
			res := initial
			initial = nil
			if res == nil {
				var er error
				res, er = request(c, i)
				if er != nil {
					return er
				}
			}
			return readRange(res, i)
		})
	}
	if len(pending) > 0 {
		child, cancel := context.WithCancel(ctx)
		defer cancel()
		first := pending[0]
		var res *http.Response
		er := retryTransfer(child, func() error {
			var requestErr error
			res, requestErr = request(child, first)
			if requestErr == nil && (res.StatusCode == 500 || res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504) {
				res.Body.Close()
				return &retryableTransferError{fmt.Errorf("下载服务暂时不可用（HTTP %d）", res.StatusCode)}
			}
			return requestErr
		})
		if er != nil {
			return er
		}
		if res.StatusCode == 200 {
			// No Range support: restart from byte zero with one sequential stream.
			if er = f.Truncate(0); er != nil {
				res.Body.Close()
				return er
			}
			if _, er = f.Seek(0, 0); er != nil {
				res.Body.Close()
				return er
			}
			j.Parts = map[int]string{}
			if er = saveJournal(root, meta, j); er != nil {
				res.Body.Close()
				return er
			}
			tracker.reset()
			if progress != nil {
				progress(0)
			}
			tracker.begin()
			n, copyErr := io.CopyBuffer(f, io.LimitReader(progressReader{res.Body, tracker, -1}, size+1), make([]byte, 256<<10))
			res.Body.Close()
			tracker.end()
			if copyErr != nil || n != size {
				tracker.discard(-1)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var diskError *os.PathError
				if errors.As(copyErr, &diskError) {
					return copyErr
				}
				if n < size {
					return &retryableTransferError{errors.New("完整下载中断，将自动重新下载；服务器不支持分片续传")}
				}
				return errors.New("完整下载中断或长度不匹配；该服务器不支持分片续传")
			}
			if er = f.Sync(); er != nil {
				return er
			}
			done = size
			tracker.commit(-1, size)
			if progress != nil {
				progress(done)
			}
		} else {
			start := int64(first) * chunk
			n := min(chunk, size-start)
			want := fmt.Sprintf("bytes %d-%d/%d", start, start+n-1, size)
			if res.StatusCode != http.StatusPartialContent || res.Header.Get("Content-Range") != want {
				return readRange(res, first)
			}
			pending = pending[1:]
			jobs := make(chan int)
			errCh := make(chan error, workers)
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				var initial *http.Response
				if w == 0 {
					initial = res
				}
				wg.Add(1)
				go func(initial *http.Response) {
					defer wg.Done()
					if initial != nil {
						if er := fetchRange(child, first, initial); er != nil {
							errCh <- er
							cancel()
							return
						}
					}
					for i := range jobs {
						if child.Err() != nil {
							return
						}
						er := fetchRange(child, i, nil)
						if er != nil {
							errCh <- er
							cancel()
							return
						}
					}
				}(initial)
			}
			go func() {
				defer close(jobs)
				for _, i := range pending {
					select {
					case jobs <- i:
					case <-child.Done():
						return
					}
				}
			}()
			wg.Wait()
			close(errCh)
			var workerErr error
			for er := range errCh {
				if er != nil && workerErr == nil {
					workerErr = er
				}
			}
			mu.Lock()
			checkpointErr := checkpointLocked()
			mu.Unlock()
			if workerErr != nil {
				return workerErr
			}
			if checkpointErr != nil {
				return checkpointErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	tracker.phase("verifying")
	if st, er := f.Stat(); er != nil || st.Size() != size {
		return errors.New("文件大小校验失败")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	hash := md5.New()
	strong := sha256.New()
	if _, err = io.Copy(io.MultiWriter(hash, strong), f); err != nil {
		return err
	}
	if expectedMD5 != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedMD5) {
		j.Parts = map[int]string{}
		_ = saveJournal(root, meta, j)
		return errors.New("文件 MD5 校验不一致，续传记录已重置")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	publication, _ := json.Marshal(receipt{identity, size, hex.EncodeToString(strong.Sum(nil))})
	rec, er := root.OpenFile(rel+".pz.receipt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if er != nil {
		return er
	}
	_, er = rec.Write(publication)
	if er == nil {
		er = rec.Sync()
	}
	ce := rec.Close()
	if er == nil {
		er = ce
	}
	if er != nil {
		return er
	}
	// Link is atomic and refuses to overwrite an existing destination, unlike Rename.
	if err = root.Link(temp, rel); err != nil {
		return fmt.Errorf("发布文件失败（目标可能已存在）：%w", err)
	}
	_ = root.Remove(temp)
	_ = root.Remove(meta)
	return nil
}

func Bytes(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
