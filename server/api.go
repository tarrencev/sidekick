package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	store       *Store
	projects    *Projects
	deliver     *Deliverer
	artifactURL string // public base URL of the artifact origin, e.g. https://dl.ts.net:7444
	notifyURL   string // optional ntfy topic URL
	prs         *PRWatcher
	apns        *APNs

	mu     sync.Mutex
	leases map[string]int // live agent connections waiting on a question
}

// ---- user API (tailnet, behind `tailscale serve`) ----

type projectSummary struct {
	Project
	Status  *Status        `json:"status,omitempty"`
	Pending int            `json:"pending"`
	Threads map[string]int `json:"threads"`
}

type projectDetail struct {
	projectSummary
	Items       []Item              `json:"items"`
	ThreadsList []Thread            `json:"threadsList"`
	Plan        *Plan               `json:"plan,omitempty"`
	PRs         map[string]PRStatus `json:"prs"`
}

func (s *Server) userMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/projects", s.listProjects)
	m.HandleFunc("GET /api/projects/{slug}", s.getProject)
	m.HandleFunc("POST /api/items/{id}/answer", s.answer)
	m.HandleFunc("POST /api/items/{id}/review", s.review)
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("GET /api/inbox", s.inbox)
	m.HandleFunc("GET /api/messages", s.listMessages)
	m.HandleFunc("POST /api/messages", s.postMessage)
	m.HandleFunc("POST /api/devices", s.registerDevice)
	m.HandleFunc("GET /api/projects/{slug}/threads/{tid}", s.getThread)
	m.HandleFunc("POST /api/projects/{slug}/prompt", s.prompt)
	return m
}

