package main

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// Question context: a self-explaining HTML page shown inside a question, so the user
// sees what they're deciding about (images to pick from, a before/after) right there.
//
// Claude Code's question tool has no field for it, so an agent publishes the page
// first (`sidekick context <dir>`) and the next question from that pane picks it up.

const contextTTL = 30 * time.Minute

type pendingContext struct {
	url     string
	created time.Time
}

type contexts struct {
	mu     sync.Mutex
	byPane map[string]pendingContext
}

func paneKey(o Origin) string { return o.Session + "/" + o.Pane }

func (c *contexts) put(o Origin, url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byPane == nil {
		c.byPane = map[string]pendingContext{}
	}
	c.byPane[paneKey(o)] = pendingContext{url: url, created: time.Now()}
}

// take returns and clears the pane's pending context, if it's fresh.
func (c *contexts) take(o Origin) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.byPane[paneKey(o)]
	delete(c.byPane, paneKey(o))
	if !ok || time.Since(p.created) > contextTTL {
		return ""
	}
	return p.url
}

// publishContext publishes a context page for project and returns its URL.
func (s *Server) publishContext(project, src string) (string, error) {
	id := newID()
	entry, err := publishArtifact(src, s.store.ArtifactDir(project, id))
	if err != nil {
		return "", err
	}
	u := strings.TrimRight(s.artifactURL, "/") + "/" + path.Join(project, id, entry)
	if entry == "" {
		u += "/"
	}
	return u, nil
}

// postContext publishes a page to attach to the pane's next question.
func (s *Server) postContext(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origin Origin `json:"origin"`
		Path   string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	project, _, ok := s.resolve(w, body.Origin)
	if !ok {
		return
	}
	url, err := s.publishContext(project, body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.pendingContexts.put(body.Origin, url)
	writeJSONResponse(w, map[string]string{"url": url})
}
