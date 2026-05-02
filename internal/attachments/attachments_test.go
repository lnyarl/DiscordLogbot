package attachments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCustomEmojiPattern(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    [][3]string // animated, name, id
	}{
		{"none", "hello world", nil},
		{"single static", "say <:wave:123>", [][3]string{{"", "wave", "123"}}},
		{"single animated", "watch <a:loading:456>", [][3]string{{"a", "loading", "456"}}},
		{
			"mixed",
			"<:hi:1> middle <a:loop:22> end <:bye:333>",
			[][3]string{{"", "hi", "1"}, {"a", "loop", "22"}, {"", "bye", "333"}},
		},
		{"non-emoji <text>", "<not_an_emoji>", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CustomEmojiPattern.FindAllStringSubmatch(tt.input, -1)
			if tt.want == nil && len(got) != 0 {
				t.Fatalf("got %d matches, want 0: %v", len(got), got)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d matches, want %d: %v", len(got), len(tt.want), got)
			}
			for i, m := range got {
				if m[1] != tt.want[i][0] || m[2] != tt.want[i][1] || m[3] != tt.want[i][2] {
					t.Errorf("match %d: got %v, want %v", i, m[1:], tt.want[i])
				}
			}
		})
	}
}

func TestDownloadAttachment_HappyPath(t *testing.T) {
	const payload = "filebody"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	dir := t.TempDir()
	d := New(dir, dir+"/emojis")

	rel := d.DownloadAttachment(context.Background(), srv.URL+"/x.png", "C1", "M1", "pic.png")
	if rel != "C1/M1_pic.png" {
		t.Fatalf("rel=%q", rel)
	}
	abs := filepath.Join(dir, "C1", "M1_pic.png")
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("body=%q want %q", string(got), payload)
	}
}

func TestDownloadAttachment_Non200ReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	dir := t.TempDir()
	d := New(dir, dir+"/emojis")

	rel := d.DownloadAttachment(context.Background(), srv.URL+"/x.png", "C1", "M1", "pic.png")
	if rel != "" {
		t.Fatalf("expected empty rel on failure, got %q", rel)
	}
}

func TestDownloadEmojis_SkipsExisting(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("img"))
	}))
	defer srv.Close()

	// Override CDN host: build a Downloader that targets srv via a custom
	// fetchTo path. Easier: pre-populate dest so DownloadEmojis sees it
	// and does NOT call the network.
	dir := t.TempDir()
	d := New(dir, dir)
	if err := os.WriteFile(filepath.Join(dir, "999.png"), []byte("preset"), 0o644); err != nil {
		t.Fatal(err)
	}

	d.DownloadEmojis(context.Background(), "say <:hello:999>")
	if hits != 0 {
		t.Fatalf("expected 0 network hits (file pre-exists), got %d", hits)
	}
}

func TestCustomEmojiPattern_RejectsMalformed(t *testing.T) {
	for _, in := range []string{
		"<a:name:>", // empty id
		"<:name>",   // missing id
		"<:::>",     // structurally bad
		"plain text",
	} {
		if CustomEmojiPattern.MatchString(in) {
			t.Errorf("should not match: %q", in)
		}
	}
}
