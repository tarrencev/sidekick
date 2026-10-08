package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Attachment is a file the user sent from the app: a photo, a screenshot, a PDF.
// It's stored on this machine so agents can open it straight from Path.
type Attachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // MIME type
	Size int64  `json:"size"`
	URL  string `json:"url"`  // for the app, on the artifact origin
	Path string `json:"path"` // for the agent
}

const maxUploadBytes = 200 << 20

var (
	uploadID   = regexp.MustCompile(`^[0-9]{8}t[0-9]{6}-[0-9a-f]{8}$`)
	unsafeName = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)
)

func (s *Server) uploadsDir() string { return filepath.Join(s.store.dir, "uploads") }

// cleanName keeps a readable, safe file name (no paths, no odd characters).
func cleanName(name string) string {
	name = strings.TrimSpace(unsafeName.ReplaceAllString(filepath.Base(name), "-"))
	name = strings.Trim(name, ".- ")
	if name == "" {
		name = "file"
	}
	if len(name) > 120 {
		ext := filepath.Ext(name)
		name = name[:120-len(ext)] + ext
	}
	return name
}

// upload stores one file: POST /api/uploads?project=<slug>&name=<file name>, the
// body is the file's bytes.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	if _, ok := s.projects.Get(project); !ok {
		http.Error(w, "unknown project", http.StatusBadRequest)
		return
	}
	name := cleanName(r.URL.Query().Get("name"))
	b := make([]byte, 4)
	rand.Read(b)
	id := time.Now().UTC().Format("20060102t150405") + "-" + hex.EncodeToString(b)
	dir := filepath.Join(s.uploadsDir(), project, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil || n == 0 {
		os.RemoveAll(dir)
		msg := "empty upload"
		if err != nil {
			msg = err.Error()
		}
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	writeJSONResponse(w, s.attachment(project, id, name, n))
}

func (s *Server) attachment(project, id, name string, size int64) Attachment {
	typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if typ == "" {
		typ = "application/octet-stream"
	}
	return Attachment{
		ID: id, Name: name, Type: typ, Size: size,
		URL:  strings.TrimRight(s.artifactURL, "/") + "/uploads/" + path.Join(project, id, name),
		Path: filepath.Join(s.uploadsDir(), project, id, name),
	}
}

// resolveAttachments turns upload ids from the app into attachments of project.
func (s *Server) resolveAttachments(project string, ids []string) ([]Attachment, error) {
	var out []Attachment
	for _, id := range ids {
		if !uploadID.MatchString(id) {
			return nil, fmt.Errorf("bad attachment id %q", id)
		}
		entries, err := os.ReadDir(filepath.Join(s.uploadsDir(), project, id))
		if err != nil || len(entries) != 1 {
			return nil, errors.New("attachment not found; upload it again")
		}
		info, _ := entries[0].Info()
		out = append(out, s.attachment(project, id, entries[0].Name(), info.Size()))
	}
	return out, nil
}

// attachmentNote tells an agent where the user's files are.
func attachmentNote(atts []Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThe user attached these files. They're on this machine: open them from these paths (images can be viewed directly):\n")
	for _, a := range atts {
		fmt.Fprintf(&b, "- %s (%s, %s)\n", a.Path, a.Type, humanSize(a.Size))
	}
	return b.String()
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// uploadsHandler serves /uploads/<project>/<id>/<name> on the artifact origin, so
// the app can show what was sent.
func (s *Server) uploadsHandler() http.Handler {
	return http.StripPrefix("/uploads/", http.FileServer(http.Dir(s.uploadsDir())))
}
