package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestTemplates_LoginRender(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var b bytes.Buffer
	if err := tpl.RenderTo(&b, "login.html", PageData{BotInviteURL: "https://example/invite"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := b.String()

	// Title block was overridden.
	if !strings.Contains(out, "<title>로그인 — Discord 사관</title>") {
		t.Errorf("title block did not override base; got %q", titleSlice(out))
	}
	// Login button is present.
	if !strings.Contains(out, "Discord로 로그인") {
		t.Error("login button text missing")
	}
	// Bot invite URL got injected.
	if !strings.Contains(out, "https://example/invite") {
		t.Error("bot_invite_url not injected")
	}
	// Username section is hidden when blank.
	if strings.Contains(out, "user-info") {
		t.Error("user-info should not render without Username")
	}
}

func TestTemplates_SearchRender(t *testing.T) {
	tpl, _ := LoadTemplates()
	var b bytes.Buffer
	if err := tpl.RenderTo(&b, "search.html", PageData{Username: "alice"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "<title>검색 — Discord 사관</title>") {
		t.Errorf("title not set; got %q", titleSlice(out))
	}
	if !strings.Contains(out, "alice") {
		t.Error("username not rendered")
	}
	// User info section appears.
	if !strings.Contains(out, "user-info") {
		t.Error("user-info section missing for signed-in user")
	}
	// Search UI elements present.
	for _, frag := range []string{"id=\"query\"", "id=\"filter-guild\"", "id=\"results\""} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment %q", frag)
		}
	}
}

func TestTemplates_AutoEscapesUsername(t *testing.T) {
	tpl, _ := LoadTemplates()
	var b bytes.Buffer
	_ = tpl.RenderTo(&b, "search.html", PageData{Username: "<script>x</script>"})
	out := b.String()
	if strings.Contains(out, "<script>x</script>") {
		t.Error("auto-escape failed: script tag emitted as-is")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("auto-escape did not produce expected entities")
	}
}

func titleSlice(s string) string {
	i := strings.Index(s, "<title>")
	j := strings.Index(s, "</title>")
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return s[i : j+len("</title>")]
}
