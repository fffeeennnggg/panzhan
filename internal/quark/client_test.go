package quark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseShare(t *testing.T) {
	s, err := ParseShare("分享：https://pan.quark.cn/s/Ab12?pwd=9xyZ#/list/share/0123456789abcdef0123456789abcdef-资料 提取码：9xyZ")
	if err != nil || s.ID != "Ab12" || s.Code != "9xyZ" || s.Parent != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = ParseShare("https://pan.quark.cn/s/abcd 提取码：1234")
	if err != nil || s.Code != "1234" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{"https://pan.quark.cn.evil/s/abc", "https://elsewhere/s/a", "https://pan.quark.cn/s/"} {
		if _, err := ParseShare(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestShareNestedBareFolderIDs(t *testing.T) {
	s, err := ParseShare("https://pan.quark.cn/s/abc#/list/share/11111111111111111111111111111111/22222222222222222222222222222222")
	if err != nil || s.Parent != "22222222222222222222222222222222" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestProtocolAndDownloadCookieSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pr") != "ucpro" {
			t.Error("missing pr")
		}
		switch r.URL.Path {
		case "/share/sharepage/token":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["passcode"] != "1234" {
				t.Error(b)
			}
			w.Write([]byte(`{"status":200,"code":0,"data":{"stoken":"share-token"}}`))
		case "/share/sharepage/detail":
			if r.URL.Query().Get("_page") == "1" {
				w.Write([]byte(`{"status":200,"code":0,"data":{"list":[{"fid":"s1","file_name":"课程","file":false,"share_fid_token":"ft"}]},"metadata":{"_total":1}}`))
			} else {
				t.Error("unnecessary page")
			}
		case "/share/sharepage/save":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if b["to_pdir_fid"] != "dest" {
				t.Error(b)
			}
			w.Write([]byte(`{"status":200,"code":0,"data":{"task_id":"task1"}}`))
		case "/task":
			w.Write([]byte(`{"status":200,"code":0,"data":{"status":2,"save_as":{"save_as_top_fids":["owned1"]}}}`))
		case "/file/download":
			if r.Header.Get("Cookie") != "__puus=old" {
				t.Error("wrong snapshot")
			}
			http.SetCookie(w, &http.Cookie{Name: "__puus", Value: "new"})
			w.Write([]byte(`{"status":200,"code":0,"data":[{"download_url":"https://cdn.example/file"}]}`))
		default:
			t.Error("unexpected", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := NewAt(srv.URL, "__puus=old", nil)
	ctx := context.Background()
	s := Share{ID: "abcd", Code: "1234", Parent: "0"}
	token, err := c.Token(ctx, s)
	if err != nil || token != "share-token" {
		t.Fatal(token, err)
	}
	fs, err := c.ShareFiles(ctx, s, token)
	if err != nil || len(fs) != 1 {
		t.Fatal(fs, err)
	}
	id, err := c.StartSave(ctx, s, token, fs, "dest")
	if err != nil || id != "task1" {
		t.Fatal(id, err)
	}
	done, fids, err := c.SaveStatus(ctx, id)
	if err != nil || !done || len(fids) != 1 || fids[0] != "owned1" {
		t.Fatal(done, fids, err)
	}
	link, err := c.DownloadLink(ctx, "owned1")
	if err != nil {
		t.Fatal(err)
	}
	if link.Header.Get("Cookie") != "__puus=old" || !strings.Contains(c.Cookie(), "__puus=new") || link.Header.Get("User-Agent") == "" {
		t.Fatal("cookie mismatch", link.Header)
	}
}

func TestRejectProviderErrorEvenWithHTTP200(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":401,"code":41001,"message":"secret=should-not-leak"}`))
	}))
	defer s.Close()
	_, err := NewAt(s.URL, "cookie", nil).Token(context.Background(), Share{ID: "a"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}

func TestMissingShare404ExplainsThatShareMayBeUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := NewAt(srv.URL, "cookie", nil).Token(context.Background(), Share{ID: "missing"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "分享可能") {
		t.Fatalf("expected a share availability hint, got %v", err)
	}
}
