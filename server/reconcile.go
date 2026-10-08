package main

// Keeping the project page true.
//
// The coordinator writes the plan and the headline, but nothing made sure they
// stayed right as threads finished and PRs merged. Two layers fix that:
//
//  1. Facts Sidekick can see are applied to what the user sees, at once and
//     without the agent: work whose thread was resolved or whose PR merged shows
//     as done, merged PRs leave the merge order, done work fades and then drops
//     off, and open threads the plan doesn't mention are listed.
//  2. What needs judgment goes to the coordinator. When the page differs from
//     what Sidekick observes, it sends one verified notice with the page as the
//     user sees it and the specific differences. It does that only when
//     something drifted, at most every 30 minutes per project, and when the
//     coordinator is idle. The coordinator republishes, or confirms the page is
//     right (`sidekick plan -confirm`), and those differences are settled.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	fadeDoneAfter  = 24 * time.Hour     // done work is shown dimmed after this
	dropDoneAfter  = 3 * 24 * time.Hour // and leaves the page after this
	newThreadGrace = 15 * time.Minute   // a new thread gets this long to reach the plan
	checkEvery     = 2 * time.Hour      // the page is re-checked at least this often while work moves
	nudgeGap       = 30 * time.Minute   // at most one reconcile request per project per this
	editGrace      = 10 * time.Minute   // a plan edited this recently is left alone
	busyPatience   = time.Hour          // a reconcile waits this long for a busy coordinator
)

// Drift is one way the page differs from what Sidekick observes. Keys identify a
// difference so the coordinator is asked about it once, not on every pass.
type Drift struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// reconcileState is the daemon's memory of a project's page, kept beside the plan.
type reconcileState struct {
	DoneSince map[string]time.Time `json:"doneSince,omitempty"` // work title -> first seen done
	Verified  time.Time            `json:"verified,omitzero"`   // the coordinator confirmed the page
	Acked     []string             `json:"acked,omitempty"`     // differences the coordinator has seen
	Nudged    []string             `json:"nudged,omitempty"`    // differences in the outstanding request
	NudgedAt  time.Time            `json:"nudgedAt,omitzero"`
	Attempts  int                  `json:"attempts,omitempty"`
}

func (s *Store) reconcilePath(project string) string {
	return filepath.Join(s.dir, "status", project+".reconcile.json")
}

func (s *Store) reconcileState(project string) reconcileState {
	var st reconcileState
	readJSON(s.reconcilePath(project), &st)
	if st.DoneSince == nil {
		st.DoneSince = map[string]time.Time{}
	}
	return st
}

func (s *Store) setReconcileState(project string, st reconcileState) {
	if err := writeJSON(s.reconcilePath(project), st); err != nil {
		log.Printf("sidekick: reconcile state for %s: %v", project, err)
	}
}

// pageFacts is everything the reconciler compares a plan against.
type pageFacts struct {
	Now     time.Time
	Plan    *Plan
	Status  *Status
	Threads map[string]ThreadFact
	PRs     map[string]PRStatus
	Items   []Item // the project's inbox items, newest first
	State   *reconcileState
}

