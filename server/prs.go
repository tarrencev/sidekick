package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PRStatus is what the app shows on a PR link: its state and how CI is doing.
type PRStatus struct {
	State  string `json:"state"`  // open | draft | merged | closed
	Checks string `json:"checks"` // passing | failing | pending | none
	Review string `json:"review,omitempty"`
}

var prURL = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/pull/\d+$`)

// PRWatcher looks PR status up with the gh CLI and caches it. Lookups run in the
// background; the app gets the cached value now and a change event when it updates.
type PRWatcher struct {
	gh      string
	changed func()

	mu       sync.Mutex
	cache    map[string]PRStatus
	fetched  map[string]time.Time
	inFlight map[string]bool
}

func NewPRWatcher(changed func()) *PRWatcher {
	gh, _ := exec.LookPath("gh")
	if gh == "" {
		if p := home(".local", "share", "mise", "shims", "gh"); fileExists(p) {
			gh = p
		}
	}
	if gh == "" {
		log.Printf("sidekick: gh not found; PR status disabled")
	}
	return &PRWatcher{gh: gh, changed: changed, cache: map[string]PRStatus{}, fetched: map[string]time.Time{}, inFlight: map[string]bool{}}
}

// Statuses returns cached statuses for the GitHub PR URLs among urls, refreshing
// stale ones in the background.
func (w *PRWatcher) Statuses(urls ...string) map[string]PRStatus {
	out := map[string]PRStatus{}
	if w.gh == "" {
		return out
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if !prURL.MatchString(u) {
			continue
		}
		st, ok := w.cache[u]
		if ok {
			out[u] = st
		}
		ttl := 90 * time.Second
		if ok && (st.State == "merged" || st.State == "closed") {
			ttl = time.Hour
		}
		if time.Since(w.fetched[u]) > ttl && !w.inFlight[u] {
			w.inFlight[u] = true
			go w.fetch(u)
		}
	}
	return out
}

func (w *PRWatcher) fetch(u string) {
	st, err := ghStatus(w.gh, u)
	w.mu.Lock()
	delete(w.inFlight, u)
	w.fetched[u] = time.Now()
	old, had := w.cache[u]
	if err == nil {
		w.cache[u] = st
	}
	w.mu.Unlock()
	if err != nil {
		log.Printf("sidekick: pr status %s: %v", u, err)
		return
	}
	if !had || old != st {
		w.changed()
	}
}

func ghStatus(gh, u string) (PRStatus, error) {
	out, err := exec.Command(gh, "pr", "view", u, "--json", "state,isDraft,reviewDecision,statusCheckRollup").Output()
	if err != nil {
		return PRStatus{}, err
	}
	var v struct {
		State             string
		IsDraft           bool
		ReviewDecision    string
		StatusCheckRollup []struct {
			Status     string // check runs: QUEUED | IN_PROGRESS | COMPLETED
			Conclusion string // SUCCESS | FAILURE | ...
			State      string // status contexts: SUCCESS | FAILURE | PENDING | ERROR
		}
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return PRStatus{}, err
	}
	st := PRStatus{State: strings.ToLower(v.State), Checks: "none", Review: strings.ToLower(v.ReviewDecision)}
	if st.State == "open" && v.IsDraft {
		st.State = "draft"
	}
	failing, pending, passing := false, false, false
	for _, c := range v.StatusCheckRollup {
		res := c.Conclusion
		if res == "" {
			res = c.State
		}
		switch {
		case c.Status == "QUEUED" || c.Status == "IN_PROGRESS" || c.Status == "PENDING" || res == "PENDING" || res == "EXPECTED":
			pending = true
		case res == "FAILURE" || res == "ERROR" || res == "TIMED_OUT" || res == "CANCELLED" || res == "ACTION_REQUIRED" || res == "STARTUP_FAILURE":
			failing = true
		case res != "":
			passing = true
		}
	}
	switch {
	case failing:
		st.Checks = "failing"
	case pending:
		st.Checks = "pending"
	case passing:
		st.Checks = "passing"
	}
	return st, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
