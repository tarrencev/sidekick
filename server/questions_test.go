package main

import (
	"testing"
	"time"
)

// A daemon restart must never drop an async question from the inbox: nobody is
// waiting on it by design (regression: the spend question vanished on restart).
func TestRestartKeepsAsyncQuestions(t *testing.T) {
	leaseGrace = 50 * time.Millisecond
	defer func() { leaseGrace = 20 * time.Second }()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	async := &Item{Project: "p", Kind: "question", Async: true, Questions: []Question{{Question: "Spend more?"}}}
	blocking := &Item{Project: "p", Kind: "question", Questions: []Question{{Question: "Which DB?"}}}
	store.Add(async)
	store.Add(blocking)

	srv := &Server{store: store, leases: map[string]int{}}
	srv.resumeQuestions()
	time.Sleep(200 * time.Millisecond)

	if it, _ := store.Get(async.ID); it.State != StatePending {
		t.Errorf("async question is %s after restart, want pending", it.State)
	}
	if it, _ := store.Get(blocking.ID); it.State != StateCancelled {
		t.Errorf("blocking question with no waiter is %s, want cancelled", it.State)
	}
}
