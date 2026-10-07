package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	if err := s.store.SetPlan(project, body.Plan); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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

// withAsks links each piece of work that waits on the user to the pending inbox
// item that would unblock it: one from the same thread, or from the coordinator.
func (s *Server) withAsks(project string, plan *Plan) *Plan {
	if plan == nil {
		return nil
	}
	out := *plan
	out.Work = append([]PlanWork(nil), plan.Work...)
	pending := map[string]string{} // thread ("" = coordinator) -> oldest pending item
	for _, it := range s.store.Items(project) {
		if it.State == StatePending && (it.Kind == "question" || it.Kind == "review" || it.Kind == "proposal") {
			pending[it.Thread] = it.ID // Items is newest first, so the oldest wins
		}
	}
	for i, w := range out.Work {
		if !w.waitsOnUser() {
			continue
		}
		if id, ok := pending[w.Thread]; ok {
			out.Work[i].Ask = id
		} else if id, ok := pending[""]; ok && w.Thread != "" {
			out.Work[i].Ask = id
		}
	}
	return &out
}

// watchBlocked reminds a coordinator when its plan says work is waiting on the
// user but the user has nothing from it in their inbox: they can't unblock what
// they can't see.
func (s *Server) watchBlocked() {
	nudged := map[string]bool{}
	var mu sync.Mutex
	for {
		time.Sleep(2 * time.Minute)
		for _, pr := range s.projects.List() {
			plan := s.withAsks(pr.Slug, s.store.Plan(pr.Slug))
			if plan == nil || !pr.Active || time.Since(plan.Updated) < 10*time.Minute {
				continue
			}
			var stuck []string
			for _, w := range plan.Work {
				if w.waitsOnUser() && w.Ask == "" {
					stuck = append(stuck, w.Title)
				}
			}
			key := pr.Slug + "|" + plan.Updated.String() + "|" + strings.Join(stuck, "|")
			mu.Lock()
			seen := nudged[key]
			nudged[key] = true
			mu.Unlock()
			if len(stuck) == 0 || seen {
				continue
			}
			if err := s.notice(pr.Slug, fmt.Sprintf(
				"Your plan says this is blocked waiting on the user: %q. But the user has nothing from you in their Sidekick inbox, so they can't unblock it. Ask now: AskUserQuestion for decisions (up to 4 questions in one call, recommended option first; never leave decisions inside a review page), `sidekick review` for something to look at, or `sidekick propose` for new work. If it isn't actually waiting on the user, republish the plan with the right \"waitingOn\".",
				strings.Join(stuck, `", "`))); err != nil {
				log.Printf("sidekick: blocked reminder for %s: %v", pr.Slug, err)
			}
		}
	}
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