// reconcile returns the plan as the user should see it, with what Sidekick can
// observe applied, and every way the coordinator's page has drifted. It records
// when work was first seen done in f.State.
func reconcile(f pageFacts) (*Plan, []Drift) {
	var drifts []Drift
	add := func(key, format string, args ...any) {
		drifts = append(drifts, Drift{Key: key, Text: fmt.Sprintf(format, args...)})
	}
	var open []ThreadFact
	for _, t := range f.Threads {
		if t.Status == "open" {
			open = append(open, t)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].ID < open[j].ID })
	activeSince := func(t time.Time) bool {
		for _, th := range f.Threads {
			if th.Changed.After(t) {
				return true
			}
		}
		for _, it := range f.Items {
			if it.Created.After(t) || it.Closed.After(t) {
				return true
			}
		}
		return false
	}

	var view *Plan
	if f.Plan == nil {
		if len(open) > 0 {
			add("no-plan", "There is no plan yet, but %d threads are open. Publish one with `sidekick plan`.", len(open))
		}
	} else {
		v := *f.Plan
		v.Work, v.MergeOrder = nil, nil
		mentioned := map[string]bool{}
		done := map[string]bool{}
		for _, w := range f.Plan.Work {
			mentioned[w.Thread], mentioned[w.PR] = true, true
			t, known := f.Threads[w.Thread]
			pr, hasPR := f.PRs[w.PR]
			if w.Stage != "done" {
				switch {
				case w.Thread != "" && !known:
					w.Auto = "thread not found"
					add("gone:"+w.Thread, "%q points at thread %s, which doesn't exist.", w.Title, w.Thread)
				case known && t.Status == "resolved":
					w.Stage, w.Auto = "done", "thread resolved"
					add("resolved:"+w.Thread, "%q: thread %s was resolved%s. Sidekick shows it as done.", w.Title, w.Thread, reasonNote(t.Reason))
				case hasPR && pr.State == "merged":
					w.Stage, w.Auto = "done", "PR merged"
					add("merged:"+w.PR, "%q: its PR %s merged. Sidekick shows it as done.", w.Title, w.PR)
				case hasPR && pr.State == "closed":
					w.Auto = "PR closed"
					add("closed:"+w.PR, "%q: its PR %s was closed without merging.", w.Title, w.PR)
				}
			}
			if w.Stage == "done" {
				done[w.Title] = true
				since, ok := f.State.DoneSince[w.Title]
				if !ok {
					since = f.Now
					f.State.DoneSince[w.Title] = since
				}
				if age := f.Now.Sub(since); age > dropDoneAfter {
					continue
				} else {
					w.Faded = age > fadeDoneAfter
				}
			}
			v.Work = append(v.Work, w)
		}
		for title := range f.State.DoneSince {
			if !done[title] {
				delete(f.State.DoneSince, title)
			}
		}

		for _, m := range f.Plan.MergeOrder {
			if st := f.PRs[m.PR].State; st == "merged" || st == "closed" {
				add(st+":"+m.PR, "%q (%s) is %s; Sidekick dropped it from the merge order.", m.Title, m.PR, st)
				continue
			}
			v.MergeOrder = append(v.MergeOrder, m)
		}

		// Work blocked on the user: link what would unblock it, or notice that
		// nothing would, or that the user has already answered.
		pending := map[string]string{} // thread ("" = coordinator) -> oldest pending item
		for _, it := range f.Items {
			if it.State == StatePending && isAsk(it) {
				pending[it.Thread] = it.ID
			}
		}
		for i, w := range v.Work {
			if !w.waitsOnUser() {
				continue
			}
			if id, ok := pending[w.Thread]; ok {
				v.Work[i].Ask = id
				continue
			} else if id, ok := pending[""]; ok && w.Thread != "" {
				v.Work[i].Ask = id
				continue
			}
			if it, ok := answeredSince(f.Items, w.Thread, f.Plan.Updated); ok {
				add("answered:"+w.Title+":"+it.ID, "%q is marked blocked on the user, but they answered %q %s ago, so it may be unblocked.", w.Title, itemLabel(it), roughAgo(f.Now.Sub(it.Closed)))
			} else if f.Now.Sub(f.Plan.Updated) > editGrace {
				add(fmt.Sprintf("ask:%s:%d", w.Title, f.Plan.Updated.Unix()),
					"%q is marked blocked waiting on the user, but nothing from you is in their Sidekick inbox, so they can't unblock it. Ask now: AskUserQuestion for decisions (recommended option first; never leave decisions inside a review page), `sidekick review` for something to look at, or `sidekick propose` for new work. If it isn't waiting on the user, set the right \"waitingOn\".", w.Title)
			}
		}

		for _, t := range open {
			if mentioned[t.ID] || (t.PR != "" && mentioned[t.PR]) || f.Now.Sub(t.Launched) < newThreadGrace {
				continue
			}
			v.Unplanned = append(v.Unplanned, PlanThread{ID: t.ID, Title: t.Title, Group: t.Group})
			add("unplanned:"+t.ID, "Open thread %s %q (%s) isn't in the plan.", t.ID, t.Title, orWord(t.Group, "open"))
		}

		v.Checked = latest(f.Plan.Updated, f.State.Verified)
		if len(open) > 0 && f.Now.Sub(v.Checked) > checkEvery && activeSince(v.Checked) {
			add(fmt.Sprintf("plan-age:%d", v.Checked.Unix()), "The plan was last updated or confirmed %s ago, and threads have moved since.", roughAgo(f.Now.Sub(v.Checked)))
		}
		view = &v
	}

	switch {
	case f.Status == nil:
		if len(open) > 0 {
			add("no-status", "There is no headline or summary yet. Set them with `sidekick status`.")
		}
	default:
		checked := latest(f.Status.Updated, f.State.Verified)
		if len(open) > 0 && f.Now.Sub(checked) > checkEvery && activeSince(checked) {
			add(fmt.Sprintf("headline:%d", checked.Unix()), "The headline and summary were last updated or confirmed %s ago, and threads have moved since.", roughAgo(f.Now.Sub(checked)))
		}
	}
	return view, drifts
}

