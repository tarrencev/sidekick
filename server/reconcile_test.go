package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconcileAppliesFacts(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	pr1, pr2 := "https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2"
	plan := &Plan{
		Updated: now.Add(-20 * time.Minute),
		Work: []PlanWork{
			{Title: "Resolved work", Stage: "building", Thread: "t-1"},
			{Title: "Merged work", Stage: "review", PR: pr1},
			{Title: "Ghost", Stage: "building", Thread: "t-9"},
			{Title: "Old done", Stage: "done"},
			{Title: "Recent done", Stage: "done"},
			{Title: "Live", Stage: "building", Thread: "t-2"},
		},
		MergeOrder: []PlanMerge{{PR: pr1, Title: "one"}, {PR: pr2, Title: "two"}},
	}
	st := &reconcileState{DoneSince: map[string]time.Time{
		"Old done":    now.Add(-4 * 24 * time.Hour),
		"Recent done": now.Add(-30 * time.Hour),
	}}
	f := pageFacts{
		Now:  now,
		Plan: plan,
		Threads: map[string]ThreadFact{
			"t-1": {ID: "t-1", Status: "resolved", Reason: "merged", Changed: now.Add(-time.Hour)},
			"t-2": {ID: "t-2", Status: "open", Launched: now.Add(-time.Hour)},
			"t-3": {ID: "t-3", Title: "New one", Status: "open", Group: "working", Launched: now.Add(-time.Hour)},
			"t-4": {ID: "t-4", Status: "open", Launched: now.Add(-time.Minute)}, // too new to expect in the plan
		},
		PRs:   map[string]PRStatus{pr1: {State: "merged"}, pr2: {State: "open"}},
		State: st,
	}
	view, drifts := reconcile(f)

	byTitle := map[string]PlanWork{}
	for _, w := range view.Work {
		byTitle[w.Title] = w
	}
	if w := byTitle["Resolved work"]; w.Stage != "done" || w.Auto != "thread resolved" {
		t.Errorf("resolved thread: %+v", w)
	}
	if w := byTitle["Merged work"]; w.Stage != "done" || w.Auto != "PR merged" {
		t.Errorf("merged PR: %+v", w)
	}
	if w := byTitle["Ghost"]; w.Auto != "thread not found" || w.Stage != "building" {
		t.Errorf("missing thread: %+v", w)
	}
	if _, ok := byTitle["Old done"]; ok {
		t.Error("done work older than 3 days should drop off")
	}
	if !byTitle["Recent done"].Faded {
		t.Error("done work older than a day should fade")
	}
	if byTitle["Resolved work"].Faded {
		t.Error("newly done work should not fade")
	}
	if len(view.MergeOrder) != 1 || view.MergeOrder[0].PR != pr2 {
		t.Errorf("merged PRs should leave the merge order: %+v", view.MergeOrder)
	}
	if len(view.Unplanned) != 1 || view.Unplanned[0].ID != "t-3" {
		t.Errorf("unplanned threads: %+v", view.Unplanned)
	}
	keys := strings.Join(driftKeys(drifts), " ")
	for _, want := range []string{"resolved:t-1", "merged:" + pr1, "gone:t-9", "unplanned:t-3", "no-status"} {
		if !strings.Contains(keys, want) {
			t.Errorf("missing drift %q in %s", want, keys)
		}
	}
	if strings.Contains(keys, "t-4") {
		t.Error("a brand new thread should get time to reach the plan")
	}
	if _, ok := st.DoneSince["Resolved work"]; !ok {
		t.Error("work seen done should be remembered")
	}
}

func TestReconcileQuietWhenCurrent(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	f := pageFacts{
		Now:     now,
		Plan:    &Plan{Updated: now.Add(-30 * time.Minute), Work: []PlanWork{{Title: "Live", Stage: "building", Thread: "t-2"}}},
		Status:  &Status{Headline: "ok", Updated: now.Add(-30 * time.Minute)},
		Threads: map[string]ThreadFact{"t-2": {ID: "t-2", Status: "open", Launched: now.Add(-time.Hour), Changed: now.Add(-5 * time.Minute)}},
		State:   &reconcileState{DoneSince: map[string]time.Time{}},
	}
	if _, drifts := reconcile(f); len(drifts) != 0 {
		t.Errorf("a current page should have no drift: %+v", drifts)
	}
	// Three hours later with threads still moving, the backstop asks for a check.
	f.Now = now.Add(3 * time.Hour)
	f.Threads["t-2"] = ThreadFact{ID: "t-2", Status: "open", Launched: now.Add(-time.Hour), Changed: f.Now.Add(-time.Minute)}
	_, drifts := reconcile(f)
	keys := strings.Join(driftKeys(drifts), " ")
	if !strings.Contains(keys, "plan-age:") || !strings.Contains(keys, "headline:") {
		t.Errorf("stale page should be flagged: %s", keys)
	}
	// Confirming resets the clock.
	f.State.Verified = f.Now
	if _, drifts := reconcile(f); len(drifts) != 0 {
		t.Errorf("a confirmed page should have no drift: %+v", drifts)
	}
}

