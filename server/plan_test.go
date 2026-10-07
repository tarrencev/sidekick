package main

import "testing"

func TestWaitsOnUser(t *testing.T) {
	cases := []struct {
		w    PlanWork
		want bool
	}{
		{PlanWork{Stage: "blocked", WaitingOn: "user"}, true},
		{PlanWork{Stage: "blocked", WaitingOn: "CI"}, false},
		{PlanWork{Stage: "blocked", Title: "Rollout proposal awaiting your verdict"}, true}, // legacy wording
		{PlanWork{Stage: "blocked", Title: "Waiting on CI"}, false},
		{PlanWork{Stage: "building", WaitingOn: "user"}, false},
	}
	for _, c := range cases {
		if got := c.w.waitsOnUser(); got != c.want {
			t.Errorf("%+v: got %v", c.w, got)
		}
	}
	p := Plan{Work: []PlanWork{{Title: "x", Stage: "blocked"}}}
	if p.validate() == nil {
		t.Error("blocked work without waitingOn should be rejected")
	}
}
