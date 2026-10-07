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
	"regexp"
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
	if !info.IsDir() && !isHTML(src) {
		return "", rejectArtifact([]string{fmt.Sprintf("%s is a bare %s file", filepath.Base(src), strings.TrimPrefix(filepath.Ext(src), "."))})
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(src, "index.html")); err != nil {
			return "", rejectArtifact([]string{"the folder has no index.html, so the user would only see a list of file names"})
		}
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	entry := "index.html"
	if !info.IsDir() {
		entry = filepath.Base(src)
		if _, err := copyFile(src, filepath.Join(dst, entry)); err != nil {
			return "", err
		}
	} else if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	if problems := checkPage(dst, entry); len(problems) > 0 {
		os.RemoveAll(dst)
		return "", rejectArtifact(problems)
	}
	if entry == "index.html" {
		return "", nil
	}
	return entry, nil
}

func copyTree(src, dst string) error {
	var total int64
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
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
		return nil
	})
}

// ---- What a reviewable page must be ----
//
// The user reviews artifacts on their phone, often without any other context. So an
// artifact is always one HTML page that explains itself in plain English and shows
// its media inline, never a folder of files or a bare image.

// minWords is the least explanation a page must carry beyond its media.
const minWords = 40

var (
	scriptOrStyle = regexp.MustCompile(`(?is)<script\b.*?</script>|<style\b.*?</style>|<!--.*?-->`)
	anyTag        = regexp.MustCompile(`(?s)<[^>]+>`)
	heading       = regexp.MustCompile(`(?is)<h1\b|<title>\s*[^<\s]`)
	embedRef      = regexp.MustCompile(`(?is)<(?:img|video|audio|source|iframe|embed|object|track)\b[^>]*?\b(?:src|data|poster)\s*=\s*["']([^"']+)["']`)
	cssURL        = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]+)["']?\s*\)`)
	mediaExt      = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".svg": true, ".heic": true,
		".mp4": true, ".mov": true, ".m4v": true, ".webm": true, ".mp3": true, ".m4a": true, ".aac": true, ".wav": true, ".ogg": true, ".flac": true}
)

func isHTML(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".html" || ext == ".htm"
}

// checkPage lists what keeps the page at entry (inside dir) from being reviewable.
func checkPage(dir, entry string) []string {
	raw, err := os.ReadFile(filepath.Join(dir, entry))
	if err != nil {
		return []string{"can't read " + entry}
	}
	page := string(raw)
	var problems []string

	if n := strings.Count(page, "[["); n > 0 {
		problems = append(problems, fmt.Sprintf("the page still has %d template placeholder(s) like [[…]]; fill them in or delete those sections", n))
	}
	if !heading.MatchString(page) {
		problems = append(problems, "the page has no title (<h1> or <title>) saying what it is")
	}
	text := html.UnescapeString(anyTag.ReplaceAllString(scriptOrStyle.ReplaceAllString(page, " "), " "))
	if n := len(strings.Fields(text)); n < minWords {
		problems = append(problems, fmt.Sprintf("the page has only %d words of explanation; say in plain English what this is, what changed, what to look at, and what you need decided (at least %d words)", n, minWords))
	}

	// Every media file in the artifact must be shown on the page, not linked or left out.
	embedded := map[string]bool{}
	base := filepath.Dir(entry)
	refs := append(embedRef.FindAllStringSubmatch(page, -1), cssURL.FindAllStringSubmatch(page, -1)...)
	var missing []string
	for _, m := range refs {
		ref := m[1]
		if strings.Contains(ref, "://") || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "//") {
			continue
		}
		ref, _ = url.PathUnescape(strings.SplitN(strings.SplitN(ref, "#", 2)[0], "?", 2)[0])
		rel := filepath.Clean(filepath.Join(base, ref))
		embedded[filepath.ToSlash(rel)] = true
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			missing = append(missing, ref)
		}
	}
	var notShown []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !mediaExt[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if !embedded[filepath.ToSlash(rel)] {
			notShown = append(notShown, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(notShown) > 0 {
		sort.Strings(notShown)
		problems = append(problems, "these files aren't shown on the page (embed each with <img>/<video>/<audio> and a caption, don't just link them): "+strings.Join(notShown, ", "))
	}
	if len(missing) > 0 {
		problems = append(problems, "the page embeds files that aren't in the artifact: "+strings.Join(missing, ", "))
	}
	return problems
}

func rejectArtifact(problems []string) error {
	var b strings.Builder
	b.WriteString("not published: the user reviews this on their phone with no other context, so an artifact must be one HTML page that explains itself.\n")
	for _, p := range problems {
		b.WriteString("  - " + p + "\n")
	}
	b.WriteString("Make an index.html that says in plain English what this is, what changed and what you need, and embeds every image or video with a caption saying what to notice. Start from `sidekick template <dir>`, then run `sidekick review <dir>` again.")
	return errors.New(b.String())
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