func TestReconcileBlockedOnUser(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	planned := now.Add(-time.Hour)
	f := pageFacts{
		Now:     now,
		Plan:    &Plan{Updated: planned, Work: []PlanWork{{Title: "Spend", Stage: "blocked", WaitingOn: "user", Thread: "t-1"}}},
		Status:  &Status{Headline: "ok", Updated: now},
		Threads: map[string]ThreadFact{"t-1": {ID: "t-1", Status: "open", Launched: planned}},
		State:   &reconcileState{DoneSince: map[string]time.Time{}},
	}
	_, drifts := reconcile(f)
	if len(drifts) != 1 || !strings.HasPrefix(drifts[0].Key, "ask:Spend:") {
		t.Fatalf("blocked on the user with nothing in the inbox: %+v", drifts)
	}
	f.Items = []Item{{ID: "q1", Kind: "question", Thread: "t-1", State: StatePending}}
	view, drifts := reconcile(f)
	if len(drifts) != 0 || view.Work[0].Ask != "q1" {
		t.Errorf("a pending question unblocks it: %+v %+v", drifts, view.Work[0])
	}
	f.Items = []Item{{ID: "q1", Kind: "question", Thread: "t-1", State: StateAnswered, Closed: now.Add(-10 * time.Minute)}}
	_, drifts = reconcile(f)
	if len(drifts) != 1 || !strings.HasPrefix(drifts[0].Key, "answered:Spend:") {
		t.Errorf("an answer since the plan may have unblocked it: %+v", drifts)
	}
}

func TestPlanAuthoredDropsDerivedFields(t *testing.T) {
	p := Plan{Work: []PlanWork{{Title: "a", Stage: "done", Ask: "x", Auto: "PR merged", Faded: true}},
		Unplanned: []PlanThread{{ID: "t"}}, Checking: true, Stale: true, Checked: time.Now()}
	a := p.authored()
	if w := a.Work[0]; w.Ask != "" || w.Auto != "" || w.Faded {
		t.Errorf("work: %+v", w)
	}
	if a.Unplanned != nil || a.Checking || a.Stale || !a.Checked.IsZero() {
		t.Errorf("plan: %+v", a)
	}
	if p.Work[0].Auto == "" {
		t.Error("authored must not modify the original")
	}
}

// The coordinator is asked once about a difference; after it updates the page
// that difference is settled and it isn't asked again.
func TestReconcileNudgesOnce(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hp")
	os.MkdirAll(filepath.Join(root, "acme", "threads"), 0o755)
	os.WriteFile(filepath.Join(root, "acme", "PROJECT.md"), []byte("+++\nname = \"Acme\"\n+++\n"), 0o644)
	os.WriteFile(filepath.Join(root, "acme", "threads", "t-1.toml"), []byte("id = \"t-1\"\ntitle = \"Checkout\"\nstatus = \"resolved\"\nresolved_reason = \"merged\"\n"), 0o644)
	typed := filepath.Join(dir, "typed.txt")
	fake := filepath.Join(dir, "fakehp")
	os.WriteFile(fake, []byte("#!/bin/sh\ncat >> "+typed+"\n"), 0o755)

	store, _ := OpenStore(filepath.Join(dir, "data"))
	srv := &Server{store: store, projects: &Projects{root: root}, deliver: &Deliverer{store: store, hpBin: fake},
		prs: &PRWatcher{}, leases: map[string]int{}}
	store.SetStatus("acme", "", Status{Headline: "Checkout in review", State: "on-track"})
	writeJSON(store.planPath("acme"), Plan{Updated: time.Now().Add(-20 * time.Minute),
		Work: []PlanWork{{Title: "Checkout", Stage: "review", Thread: "t-1"}}})
	pr, _ := srv.projects.Get("acme")
	busy := map[string]time.Time{}

	srv.reconcileProject(pr, busy)
	got, _ := os.ReadFile(typed)
	if !strings.Contains(string(got), "[sidekick notice:") || !strings.Contains(string(got), "thread t-1 was resolved") ||
		!strings.Contains(string(got), "Headline (on-track") || !strings.Contains(string(got), "sidekick plan -confirm") {
		t.Fatalf("reconcile request:\n%s", got)
	}
	if view, _, _ := srv.page("acme"); !view.Checking || view.Work[0].Stage != "done" {
		t.Fatalf("the page should show done and that it's being checked: %+v", view)
	}

	os.Remove(typed)
	srv.reconcileProject(pr, busy)
	if _, err := os.Stat(typed); err == nil {
		t.Fatal("asked again within the gap")
	}

	srv.reconciled("acme", false) // the coordinator republished
	st := store.reconcileState("acme")
	st.NudgedAt = time.Now().Add(-2 * time.Hour)
	store.setReconcileState("acme", st)
	srv.reconcileProject(pr, busy)
	if _, err := os.Stat(typed); err == nil {
		t.Fatal("asked again about a settled difference")
	}
	if view, drifts, _ := srv.page("acme"); view.Checking || view.Stale || len(drifts) != 0 || view.Work[0].Stage != "done" {
		t.Fatalf("settled page: %+v %+v", view, drifts)
	}
}
