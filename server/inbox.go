package main

// Keeping the inbox to what still needs the user.
//
// Agents ask, then move on, and their asks go stale: a newer version of the
// same question, a decision made in another way, a thread that finished. Three
// things keep the inbox current:
//
//   - The user can dismiss anything ("no longer needed"); the agent is told.
//   - An agent can withdraw its own asks (`sidekick withdraw`), and a new ask that
//     repeats a pending one from the same agent replaces it automatically.
//   - The page check (reconcile.go) shows the coordinator its project's pending
//     inbox and asks it to withdraw what's moot and merge near-duplicates.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// askKey is what makes two asks the same: their questions, or their title.
func askTexts(it Item) []string {
	var out []string
	if it.Kind == "question" {
		for _, q := range it.Questions {
			out = append(out, normalize(q.Question))
		}
	} else {
		out = append(out, normalize(it.Title))
	}
	return out
}

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func normalize(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// repeats reports whether a new ask covers everything an older one asked.
func repeats(newer, older Item) bool {
	if newer.Kind != older.Kind || newer.Project != older.Project || newer.Thread != older.Thread {
		return false
	}
	have := map[string]bool{}
	for _, t := range askTexts(newer) {
		have[t] = true
	}
	old := askTexts(older)
	for _, t := range old {
		if t == "" || !have[t] {
			return false
		}
	}
	return len(old) > 0
}

// similar reports whether two pending asks look like the same request worded
// differently, for the coordinator to merge: most of their words are shared.
func similar(a, b Item) bool {
	if a.Kind != b.Kind {
		return false
	}
	wa, wb := words(strings.Join(askTexts(a), " ")), words(strings.Join(askTexts(b), " "))
	if len(wa) < 4 || len(wb) < 4 {
		return false
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	return float64(shared)/float64(len(wa)+len(wb)-shared) >= 0.6
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(s) {
		if len(w) > 2 {
			out[w] = true
		}
	}
	return out
}

// supersede withdraws the pending asks a new one replaces: the one it names, and
// any earlier ask from the same agent that it repeats.
func (s *Server) supersede(it Item, replaces string) {
	for _, old := range s.store.Items(it.Project) {
		if old.ID == it.ID || old.State != StatePending || !isAsk(old) {
			continue
		}
		if old.ID != replaces && !repeats(it, old) {
			continue
		}
		if old.Kind != it.Kind || old.Project != it.Project {
			continue
		}
		s.store.Close(old.ID, old.Kind, StateCancelled, func(o *Item) {
			o.Reason = "Replaced by a newer version"
			o.Delivered = true // the agent replaced it itself; nothing to tell it
		})
		log.Printf("sidekick: %s replaced by %s", old.ID, it.ID)
	}
}

// dismiss is the user saying an ask no longer needs them. The agent is told, so
// it neither waits on it nor acts on a guess.
func (s *Server) dismiss(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	it, err := s.store.Close(r.PathValue("id"), "question|review|proposal", StateDismissed, func(it *Item) {
		it.Reason = strings.TrimSpace(body.Reason)
	})
	if !checkClose(w, r, err) {
		return
	}
	s.deliver.Enqueue(it.ID)
	writeJSONResponse(w, it)
}

// withdraw lets an agent take back an ask that no longer needs the user. A thread
// withdraws its own; the coordinator any in its project.
func (s *Server) withdraw(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origin Origin `json:"origin"`
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	project, thread, ok := s.resolve(w, body.Origin)
	if !ok {
		return
	}
	pre, found := s.store.Get(r.PathValue("id"))
	if !found || pre.Project != project || (thread != "" && pre.Thread != thread) {
		http.Error(w, "no such item of yours in this project", http.StatusNotFound)
		return
	}
	it, err := s.store.Close(pre.ID, "question|review|proposal", StateCancelled, func(it *Item) {
		it.Reason = firstNonEmpty(body.Reason, "Withdrawn by the agent")
		it.Delivered = true
	})
	if errors.Is(err, ErrNotPending) {
		http.Error(w, fmt.Sprintf("%s is already %s", pre.ID, pre.State), http.StatusConflict)
		return
	}
	if !checkClose(w, r, err) {
		return
	}
	writeJSONResponse(w, it)
}

// InboxEntry is one pending ask, as an agent sees it.
type InboxEntry struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Thread  string    `json:"thread,omitempty"`
	Created time.Time `json:"created"`
	Text    string    `json:"text"`
}

func inboxEntry(it Item) InboxEntry {
	text := it.Title
	if it.Kind == "question" {
		var qs []string
		for _, q := range it.Questions {
			qs = append(qs, q.Question)
		}
		text = strings.Join(qs, " / ")
	}
	return InboxEntry{ID: it.ID, Kind: it.Kind, Thread: it.Thread, Created: it.Created, Text: text}
}

// pendingAsks returns a project's pending asks, oldest first; a thread's own only
// when thread is set.
func (s *Server) pendingAsks(project, thread string) []Item {
	var out []Item
	for _, it := range s.store.Items(project) {
		if it.State == StatePending && isAsk(it) && (thread == "" || it.Thread == thread) {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// agentInbox lists what's waiting on the user from the caller (the coordinator
// sees its whole project).
func (s *Server) agentInbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origin Origin `json:"origin"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	project, thread, ok := s.resolve(w, body.Origin)
	if !ok {
		return
	}
	out := []InboxEntry{}
	for _, it := range s.pendingAsks(project, thread) {
		out = append(out, inboxEntry(it))
	}
	writeJSONResponse(w, map[string]any{"project": project, "items": out})
}

func inboxText(items []Item, now time.Time) string {
	var b strings.Builder
	for _, it := range items {
		e := inboxEntry(it)
		who := "you"
		if e.Thread != "" {
			who = "thread " + e.Thread
		}
		text := e.Text
		if len(text) > 160 {
			text = text[:157] + "…"
		}
		fmt.Fprintf(&b, "- %s %s from %s, %s ago: %s\n", e.ID, e.Kind, who, roughAgo(now.Sub(e.Created)), text)
	}
	return b.String()
}

// refreshProject is the user pulling to refresh a project page: the coordinator
// is asked to bring its page up to date now, and each thread whose summary is
// old to refresh it. Repeated pulls within a few minutes don't ask again.
func (s *Server) refreshProject(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.projects.Get(r.PathValue("slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.recMu.Lock()
	view, drifts, st := s.pageLocked(pr.Slug)
	now := time.Now().UTC()
	if now.Sub(st.RequestedAt) < refreshGap {
		s.recMu.Unlock()
		writeJSONResponse(w, map[string]any{"asked": false, "reason": "asked moments ago"})
		return
	}
	st.RequestedAt, st.NudgedAt, st.Nudged, st.Asked = now, now, driftKeys(drifts), true
	s.store.setReconcileState(pr.Slug, st)
	s.recMu.Unlock()

	msg := "The user pulled to refresh this project's page in Sidekick and wants it current now. As soon as you're between steps, bring it up to date; don't drop other work for longer than that.\n\n" +
		s.reconcileMessage(pr.Slug, view, drifts)
	if err := s.notice(pr.Slug, msg); err != nil {
		log.Printf("sidekick: refresh %s: %v", pr.Slug, err)
		http.Error(w, "couldn't reach the coordinator: "+err.Error(), http.StatusBadGateway)
		return
	}
	threads := 0
	for _, t := range s.projects.Threads(pr.Slug) {
		if st, ok := s.store.Status(threadKey(pr.Slug, t.ID)); ok && now.Sub(st.Updated) < threadSummaryFresh {
			continue
		}
		if err := s.noticeTo(pr.Slug, t.ID, "The user refreshed the project page in Sidekick, and your thread's summary there is out of date. Between steps, update it: `sidekick status \"<one-line headline>\" -summary \"<2-3 plain sentences: where this thread stands, what's next, anything blocking>\" [-link <PR>]`. Then carry on."); err == nil {
			threads++
		}
	}
	s.store.Publish(pr.Slug)
	writeJSONResponse(w, map[string]any{"asked": true, "threads": threads})
}

const (
	refreshGap         = 3 * time.Minute
	threadSummaryFresh = 30 * time.Minute
	checkingFor        = 20 * time.Minute // how long the app shows "checking" without an answer
)
