// Package attachments downloads Discord CDN content (message attachments
// and custom emoji images) into a local directory. It is the Go translation
// of utils/attachments.py.
package attachments

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// CustomEmojiPattern matches Discord's custom-emoji syntax in message text:
//
//	<:name:id>   (static)
//	<a:name:id>  (animated)
var CustomEmojiPattern = regexp.MustCompile(`<(a?):(\w+):(\d+)>`)

// Downloader is a thin wrapper around an HTTP client and the two output
// directories. Construct once at startup and reuse — *http.Client is
// safe for concurrent use.
type Downloader struct {
	AttachmentsDir string
	EmojisDir      string
	HTTP           *http.Client
}

func New(attachmentsDir, emojisDir string) *Downloader {
	return &Downloader{
		AttachmentsDir: attachmentsDir,
		EmojisDir:      emojisDir,
		HTTP:           &http.Client{Timeout: 30 * time.Second},
	}
}

// DownloadAttachment fetches url into <AttachmentsDir>/<channelID>/<messageID>_<filename>
// and returns the relative path (forward slashes, suitable for storing in
// the messages.attachments JSON). On any failure it returns "" — Python
// returns None and the caller stores no local_path; we mirror that.
func (d *Downloader) DownloadAttachment(
	ctx context.Context, url, channelID, messageID, filename string,
) string {
	rel := fmt.Sprintf("%s/%s_%s", channelID, messageID, filename)
	abs := filepath.Join(d.AttachmentsDir, channelID, fmt.Sprintf("%s_%s", messageID, filename))
	if err := d.fetchTo(ctx, url, abs); err != nil {
		return ""
	}
	return rel
}

// DownloadEmojis scans content for custom emoji refs and fetches any not
// already present on disk. Errors are swallowed (best effort) — emoji
// download is purely a UI improvement, not authoritative state.
func (d *Downloader) DownloadEmojis(ctx context.Context, content string) {
	for _, m := range CustomEmojiPattern.FindAllStringSubmatch(content, -1) {
		animated, _, emojiID := m[1], m[2], m[3]
		ext := "png"
		if animated == "a" {
			ext = "gif"
		}
		dest := filepath.Join(d.EmojisDir, fmt.Sprintf("%s.%s", emojiID, ext))
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		url := fmt.Sprintf("https://cdn.discordapp.com/emojis/%s.%s", emojiID, ext)
		_ = d.fetchTo(ctx, url, dest)
	}
}

func (d *Downloader) fetchTo(ctx context.Context, url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}