func (s *Server) summary(pr Project) projectSummary {
	sum := projectSummary{Project: pr, Threads: map[string]int{}}
	if st, ok := s.store.Status(pr.Slug); ok {
		sum.Status = &st
	}
	for _, it := range s.store.Items(pr.Slug) {
		if it.State == StatePending && it.Kind != "message" {
			sum.Pending++
		}
	}
	for _, t := range s.projects.Threads(pr.Slug) {
		sum.Threads[t.Group]++
	}
	return sum
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	out := []projectSummary{}
	for _, pr := range s.projects.List() {
		if pr.Active {
			out = append(out, s.summary(pr))
		}
	}
	writeJSONResponse(w, out)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.projects.Get(r.PathValue("slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	d := projectDetail{projectSummary: s.summary(pr), ThreadsList: s.threads(pr.Slug)}
	d.Items = []Item{}
	for _, it := range s.store.Items(pr.Slug) {
		if it.Kind != "message" && it.Kind != "notice" { // conversations live in the composer
			d.Items = append(d.Items, it)
		}
	}
	d.Plan = s.withAsks(pr.Slug, s.store.Plan(pr.Slug))
	var urls []string
	if d.Status != nil {
		urls = append(urls, d.Status.Link)
	}
	for _, t := range d.ThreadsList {
		urls = append(urls, t.PR, t.Link)
	}
	if d.Plan != nil {
		for _, w := range d.Plan.Work {
			urls = append(urls, w.PR)
		}
		for _, m := range d.Plan.MergeOrder {
			urls = append(urls, m.PR)
		}
	}
	d.PRs = s.prs.Statuses(urls...)
	if d.ThreadsList == nil {
		d.ThreadsList = []Thread{}
	}
	if d.Items == nil {
		d.Items = []Item{}
	}
	writeJSONResponse(w, d)
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answers map[string]string `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Answers) == 0 {
		http.Error(w, "answers required", http.StatusBadRequest)
		return
	}
	it, err := s.store.Close(r.PathValue("id"), "question", StateAnswered, func(it *Item) {
		it.Answers = body.Answers
	})
	if !checkClose(w, r, err) {
		return
	}
	if it.Async {
		s.deliver.Enqueue(it.ID) // nobody is waiting: type the answer into the asker's pane
	}
	writeJSONResponse(w, it)
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Verdict string `json:"verdict"` // "approve" | "changes" | "reject"
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	state := map[string]string{"approve": StateApproved, "changes": StateChanges, "reject": StateRejected}[body.Verdict]
	if state == "" {
		http.Error(w, `verdict must be "approve", "changes" or "reject"`, http.StatusBadRequest)
		return
	}
	if state == StateChanges && strings.TrimSpace(body.Comment) == "" {
		http.Error(w, "say what should change", http.StatusBadRequest)
		return
	}
	it, err := s.store.Close(r.PathValue("id"), "review|proposal", state, func(it *Item) {
		it.Comment = strings.TrimSpace(body.Comment)
	})
	if !checkClose(w, r, err) {
		return
	}
	s.deliver.Enqueue(it.ID)
	writeJSONResponse(w, it)
}

func checkClose(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, os.ErrNotExist):
		http.NotFound(w, r)
	case errors.Is(err, ErrNotPending):
		http.Error(w, "already handled", http.StatusConflict)
	case errors.Is(err, ErrWrongKind):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		return true
	}
	return false
}

type threadDetail struct {
	Thread
	Report string              `json:"report,omitempty"`
	Files  []libraryFile       `json:"files"`
	Items  []Item              `json:"items"`
	PRs    map[string]PRStatus `json:"prs"`
}

type libraryFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	slug, tid := r.PathValue("slug"), r.PathValue("tid")
	if _, ok := s.projects.Get(slug); !ok {
		http.NotFound(w, r)
		return
	}
	var d threadDetail
	for _, t := range s.threads(slug) {
		if t.ID == tid {
			d.Thread = t
		}
	}
	if d.ID == "" {
		http.NotFound(w, r)
		return
	}
	d.Report = s.projects.Report(slug, tid)
	d.Files = []libraryFile{}
	for _, f := range s.projects.LibraryFiles(slug, tid) {
		d.Files = append(d.Files, libraryFile{Name: f, URL: strings.TrimRight(s.artifactURL, "/") + "/lib/" + path.Join(slug, tid, f)})
	}
	d.Items = []Item{}
	for _, it := range s.store.Items(slug) {
		if it.Thread == tid && it.Kind != "message" && it.Kind != "notice" {
			d.Items = append(d.Items, it)
		}
	}
	d.PRs = s.prs.Statuses(d.PR, d.Link)
	writeJSONResponse(w, d)
}

// libraryHandler serves /lib/<slug>/<tid>/<file> from herdr-projects' library
// folders, on the artifact origin.
func (s *Server) libraryHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/lib/"), "/")
		if _, ok := s.projects.Get(slug); !ok {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + rest
		http.FileServer(http.Dir(s.projects.LibraryDir(slug))).ServeHTTP(w, r2)
	})
}

type inboxItem struct {
	Item
	ProjectName string `json:"projectName"`
	ThreadTitle string `json:"threadTitle,omitempty"`
}

// inbox lists every item across active projects, newest first.
func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	out := []inboxItem{}
	names := map[string]string{}
	titles := map[string]string{}
	for _, pr := range s.projects.List() {
		if !pr.Active {
			continue
		}
		names[pr.Slug] = pr.Name
		for _, t := range s.projects.Threads(pr.Slug) {
			titles[pr.Slug+"/"+t.ID] = t.Title
		}
	}
	for _, it := range s.store.Items("") {
		if it.Kind == "message" || it.Kind == "notice" {
			continue // conversations and agent reminders aren't for the inbox
		}
		if name, ok := names[it.Project]; ok {
			out = append(out, inboxItem{Item: it, ProjectName: name, ThreadTitle: titles[it.Project+"/"+it.Thread]})
		}
	}
	writeJSONResponse(w, out)
}

// prompt is the original fire-and-forget form of POST /api/messages, kept for
// older app builds.
func (s *Server) prompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if _, err := s.sendMessage(r.PathValue("slug"), "", body.Text); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// events streams "changed" notifications so the app can refetch.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, cancel := s.store.Subscribe()
	defer cancel()
	fmt.Fprint(w, ": hello\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case slug := <-ch:
			fmt.Fprintf(w, "event: changed\ndata: %s\n\n", slug)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		fl.Flush()
	}
}

// ---- agent API (unix socket, local agents only) ----

func (s *Server) agentMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/ask", s.ask)
	m.HandleFunc("GET /v1/items/{id}/wait", s.wait)
	m.HandleFunc("POST /v1/items/{id}/cancel", s.cancel)
	m.HandleFunc("POST /v1/items/{id}/release", s.release)
	m.HandleFunc("POST /v1/items/{id}/reopen", s.reopen)
	m.HandleFunc("POST /v1/review", s.requestReview)
	m.HandleFunc("POST /v1/status", s.setStatus)
	m.HandleFunc("POST /v1/whoami", s.whoami)
	m.HandleFunc("POST /v1/plan", s.setPlan)
	m.HandleFunc("POST /v1/verify", s.verify)
	m.HandleFunc("POST /v1/push-test", s.pushTest)
	m.HandleFunc("POST /v1/propose", s.propose)
	m.HandleFunc("POST /v1/messages/pending", s.pendingMessages)
	m.HandleFunc("POST /v1/messages/{id}/reply", s.replyMessage)
	return m
}

func (s *Server) resolve(w http.ResponseWriter, o Origin) (string, string, bool) {
	project, thread, ok := s.projects.Resolve(o)
	if !ok {
		http.Error(w, fmt.Sprintf("not inside a herdr-projects project (session=%q pane=%q cwd=%q)", o.Session, o.Pane, o.Cwd), http.StatusUnprocessableEntity)
	}
	return project, thread, ok
}

func (s *Server) whoami(w http.ResponseWriter, r *http.Request) {
	var o Origin
	json.NewDecoder(r.Body).Decode(&o)
	if p, t, ok := s.resolve(w, o); ok {
		writeJSONResponse(w, map[string]string{"project": p, "thread": t})
	}
}

func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origin    Origin     `json:"origin"`
		Questions []Question `json:"questions"`
		Async     bool       `json:"async"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Questions) == 0 {
		http.Error(w, "questions required", http.StatusBadRequest)
		return
	}
	for _, q := range body.Questions {
		if strings.TrimSpace(q.Question) == "" {
			http.Error(w, "every question needs text", http.StatusBadRequest)
			return
		}
	}
	project, thread, ok := s.resolve(w, body.Origin)
	if !ok {
		return
	}
	it := &Item{Project: project, Thread: thread, Kind: "question", Origin: body.Origin, Questions: body.Questions, Async: body.Async}
	if err := s.store.Add(it); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !it.Async {
		// A blocking asker must start waiting promptly; otherwise the question lapses.
		s.leaseRelease(it.ID)
	}
	s.notifyPush(*it)
	writeJSONResponse(w, it)
}

// wait long-polls a question. A question stays pending only while some agent is
// waiting on it: when the last waiter disconnects (the agent was interrupted or
// the hook timed out) and nobody reconnects within the grace period, it is cancelled.
func (s *Server) wait(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	timeout := 50 * time.Second
	if v, err := strconv.Atoi(r.URL.Query().Get("timeout")); err == nil && v > 0 && v <= 300 {
		timeout = time.Duration(v) * time.Second
	}
	s.leaseAcquire(id)
	defer s.leaseRelease(id)

	done := make(chan Item, 1)
	go func() {
		it, _ := s.store.Wait(id, timeout)
		done <- it
	}()
	select {
	case it := <-done:
		if it.ID == "" {
			http.NotFound(w, r)
			return
		}
		writeJSONResponse(w, it)
	case <-r.Context().Done():
	}
}

// leaseGrace is how long a blocking question survives without a waiting asker.
var leaseGrace = 20 * time.Second

// resumeQuestions runs at startup. Blocking questions that were pending when the
// daemon stopped lost their askers' connections; they get the usual grace period
// to reconnect. Async questions have no asker waiting by design and stay in the
// inbox until the user answers.
func (s *Server) resumeQuestions() {
	for _, it := range s.store.Items("") {
		if it.Kind == "question" && it.State == StatePending && !it.Async {
			s.leaseAcquire(it.ID)
			s.leaseRelease(it.ID)
		}
	}
}

func (s *Server) leaseAcquire(id string) {
	s.mu.Lock()
	s.leases[id]++
	s.mu.Unlock()
}

func (s *Server) leaseRelease(id string) {
	s.mu.Lock()
	if s.leases[id] > 0 {
		s.leases[id]--
	}
	s.mu.Unlock()
	time.AfterFunc(leaseGrace, func() {
		s.mu.Lock()
		live := s.leases[id]
		if live == 0 {
			delete(s.leases, id)
		}
		s.mu.Unlock()
		if it, ok := s.store.Get(id); !ok || it.Async {
			return // async questions are never withdrawn for lack of a waiter
		}
		if live == 0 {
			if _, err := s.store.Close(id, "question", StateCancelled, nil); err == nil {
				log.Printf("question %s cancelled: asker went away", id)
			}
		}
	})
}

// release frees an agent that is blocked waiting on a question: the question moves
// to a new async item (still in the user's inbox, answer delivered later), and the
// waiting agent is told to carry on without it.
func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	old, ok := s.store.Get(r.PathValue("id"))
	if !ok || old.Kind != "question" || old.State != StatePending || old.Async {
		http.Error(w, "not a blocking question that's still pending", http.StatusConflict)
		return
	}
	moved := &Item{Project: old.Project, Thread: old.Thread, Kind: "question", Origin: old.Origin, Questions: old.Questions, Async: true}
	if err := s.store.Add(moved); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	note := fmt.Sprintf("Not answered yet. Sidekick kept this question in the user's inbox (%s) and released you so you aren't blocked. Continue with all other work now; don't act on this decision until a message starting with [sidekick answer:%s] arrives with their answer.", moved.ID, moved.ID)
	s.store.Close(old.ID, "question", StateAnswered, func(it *Item) {
		it.Answers = map[string]string{}
		for _, q := range old.Questions {
			it.Answers[q.Question] = note
		}
	})
	writeJSONResponse(w, moved)
}

