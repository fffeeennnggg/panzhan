package quark

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShareSummaryTraversesFoldersAndUsesFileSizesOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			fmt.Fprint(w, `{"data":{"stoken":"secret"}}`)
			return
		}
		switch r.URL.Query().Get("pdir_fid") {
		case "0":
			fmt.Fprint(w, `{"data":{"list":[{"fid":"folder","file_name":"电影","file":false,"size":999999},{"fid":"readme","file_name":"README.PDF","file":true,"size":100}]},"metadata":{"_total":2}}`)
		case "folder":
			fmt.Fprint(w, `{"data":{"list":[{"fid":"v","file_name":"episode.MKV","file":true,"size":2000},{"fid":"s","file_name":"episode.srt","file":true,"size":30},{"fid":"u","file_name":"notes.xyz","file":true}]},"metadata":{"_total":3}}`)
		default:
			t.Errorf("unexpected folder %q", r.URL.Query().Get("pdir_fid"))
		}
	}))
	defer srv.Close()
	got := newShareCheckerAt(srv.URL).Check(context.Background(), "https://pan.quark.cn/s/abc")
	s := got.Content
	if got.Availability != "accessible" || s == nil || !s.Complete || s.FileCount != 4 || s.FolderCount != 1 || s.TotalBytes != 2130 || s.UnknownSize != 1 {
		t.Fatalf("bad summary: %+v", s)
	}
	if s.Types["video"] != 1 || s.Types["document"] != 1 || s.Types["subtitle"] != 1 || s.Types["other"] != 1 {
		t.Fatal(s.Types)
	}
}

func TestShareSummaryPaginatesAndBoundsLargeDirectories(t *testing.T) {
	for _, total := range []int{101, 1000} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			pages := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					fmt.Fprint(w, `{"data":{"stoken":"secret"}}`)
					return
				}
				pages++
				count := 100
				if total == 101 && pages == 2 {
					count = 1
				}
				var files []string
				for i := 0; i < count; i++ {
					files = append(files, fmt.Sprintf(`{"fid":"p%df%d","file_name":"%d.mp4","file":true,"size":1024}`, pages, i, i))
				}
				fmt.Fprintf(w, `{"data":{"list":[%s]},"metadata":{"_total":%d}}`, strings.Join(files, ","), total)
			}))
			defer srv.Close()
			got := newShareCheckerAt(srv.URL).Check(context.Background(), "https://pan.quark.cn/s/paged")
			if got.Content == nil {
				t.Fatal(got)
			}
			if total == 101 && (!got.Content.Complete || got.Content.FileCount != 101 || got.Content.TotalBytes != 101*1024 || pages != 2) {
				t.Fatal(got.Content, pages)
			}
			if total == 1000 && (got.Content.Complete || got.Content.FileCount != 600 || pages != 6) {
				t.Fatal(got.Content, pages)
			}
		})
	}
}

func TestFolderReadFailurePreservesAccessibleShareWithPartialSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			fmt.Fprint(w, `{"data":{"stoken":"secret"}}`)
			return
		}
		if r.URL.Query().Get("pdir_fid") == "0" {
			fmt.Fprint(w, `{"data":{"list":[{"fid":"folder","file":false,"size":999}]}}`)
			return
		}
		w.WriteHeader(429)
	}))
	defer srv.Close()
	got := newShareCheckerAt(srv.URL).Check(context.Background(), "https://pan.quark.cn/s/abc")
	if got.Availability != "accessible" || got.Content == nil || got.Content.Complete || got.Content.TotalBytes != 0 {
		t.Fatal(got)
	}
}
