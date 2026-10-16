// SPDX-License-Identifier: AGPL-3.0-or-later
// Share protocol ported with reference to Cp0204/quark-auto-save; see THIRD_PARTY_NOTICES.md.
package quark

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

type Share struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Parent string `json:"parent"`
	URL    string `json:"url"`
}

var shareURL = regexp.MustCompile(`https?://pan\.quark\.cn/s/[A-Za-z0-9]+(?:[^\s<>"，。]*)?`)
var codePattern = regexp.MustCompile(`(?:提取码|提取碼|密码|密碼)\s*[:：]?\s*([A-Za-z0-9]{4})`)
var folderPattern = regexp.MustCompile(`^([A-Za-z0-9]{32})(?:-|$)`)

func ParseShare(text string) (Share, error) {
	raw := shareURL.FindString(strings.TrimSpace(text))
	if raw == "" {
		return Share{}, errors.New("请输入有效的夸克分享链接")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != "pan.quark.cn" || u.User != nil {
		return Share{}, errors.New("无效的夸克分享链接")
	}
	id := strings.TrimPrefix(u.Path, "/s/")
	if strings.Contains(id, "/") {
		id = strings.Split(id, "/")[0]
	}
	s := Share{ID: id, Code: u.Query().Get("pwd"), Parent: "0", URL: raw}
	if s.Code == "" {
		if m := codePattern.FindStringSubmatch(text); len(m) > 1 {
			s.Code = m[1]
		}
	}
	for _, segment := range strings.Split(u.Fragment, "/") {
		if p := folderPattern.FindStringSubmatch(segment); len(p) > 1 {
			s.Parent = p[1]
		}
	}
	return s, nil
}