func isAsk(it Item) bool {
	return it.Kind == "question" || it.Kind == "review" || it.Kind == "proposal"
}

// answeredSince finds an inbox item from the thread (or the coordinator) that the
// user settled after t.
func answeredSince(items []Item, thread string, t time.Time) (Item, bool) {
	for _, it := range items {
		if isAsk(it) && it.State != StatePending && it.State != StateCancelled && it.Closed.After(t) &&
			(it.Thread == thread || it.Thread == "") {
			return it, true
		}
	}
	return Item{}, false
}

func itemLabel(it Item) string {
	if it.Kind == "question" && len(it.Questions) > 0 {
		q := it.Questions[0].Question
		if len(q) > 80 {
			q = q[:77] + "…"
		}
		return q
	}
	return it.Title
}

func reasonNote(reason string) string {
	switch reason {
	case "":
		return ""
	case "merged":
		return " (its PR merged)"
	}
	return " (" + reason + ")"
}

func orWord(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func roughAgo(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", max(1, int(d.Minutes())))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// unsettled drops the differences the coordinator has already seen.
func unsettled(drifts []Drift, acked []string) []Drift {
	var out []Drift
	for _, d := range drifts {
		if !slices.Contains(acked, d.Key) {
			out = append(out, d)
		}
	}
	return out
}

func driftKeys(drifts []Drift) []string {
	keys := make([]string, len(drifts))
	for i, d := range drifts {
		keys[i] = d.Key
	}
	sort.Strings(keys)
	return keys
}

// page computes a project's page as the user should see it and its unsettled
// differences, and saves what it learned (when work was first seen done).
func (s *Server) page(project string) (*Plan, []Drift, reconcileState) {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	return s.pageLocked(project)
}

func (s *Server) pageLocked(project string) (*Plan, []Drift, reconcileState) {
	st := s.store.reconcileState(project)
	before, _ := json.Marshal(st)
	f := pageFacts{Now: time.Now().UTC(), Plan: s.store.Plan(project), Threads: s.projects.ThreadFacts(project),
		Items: s.store.Items(project), State: &st}
	if status, ok := s.store.Status(project); ok {
		f.Status = &status
	}
	if f.Plan != nil {
		var urls []string
		for _, w := range f.Plan.Work {
			urls = append(urls, w.PR)
			if t, ok := f.Threads[w.Thread]; ok {
				urls = append(urls, t.PR)
			}
		}
		for _, m := range f.Plan.MergeOrder {
			urls = append(urls, m.PR)
		}
		f.PRs = s.prs.Statuses(urls...)
	}
	view, drifts := reconcile(f)
	open := unsettled(drifts, st.Acked)
	// Forget settled differences that no longer apply, so the list stays short.
	var still []string
	for _, k := range st.Acked {
		if slices.ContainsFunc(drifts, func(d Drift) bool { return d.Key == k }) {
			still = append(still, k)
		}
	}
	st.Acked = still
	if view != nil {
		view.Stale = len(open) > 0
		view.Checking = len(st.Nudged) > 0 && st.NudgedAt.After(view.Checked)
	}
	if after, _ := json.Marshal(st); string(after) != string(before) {
		s.store.setReconcileState(project, st)
	}
	return view, open, st
}

// reconciled records that the coordinator updated its page (or, with confirm,
// said it is right as it stands): the differences it was told about, or with
// confirm all current ones, are settled.
func (s *Server) reconciled(project string, confirm bool) {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	_, open, st := s.pageLocked(project)
	st.Acked = append(st.Acked, st.Nudged...)
	if confirm {
		st.Acked = append(st.Acked, driftKeys(open)...)
		st.Verified = time.Now().UTC()
	}
	slices.Sort(st.Acked)
	st.Acked = slices.Compact(st.Acked)
	st.Nudged, st.Attempts = nil, 0
	s.store.setReconcileState(project, st)
	s.store.Publish(project)
}

// reconcileLoop is the backstop that keeps every project's page current.
func (s *Server) reconcileLoop() {
	busy := map[string]time.Time{} // project -> when a due reconcile first found the coordinator busy
	for {
		time.Sleep(2 * time.Minute)
		for _, pr := range s.projects.List() {
			if pr.Active {
				s.reconcileProject(pr, busy)
			}
		}
	}
}

func (s *Server) reconcileProject(pr Project, busy map[string]time.Time) {
	s.recMu.Lock()
	view, open, st := s.pageLocked(pr.Slug)
	s.recMu.Unlock()
	now := time.Now().UTC()
	if len(open) == 0 {
		delete(busy, pr.Slug)
		return
	}
	keys := driftKeys(open)
	wait := nudgeGap
	if slices.Equal(keys, st.Nudged) { // asked already and nothing changed: back off
		wait = nudgeGap << min(st.Attempts, 3)
	}
	if now.Sub(st.NudgedAt) < wait || (view != nil && now.Sub(view.Updated) < editGrace) {
		return
	}
	if s.coordinatorBusy(pr) {
		if _, ok := busy[pr.Slug]; !ok {
			busy[pr.Slug] = now
		}
		if now.Sub(busy[pr.Slug]) < busyPatience {
			return // wait for a quiet moment; it is queued as a prompt anyway after this
		}
	}
	delete(busy, pr.Slug)
	if err := s.notice(pr.Slug, s.reconcileMessage(pr.Slug, view, open)); err != nil {
		log.Printf("sidekick: reconcile %s: %v", pr.Slug, err)
		return
	}
	log.Printf("sidekick: asked %s to reconcile %d differences", pr.Slug, len(open))

	s.recMu.Lock()
	defer s.recMu.Unlock()
	cur := s.store.reconcileState(pr.Slug)
	if slices.Equal(keys, cur.Nudged) {
		cur.Attempts++
	} else {
		cur.Attempts = 1
	}
	cur.Nudged, cur.NudgedAt = keys, now
	s.store.setReconcileState(pr.Slug, cur)
	s.store.Publish(pr.Slug)
}

// coordinatorBusy reports whether the coordinator's agent is mid-turn.
func (s *Server) coordinatorBusy(pr Project) bool {
	c := pr.coordinator
	if c.PaneID == "" {
		return false
	}
	args := []string{"pane", "get", c.PaneID}
	if c.Session != "" {
		args = append([]string{"--session", c.Session}, args...)
	}
	out, err := exec.Command(s.deliver.herdr, args...).Output()
	if err != nil {
		return false
	}
	var v struct {
		Result struct {
			Pane struct {
				AgentStatus string `json:"agent_status"`
			} `json:"pane"`
		} `json:"result"`
	}
	return json.Unmarshal(out, &v) == nil && v.Result.Pane.AgentStatus == "working"
}

// reconcileMessage shows the coordinator its page as the user sees it and what
// differs, and asks for one update.
func (s *Server) reconcileMessage(project string, view *Plan, drifts []Drift) string {
	var b strings.Builder
	b.WriteString("Page check. Sidekick compared the project page the user sees in the app with what it observes, and it looks out of date.\n\nWhat changed:\n")
	for _, d := range drifts {
		fmt.Fprintf(&b, "- %s\n", d.Text)
	}
	b.WriteString("\nWhat the user sees now:\n")
	if st, ok := s.store.Status(project); ok {
		fmt.Fprintf(&b, "Headline (%s, updated %s ago): %s\n", st.State, roughAgo(time.Since(st.Updated)), st.Headline)
		if st.Summary != "" {
			fmt.Fprintf(&b, "Summary: %s\n", st.Summary)
		}
	} else {
		b.WriteString("Headline: (none)\n")
	}
	b.WriteString(planText(view))
	b.WriteString("\nReconcile it in one pass, without stopping other work: run `sidekick plan -show > plan.json`, edit it to match reality (stages, merge order, new or finished threads, focus), and publish it with `sidekick plan -f plan.json`. Update `sidekick status` too if the headline or summary changed. If everything above is already right, run `sidekick plan -confirm` so Sidekick stops asking.")
	return b.String()
}

func planText(p *Plan) string {
	if p == nil {
		return "Plan: (none)\n"
	}
	var b strings.Builder
	if p.Focus != "" {
		fmt.Fprintf(&b, "Focus: %s\n", p.Focus)
	}
	if len(p.Work) > 0 {
		b.WriteString("Priorities:\n")
		for i, w := range p.Work {
			fmt.Fprintf(&b, "%d. [%s] %s", i+1, w.Stage, w.Title)
			var extra []string
			if w.Thread != "" {
				extra = append(extra, "thread "+w.Thread)
			}
			if w.PR != "" {
				extra = append(extra, w.PR)
			}
			if w.WaitingOn != "" {
				extra = append(extra, "waiting on "+w.WaitingOn)
			}
			if w.Auto != "" {
				extra = append(extra, "Sidekick: "+w.Auto)
			}
			if len(extra) > 0 {
				fmt.Fprintf(&b, " (%s)", strings.Join(extra, ", "))
			}
			if w.Note != "" {
				fmt.Fprintf(&b, ": %s", w.Note)
			}
			b.WriteString("\n")
		}
	}
	if len(p.MergeOrder) > 0 {
		b.WriteString("Merge order:\n")
		for i, m := range p.MergeOrder {
			fmt.Fprintf(&b, "%d. %s %s\n", i+1, m.Title, m.PR)
		}
	}
	if len(p.Next) > 0 {
		b.WriteString("Up next:\n")
		for _, n := range p.Next {
			fmt.Fprintf(&b, "- %s\n", n.Title)
		}
	}
	if len(p.Unplanned) > 0 {
		b.WriteString("Open threads not in the plan:\n")
		for _, t := range p.Unplanned {
			fmt.Fprintf(&b, "- %s %s\n", t.ID, t.Title)
		}
	}
	return b.String()
}

// showPlan gives the coordinator its page as the user sees it, to edit and
// republish, and what differs.
func (s *Server) showPlan(w http.ResponseWriter, r *http.Request) {
	project, ok := s.coordinatorOnly(w, r)
	if !ok {
		return
	}
	view, drifts, _ := s.page(project)
	if drifts == nil {
		drifts = []Drift{}
	}
	writeJSONResponse(w, map[string]any{"project": project, "plan": view, "drift": drifts})
}

// confirmPlan records that the coordinator checked its page and it is right.
func (s *Server) confirmPlan(w http.ResponseWriter, r *http.Request) {
	project, ok := s.coordinatorOnly(w, r)
	if !ok {
		return
	}
	s.reconciled(project, true)
	writeJSONResponse(w, map[string]string{"project": project})
}

func (s *Server) coordinatorOnly(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Origin Origin `json:"origin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return "", false
	}
	project, thread, ok := s.resolve(w, body.Origin)
	if !ok {
		return "", false
	}
	if thread != "" {
		http.Error(w, "only the project coordinator keeps the project page", http.StatusForbidden)
		return "", false
	}
	return project, true
}
