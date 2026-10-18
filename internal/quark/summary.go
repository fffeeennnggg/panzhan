// SPDX-License-Identifier: AGPL-3.0-or-later
package quark

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
)

// ContentSummary counts actual files; directory size fields are never summed.
// Complete=false means the public listing was only partially inspected.
type ContentSummary struct {
	FileCount   int            `json:"file_count"`
	FolderCount int            `json:"folder_count"`
	TotalBytes  int64          `json:"total_bytes"`
	UnknownSize int            `json:"unknown_size"`
	Types       map[string]int `json:"types"`
	Extensions  []string       `json:"extensions,omitempty"`
	Complete    bool           `json:"complete"`
}

type summaryFile struct {
	ID       string `json:"fid"`
	Name     string `json:"file_name"`
	Size     *int64 `json:"size"`
	IsFile   bool   `json:"file"`
	Category string `json:"obj_category"`
}

func contentType(f summaryFile) string {
	ext := strings.ToLower(path.Ext(f.Name))
	for kind, extensions := range map[string]string{
		"video":       ".mp4 .mkv .avi .mov .wmv .flv .m4v .ts .m2ts .webm .rmvb .mpeg .mpg",
		"audio":       ".mp3 .flac .wav .aac .m4a .ogg .ape .wma .alac .opus",
		"image":       ".jpg .jpeg .png .gif .webp .bmp .svg .heic .tiff .avif",
		"document":    ".pdf .txt .doc .docx .xls .xlsx .ppt .pptx .epub .mobi .md .azw3 .csv",
		"archive":     ".zip .rar .7z .tar .gz .bz2 .xz .iso",
		"application": ".exe .msi .apk .dmg .pkg .deb .rpm .appimage",
		"subtitle":    ".srt .ass .ssa .vtt .sub",
	} {
		for _, candidate := range strings.Fields(extensions) {
			if ext == candidate {
				return kind
			}
		}
	}
	switch strings.ToLower(f.Category) {
	case "video", "audio", "image", "document":
		return strings.ToLower(f.Category)
	}
	return "other"
}

func (c *ShareChecker) summarize(ctx context.Context, q url.Values, first []summaryFile, total int) *ContentSummary {
	const maxPages = 6
	summary := &ContentSummary{Types: map[string]int{}, Complete: true}
	type pageTask struct {
		folder string
		page   int
	}
	queue := []pageTask{}
	seenFolders := map[string]bool{q.Get("pdir_fid"): true}
	seenFiles := map[string]bool{}
	extensions := map[string]bool{}
	consume := func(files []summaryFile) {
		for _, f := range files {
			if !f.IsFile {
				if f.ID == "" {
					summary.Complete = false
					continue
				}
				if !seenFolders[f.ID] {
					seenFolders[f.ID] = true
					summary.FolderCount++
					queue = append(queue, pageTask{f.ID, 1})
				}
				continue
			}
			if f.ID != "" && seenFiles[f.ID] {
				continue
			}
			if f.ID != "" {
				seenFiles[f.ID] = true
			}
			summary.FileCount++
			summary.Types[contentType(f)]++
			if f.Size == nil || *f.Size < 0 {
				summary.UnknownSize++
			} else {
				summary.TotalBytes += *f.Size
			}
			if ext := strings.TrimPrefix(strings.ToLower(path.Ext(f.Name)), "."); ext != "" && len(ext) <= 12 {
				extensions[ext] = true
			}
		}
	}
	consume(first)
	if total > len(first) && len(first) < 100 {
		summary.Complete = false
	}
	if len(first) >= 100 && (total == 0 || total > len(first)) {
		queue = append(queue, pageTask{q.Get("pdir_fid"), 2})
	}
	pages := 1
	for len(queue) > 0 && pages < maxPages {
		task := queue[0]
		queue = queue[1:]
		params := url.Values{}
		for k, v := range q {
			params[k] = append([]string{}, v...)
		}
		params.Set("pdir_fid", task.folder)
		params.Set("_page", strconv.Itoa(task.page))
		e, ok := c.request(ctx, "GET", c.detailBase+"/share/sharepage/detail?"+params.Encode(), nil)
		pages++
		if !ok || e.Code != 0 || (e.Status != 0 && e.Status != 200) {
			summary.Complete = false
			continue
		}
		var detail struct {
			List []summaryFile `json:"list"`
		}
		if json.Unmarshal(e.Data, &detail) != nil {
			summary.Complete = false
			continue
		}
		consume(detail.List)
		if e.Metadata.Total > (task.page-1)*100+len(detail.List) && len(detail.List) < 100 {
			summary.Complete = false
		}
		if len(detail.List) >= 100 && (e.Metadata.Total == 0 || task.page*100 < e.Metadata.Total) {
			queue = append(queue, pageTask{task.folder, task.page + 1})
		}
		if ctx.Err() != nil {
			summary.Complete = false
			break
		}
	}
	if len(queue) > 0 {
		summary.Complete = false
	}
	for ext := range extensions {
		summary.Extensions = append(summary.Extensions, ext)
	}
	sort.Strings(summary.Extensions)
	return summary
}