// reopen puts a wrongly closed question, review or proposal back in the user's
// inbox (an operator repair; see the sidekick-dev skill).
func (s *Server) reopen(w http.ResponseWriter, r *http.Request) {
	it, err := s.store.Update(r.PathValue("id"), func(it *Item) error {
		if it.Kind != "question" && it.Kind != "review" && it.Kind != "proposal" {
			return ErrWrongKind
		}
		if it.State != StateCancelled {
			return errors.New("only a cancelled item can be reopened")
		}
		it.State, it.Closed, it.Answers, it.Delivered = StatePending, time.Time{}, nil, false
		if it.Kind == "question" {
			it.Async = true // nobody can be waiting on it any more
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.notifyPush(it)
	writeJSONResponse(w, it)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	it, err := s.store.Close(r.PathValue("id"), "question", StateCancelled, nil)
	if err != nil && !errors.Is(err, ErrNotPending) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSONResponse(w, it)
}

func (s *Server) requestReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origin  Origin `json:"origin"`
		Path    string `json:"path"`
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" || strings.TrimSpace(body.Title) == "" {
		http.Error(w, "path and title required", http.StatusBadRequest)
		return
	}
	project, thread, ok := s.resolve(w, body.Origin)
	if !ok {
		return
	}
	it := &Item{Project: project, Thread: thread, Kind: "review", Origin: body.Origin, Title: strings.TrimSpace(body.Title), Summary: strings.TrimSpace(body.Summary)}
	// Reserve the id first so the artifact directory and URL can carry it.
	it.ID = newID()
	entry, err := publishArtifact(body.Path, s.store.ArtifactDir(project, it.ID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	it.URL = strings.TrimRight(s.artifactURL, "/") + "/" + path.Join(project, it.ID, entry)
	if entry == "" {
		it.URL += "/"
	}
	if err := s.store.Add(it); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyPush(*it)
	writeJSONResponse(w, it)
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origin Origin `json:"origin"`
		Status
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Headline) == "" {
		http.Error(w, "headline required", http.StatusBadRequest)
		return
	}
	switch body.State {
	case "":
		body.State = "on-track"
	case "on-track", "at-risk", "blocked", "done":
	default:
		http.Error(w, "state must be on-track, at-risk, blocked or done", http.StatusBadRequest)
		return
	}
	project, thread, ok := s.resolve(w, body.Origin)
	if !ok {
		return
	}
	if body.Link != "" && !strings.HasPrefix(body.Link, "https://") && !strings.HasPrefix(body.Link, "http://") {
		http.Error(w, "link must be an http(s) URL", http.StatusBadRequest)
		return
	}
	// The coordinator's status describes the project; a thread's describes itself.
	if err := s.store.SetStatus(project, thread, body.Status); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key := project
	if thread != "" {
		key = threadKey(project, thread)
	}
	st, _ := s.store.Status(key)
	writeJSONResponse(w, map[string]any{"project": project, "thread": thread, "status": st})
}

// threads lists a project's open threads with their summaries: the thread's own
// `sidekick status` if it set one, else the opening of its report.
func (s *Server) threads(slug string) []Thread {
	ts := s.projects.Threads(slug)
	for i := range ts {
		t := &ts[i]
		if st, ok := s.store.Status(threadKey(slug, t.ID)); ok {
			t.Summary, t.Link, t.Updated = firstNonEmpty(st.Summary, st.Headline), st.Link, st.Updated
		} else {
			t.Summary = reportSummary(s.projects.Report(slug, t.ID))
		}
		if t.Link == "" {
			t.Link = firstNonEmpty(t.PR, firstURL.FindString(t.Summary))
		}
	}
	return ts
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func writeJSONResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
