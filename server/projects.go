package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Projects reads herdr-projects state (~/.herdr-projects) directly from disk.
// It is read-only: herdr-projects owns these files.
type Projects struct {
	root string
}

type Project struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Goal   string `json:"goal,omitempty"`
	Active bool   `json:"active"`

	coordinator coordinatorRecord
}

type coordinatorRecord struct {
	Session string `json:"session"`
	PaneID  string `json:"pane_id"`
	Cwd     string `json:"cwd"`
}

type Thread struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Group     string    `json:"group"`
	Line      string    `json:"line,omitempty"`
	PR        string    `json:"pr,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Link      string    `json:"link,omitempty"`
	Updated   time.Time `json:"updated,omitzero"`
	PaneID    string    `json:"-"`
	Cwd       string    `json:"-"`
	ThreadDir string    `json:"-"`
}

func (p *Projects) List() []Project {
	dirs, _ := os.ReadDir(p.root)
	var out []Project
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		if pr, ok := p.Get(d.Name()); ok {
			out = append(out, pr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (p *Projects) Get(slug string) (Project, bool) {
	if slug == "" || strings.ContainsAny(slug, "/\\") || strings.HasPrefix(slug, ".") {
		return Project{}, false
	}
	dir := filepath.Join(p.root, slug)
	fm, ok := readFrontMatter(filepath.Join(dir, "PROJECT.md"))
	if !ok {
		return Project{}, false
	}
	pr := Project{Slug: slug, Name: fm["name"], Goal: fm["goal"], Active: true}
	if pr.Name == "" {
		pr.Name = slug
	}
	var state struct {
		Status string `json:"status"`
	}
	if readJSON(filepath.Join(dir, ".state", "project.json"), &state) == nil && state.Status != "" {
		pr.Active = state.Status == "active"
	}
	readJSON(filepath.Join(dir, ".state", "coordinator.json"), &pr.coordinator)
	return pr, true
}

// Report returns the home copy of a thread's report (markdown), if any.
func (p *Projects) Report(slug, tid string) string {
	if !safeName(tid) {
		return ""
	}
	b, _ := os.ReadFile(filepath.Join(p.root, slug, "threads", tid+".md"))
	return string(b)
}

// LibraryFiles lists the files a thread handed the user (library/<tid>/...),
// relative to that folder.
func (p *Projects) LibraryFiles(slug, tid string) []string {
	if !safeName(tid) {
		return nil
	}
	dir := filepath.Join(p.root, slug, "library", tid)
	var out []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && len(out) < 200 {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

// LibraryDir is the folder holding every thread's library for a project.
func (p *Projects) LibraryDir(slug string) string {
	return filepath.Join(p.root, slug, "library")
}

func safeName(s string) bool {
	return s != "" && !strings.ContainsAny(s, "/\\") && !strings.HasPrefix(s, ".")
}

// Threads returns the project's open threads, most urgent first.
func (p *Projects) Threads(slug string) []Thread {
	files, _ := filepath.Glob(filepath.Join(p.root, slug, "threads", "*.toml"))
	var out []Thread
	for _, f := range files {
		kv := readTOML(f)
		if kv["status"] != "open" {
			continue
		}
		out = append(out, Thread{
			ID: kv["id"], Title: kv["title"], Status: kv["status"],
			Group: kv["last_group"], Line: kv["state_line"], PR: kv["pr"],
			PaneID: kv["pane_id"], Cwd: kv["cwd"], ThreadDir: kv["thread_dir"],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := groupRank(out[i].Group), groupRank(out[j].Group); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func groupRank(g string) int {
	switch g {
	case "waiting-on-you":
		return 0
	case "ready-for-review":
		return 1
	case "landing":
		return 2
	case "working":
		return 3
	case "idle":
		return 4
	}
	return 5
}

// Resolve maps the pane an agent runs in to a project and, for threads, a thread id.
func (p *Projects) Resolve(o Origin) (project, thread string, ok bool) {
	for _, pr := range p.List() {
		sameSession := o.Session == "" || pr.coordinator.Session == "" || o.Session == pr.coordinator.Session
		if sameSession && o.Pane != "" && o.Pane == pr.coordinator.PaneID {
			return pr.Slug, "", true
		}
		if sameSession && o.Pane != "" {
			for _, t := range p.Threads(pr.Slug) {
				if t.PaneID == o.Pane {
					return pr.Slug, t.ID, true
				}
			}
		}
	}
	// No pane match (e.g. a subshell outside Herdr): fall back to the working directory.
	if o.Cwd != "" {
		for _, pr := range p.List() {
			for _, t := range p.Threads(pr.Slug) {
				if t.Cwd != "" && within(o.Cwd, t.Cwd) {
					return pr.Slug, t.ID, true
				}
			}
			if within(o.Cwd, filepath.Join(p.root, pr.Slug)) {
				return pr.Slug, "", true
			}
		}
	}
	return "", "", false
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// readFrontMatter parses the top-level string keys of the TOML block between
// the leading "+++" fences of a herdr-projects markdown file.
func readFrontMatter(path string) (map[string]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "+++" {
		return map[string]string{}, true
	}
	var lines []string
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "+++" {
			break
		}
		lines = append(lines, sc.Text())
	}
	return parseTOML(lines), true
}

func readTOML(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	return parseTOML(strings.Split(string(b), "\n"))
}

// parseTOML is a deliberately tiny reader: top-level `key = value` pairs only,
// stopping at the first table header. That is all sidekick needs.
func parseTOML(lines []string) map[string]string {
	kv := map[string]string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		kv[strings.TrimSpace(k)] = tomlString(strings.TrimSpace(v))
	}
	return kv
}

func tomlString(v string) string {
	switch {
	case strings.HasPrefix(v, `"`):
		if s, err := strconv.Unquote(firstQuoted(v, '"')); err == nil {
			return s
		}
	case strings.HasPrefix(v, `'`):
		q := firstQuoted(v, '\'')
		if len(q) >= 2 {
			return q[1 : len(q)-1]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// firstQuoted returns the leading quoted token of v, dropping any trailing comment.
func firstQuoted(v string, q byte) string {
	for i := 1; i < len(v); i++ {
		if v[i] == '\\' && q == '"' {
			i++
			continue
		}
		if v[i] == q {
			return v[:i+1]
		}
	}
	return v
}

// ThreadFact is what Sidekick can observe about any thread, open or resolved.
type ThreadFact struct {
	ID, Title, Status, Reason, PR, Group string
	Launched, Changed                    time.Time
}

// ThreadFacts returns every thread of a project, including resolved ones, by id.
func (p *Projects) ThreadFacts(slug string) map[string]ThreadFact {
	files, _ := filepath.Glob(filepath.Join(p.root, slug, "threads", "*.toml"))
	out := map[string]ThreadFact{}
	for _, f := range files {
		kv := readTOML(f)
		if kv["id"] == "" {
			continue
		}
		t := ThreadFact{ID: kv["id"], Title: kv["title"], Status: kv["status"], Reason: kv["resolved_reason"],
			PR: kv["pr"], Group: kv["last_group"]}
		t.Launched, _ = time.Parse(time.RFC3339, kv["launched_at"])
		t.Changed, _ = time.Parse(time.RFC3339, kv["last_state_change"])
		if t.Status == "resolved" {
			// Resolving rewrites the file; its modification time is when that happened.
			if fi, err := os.Stat(f); err == nil && fi.ModTime().After(t.Changed) {
				t.Changed = fi.ModTime()
			}
		}
		if t.Launched.After(t.Changed) {
			t.Changed = t.Launched
		}
		out[t.ID] = t
	}
	return out
}
