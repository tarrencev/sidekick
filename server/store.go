package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Item is one thing an agent pushed to the user: a question or an artifact to review.
type Item struct {
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Thread  string    `json:"thread,omitempty"`
	Kind    string    `json:"kind"` // "question" | "review" | "message"
	State   string    `json:"state"`
	Created time.Time `json:"created"`
	Closed  time.Time `json:"closed,omitzero"`
	Origin  Origin    `json:"origin"`

	// question. Async questions don't hold the agent: it keeps working and the
	// answer is typed into its pane once the user gives it.
	Async     bool              `json:"async,omitempty"`
	Questions []Question        `json:"questions,omitempty"`
	Answers   map[string]string `json:"answers,omitempty"`

	// review
	Title     string `json:"title,omitempty"`
	Summary   string `json:"summary,omitempty"`
	URL       string `json:"url,omitempty"`
	Comment   string `json:"comment,omitempty"`
	Delivered bool   `json:"delivered,omitempty"`

	// message: the user wrote to an agent from the app; Reply is its answer.
	Text  string `json:"text,omitempty"`
	Reply string `json:"reply,omitempty"`
	Error string `json:"error,omitempty"`
}

const (
	StatePending   = "pending"
	StateAnswered  = "answered"
	StateApproved  = "approved"
	StateChanges   = "changes"
	StateRejected  = "rejected"
	StateCancelled = "cancelled"
	StateReplied   = "replied"
	StateFailed    = "failed"
)

type Question struct {
	Question    string   `json:"question"`
	Header      string   `json:"header,omitempty"`
	Options     []Option `json:"options,omitempty"`
	MultiSelect bool     `json:"multiSelect,omitempty"`
}

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Origin identifies the agent pane that pushed an item.
type Origin struct {
	Session string `json:"session,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
}

// Status is the agent-maintained, high-level status of a project.
type Status struct {
	Headline string    `json:"headline"`
	Summary  string    `json:"summary,omitempty"` // 3-4 sentences on where things stand
	Link     string    `json:"link,omitempty"`    // the one place to look, e.g. a PR
	State    string    `json:"state"`             // "on-track" | "at-risk" | "blocked" | "done"
	Notes    []string  `json:"notes,omitempty"`
	Updated  time.Time `json:"updated"`
}

// threadKey is the status key for one thread of a project.
func threadKey(project, thread string) string { return project + "~" + thread }

type Store struct {
	dir string

	mu      sync.Mutex
	items   map[string]*Item
	status  map[string]Status
	waiters map[string][]chan struct{}
	subs    map[chan string]struct{}
}

func OpenStore(dir string) (*Store, error) {
	s := &Store{
		dir:     dir,
		items:   map[string]*Item{},
		status:  map[string]Status{},
		waiters: map[string][]chan struct{}{},
		subs:    map[chan string]struct{}{},
	}
	for _, d := range []string{"items", "status", "artifacts"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return nil, err
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "items", "*.json"))
	for _, f := range files {
		var it Item
		if readJSON(f, &it) == nil {
			s.items[it.ID] = &it
		}
	}
	files, _ = filepath.Glob(filepath.Join(dir, "status", "*.json"))
	for _, f := range files {
		var st Status
		if readJSON(f, &st) == nil {
			s.status[trimExt(filepath.Base(f))] = st
		}
	}
	return s, nil
}

func (s *Store) ArtifactDir(project, id string) string {
	return filepath.Join(s.dir, "artifacts", project, id)
}

func (s *Store) Add(it *Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it.ID == "" {
		it.ID = newID()
	}
	it.Created = time.Now().UTC()
	it.State = StatePending
	if err := s.persist(it); err != nil {
		return err
	}
	s.items[it.ID] = it
	s.publish(it.Project)
	return nil
}

func (s *Store) Get(id string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, false
	}
	return *it, true
}

var ErrNotPending = errors.New("item is not pending")

// Update mutates a pending item under the lock, persists it and wakes waiters.
func (s *Store) Update(id string, fn func(*Item) error) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, os.ErrNotExist
	}
	next := *it
	if err := fn(&next); err != nil {
		return Item{}, err
	}
	if err := s.persist(&next); err != nil {
		return Item{}, err
	}
	*it = next
	for _, ch := range s.waiters[id] {
		close(ch)
	}
	delete(s.waiters, id)
	s.publish(it.Project)
	return next, nil
}

var ErrWrongKind = errors.New("wrong item kind")

// Close transitions a pending item of the given kind ("" for any; "a|b" for
// either) to a terminal state.
func (s *Store) Close(id, kind, state string, fn func(*Item)) (Item, error) {
	return s.Update(id, func(it *Item) error {
		if kind != "" && !strings.Contains("|"+kind+"|", "|"+it.Kind+"|") {
			return ErrWrongKind
		}
		if it.State != StatePending {
			return ErrNotPending
		}
		it.State = state
		it.Closed = time.Now().UTC()
		if fn != nil {
			fn(it)
		}
		return nil
	})
}

// Wait blocks until the item leaves the pending state or the timeout elapses.
func (s *Store) Wait(id string, timeout time.Duration) (Item, bool) {
	s.mu.Lock()
	it, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return Item{}, false
	}
	if it.State != StatePending {
		cp := *it
		s.mu.Unlock()
		return cp, true
	}
	ch := make(chan struct{})
	s.waiters[id] = append(s.waiters[id], ch)
	s.mu.Unlock()

	select {
	case <-ch:
	case <-time.After(timeout):
	}
	return s.Get(id)
}

// Items returns a project's items, newest first. Pass "" for all projects.
func (s *Store) Items(project string) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Item
	for _, it := range s.items {
		if project == "" || it.Project == project {
			out = append(out, *it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Status returns the status stored under a project slug or a threadKey.
func (s *Store) Status(project string) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.status[project]
	return st, ok
}

// SetStatus stores a project's status, or a thread's when thread is non-empty.
func (s *Store) SetStatus(project, thread string, st Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := project
	if thread != "" {
		key = threadKey(project, thread)
	}
	st.Updated = time.Now().UTC()
	if err := writeJSON(filepath.Join(s.dir, "status", key+".json"), st); err != nil {
		return err
	}
	s.status[key] = st
	s.publish(project)
	return nil
}

// Subscribe returns a channel that receives the slug of every project that changes.
func (s *Store) Subscribe() (chan string, func()) {
	ch := make(chan string, 32)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

func (s *Store) Publish(project string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publish(project)
}

func (s *Store) publish(project string) {
	for ch := range s.subs {
		select {
		case ch <- project:
		default: // slow subscriber; it will resync on its next fetch
		}
	}
}

func (s *Store) persist(it *Item) error {
	return writeJSON(filepath.Join(s.dir, "items", it.ID+".json"), it)
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return time.Now().UTC().Format("20060102t150405") + "-" + hex.EncodeToString(b)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func trimExt(name string) string {
	return name[:len(name)-len(filepath.Ext(name))]
}
