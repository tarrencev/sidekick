package main

import (
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxArtifactBytes = 512 << 20

// publishArtifact snapshots src (a file or a static-site directory) into dst so
// later edits by the agent don't change what the user is reviewing. It returns
// the entry path to open, relative to dst.
func publishArtifact(src, dst string) (string, error) {
	if !filepath.IsAbs(src) {
		return "", errors.New("path must be absolute")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	if !info.IsDir() {
		name := filepath.Base(src)
		if _, err := copyFile(src, filepath.Join(dst, name)); err != nil {
			return "", err
		}
		// A lone image, video or audio file gets a minimal viewer page so it
		// opens centered on a dark page instead of as a bare file.
		if page := mediaPage(name); page != "" {
			if err := os.WriteFile(filepath.Join(dst, "index.html"), []byte(page), 0o644); err != nil {
				return "", err
			}
			return "", nil
		}
		return name, nil
	}

	var total int64
	var pages []string
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() { // skip symlinks and devices
			return nil
		}
		n, err := copyFile(p, filepath.Join(dst, rel))
		if err != nil {
			return err
		}
		if total += n; total > maxArtifactBytes {
			return fmt.Errorf("artifact exceeds %d MB", maxArtifactBytes>>20)
		}
		if strings.HasSuffix(strings.ToLower(rel), ".html") {
			pages = append(pages, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	for _, p := range pages {
		if p == "index.html" {
			return "", nil
		}
	}
	if len(pages) > 0 {
		sort.Slice(pages, func(i, j int) bool { return len(pages[i]) < len(pages[j]) })
		return pages[0], nil
	}
	return "", nil // directory listing
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func mediaPage(name string) string {
	esc := html.EscapeString(name)
	src := html.EscapeString(url.PathEscape(name))
	var media string
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".avif", ".heic":
		media = `<img src="` + src + `" alt="` + esc + `">`
	case ".mp4", ".mov", ".m4v", ".webm":
		media = `<video src="` + src + `" controls playsinline preload="metadata"></video>`
	case ".mp3", ".m4a", ".aac", ".wav", ".ogg", ".flac":
		media = `<div class="audio"><p>` + esc + `</p><audio src="` + src + `" controls preload="metadata"></audio></div>`
	default:
		return ""
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + esc + `</title><style>
html,body{margin:0;height:100%;background:#111;color:#eee;font:15px -apple-system,system-ui,sans-serif}
body{display:flex;align-items:center;justify-content:center}
img,video{max-width:100%;max-height:100vh;display:block}
.audio{width:min(92vw,560px);text-align:center}.audio audio{width:100%}.audio p{color:#999;margin:0 0 14px}
</style></head><body>` + media + `</body></html>`
}
