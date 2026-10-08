package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Plan is the coordinator's structured picture of a project: what matters now,
// the work in priority order with its stage, the order PRs should merge in, and
// what comes next.
type Plan struct {
	Focus      string      `json:"focus,omitempty"`
	Work       []PlanWork  `json:"work"`
	MergeOrder []PlanMerge `json:"mergeOrder,omitempty"`
	Next       []PlanNext  `json:"next,omitempty"`
	Updated    time.Time   `json:"updated"`

	// Filled in by the daemon for the app (see reconcile.go); dropped when an
	// agent publishes a plan.
	Unplanned []PlanThread `json:"unplanned,omitempty"` // open threads the plan doesn't mention
	Checked   time.Time    `json:"checked,omitzero"`    // last updated or confirmed by the coordinator
	Checking  bool         `json:"checking,omitempty"`  // Sidekick asked the coordinator to reconcile
	Stale     bool         `json:"stale,omitempty"`     // it differs from what Sidekick observes
}

// PlanThread is an open thread the plan doesn't cover.
type PlanThread struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Group string `json:"group,omitempty"`
}

type PlanWork struct {
	Title  string `json:"title"`
	Stage  string `json:"stage"` // research | planning | building | review | merging | blocked | done
	Thread string `json:"thread,omitempty"`
	PR     string `json:"pr,omitempty"`
	Note   string `json:"note,omitempty"`
	// WaitingOn says what blocked work waits for: "user", or who/what else ("CI",
	// "release owner"). Required when Stage is blocked.
	WaitingOn string `json:"waitingOn,omitempty"`
	// Ask is filled in by the daemon: the pending inbox item that unblocks this
	// work, when it waits on the user.
	Ask string `json:"ask,omitempty"`
	// Auto says what Sidekick corrected from what it observed ("thread resolved",
	// "PR merged"), and Faded marks done work that is about to drop off the page.
	Auto  string `json:"auto,omitempty"`
	Faded bool   `json:"faded,omitempty"`
}

var mentionsUser = regexp.MustCompile(`(?i)\b(you|your|user)\b`)

// waitsOnUser reports whether blocked work needs the user. Plans written before
// waitingOn existed are read from their wording.
func (w PlanWork) waitsOnUser() bool {
	if w.Stage != "blocked" {
		return false
	}
	if w.WaitingOn != "" {
		return strings.EqualFold(w.WaitingOn, "user")
	}
	return mentionsUser.MatchString(w.Title + " " + w.Note)
}

type PlanMerge struct {
	PR    string `json:"pr"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
}

type PlanNext struct {
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
}

var planStages = map[string]bool{"research": true, "planning": true, "building": true, "review": true, "merging": true, "blocked": true, "done": true}

func (p *Plan) validate() error {
	if len(p.Work) == 0 && len(p.Next) == 0 {
		return errors.New("a plan needs at least one work or next entry")
	}
	for i, w := range p.Work {
		if strings.TrimSpace(w.Title) == "" {
			return fmt.Errorf("work[%d] needs a title", i)
		}
		if !planStages[w.Stage] {
			return fmt.Errorf("work[%d] stage %q must be one of research, planning, building, review, merging, blocked, done", i, w.Stage)
		}
		if w.Stage == "blocked" && strings.TrimSpace(w.WaitingOn) == "" {
			return fmt.Errorf(`work[%d] is blocked: set "waitingOn" to "user" or to who/what it waits for (e.g. "CI", "release owner"). If it waits on the user, ask them first (AskUserQuestion for decisions, sidekick review, sidekick propose) so it's in their inbox`, i)
		}
	}
	for i, m := range p.MergeOrder {
		if !prURL.MatchString(m.PR) {
			return fmt.Errorf("mergeOrder[%d] pr must be a GitHub pull request URL", i)
		}
	}
	return nil
}

func (s *Store) planPath(project string) string {
	return filepath.Join(s.dir, "status", project+".plan.json")
}

func (s *Store) Plan(project string) *Plan {
	var p Plan
	if readJSON(s.planPath(project), &p) != nil {
		return nil
	}
	return &p
}

func (s *Store) SetPlan(project string, p Plan) error {
	p.Updated = time.Now().UTC()
	if err := writeJSON(s.planPath(project), p); err != nil {
		return err
	}
	s.Publish(project)
	return nil
}

func (s *Server) setPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origin Origin `json:"origin"`
		Plan   Plan   `json:"plan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad plan JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := body.Plan.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	project, thread, ok := s.resolve(w, body.Origin)
	if !ok {
		return
	}
	if thread != "" {
		http.Error(w, "only the project coordinator sets the plan", http.StatusForbidden)
		return
	}
	if err := s.store.SetPlan(project, body.Plan.authored()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.reconciled(project, false)
	writeJSONResponse(w, map[string]string{"project": project})
}

// propose files a new thread proposal for the user's approval (coordinator only).
func (s *Server) propose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origin Origin `json:"origin"`
		Title  string `json:"title"`
		Why    string `json:"why"`
		Plan   string `json:"plan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Title) == "" || strings.TrimSpace(body.Why) == "" {
		http.Error(w, "title and why required", http.StatusBadRequest)
		return
	}
	project, thread, ok := s.resolve(w, body.Origin)
	if !ok {
		return
	}
	if thread != "" {
		http.Error(w, "only the project coordinator proposes threads", http.StatusForbidden)
		return
	}
	it := &Item{Project: project, Kind: "proposal", Origin: body.Origin,
		Title: strings.TrimSpace(body.Title), Summary: strings.TrimSpace(body.Why), Text: strings.TrimSpace(body.Plan)}
	if err := s.store.Add(it); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyPush(*it)
	writeJSONResponse(w, it)
}

// authored strips what the daemon fills in, so a plan an agent copied from
// `sidekick plan -show` is stored as the agent's own.
func (p Plan) authored() Plan {
	p.Unplanned, p.Checked, p.Checking, p.Stale = nil, time.Time{}, false, false
	p.Work = append([]PlanWork(nil), p.Work...)
	for i := range p.Work {
		p.Work[i].Ask, p.Work[i].Auto, p.Work[i].Faded = "", "", false
	}
	return p
}

// notice sends the coordinator a message from Sidekick itself, tagged so the
// prompt hook can verify it.
func (s *Server) notice(project, text string) error { return s.noticeTo(project, "", text) }

// noticeTo sends a verified Sidekick reminder to a coordinator, or to one thread.
func (s *Server) noticeTo(project, thread, text string) error {
	it := &Item{Project: project, Thread: thread, Kind: "notice", Text: text, State: StatePending}
	if err := s.store.Add(it); err != nil {
		return err
	}
	s.store.Close(it.ID, "notice", StateAnswered, nil)
	return s.deliver.Prompt(project, thread, fmt.Sprintf("[sidekick notice:%s] %s", it.ID, text))
}
