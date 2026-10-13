package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type inboxRig struct {
	srv         *Server
	app, agent  *httptest.Server
	typed       string
	coordinator Origin
	thread      Origin
	otherThread Origin
	t           *testing.T
}

func newInboxRig(t *testing.T) *inboxRig {
	dir := t.TempDir()
	root := filepath.Join(dir, "hp")
	os.MkdirAll(filepath.Join(root, "acme", ".state"), 0o755)
	os.MkdirAll(filepath.Join(root, "acme", "threads"), 0o755)
	os.WriteFile(filepath.Join(root, "acme", "PROJECT.md"), []byte("+++\nname = \"Acme\"\n+++\n"), 0o644)
	os.WriteFile(filepath.Join(root, "acme", ".state", "coordinator.json"), []byte(`{"session":"s","pane_id":"p0"}`), 0o644)
	for _, t := range []string{"t-1", "t-2"} {
		os.WriteFile(filepath.Join(root, "acme", "threads", t+".toml"), []byte("id = \""+t+"\"\ntitle = \"Work "+t+"\"\nstatus = \"open\"\npane_id = \"p-"+t+"\"\n"), 0o644)
	}
	typed := filepath.Join(dir, "typed.txt")
	fake := filepath.Join(dir, "fakehp")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"--- $*\" >> "+typed+"\ncat >> "+typed+"\n"), 0o755)
	store, _ := OpenStore(filepath.Join(dir, "data"))
	d := &Deliverer{store: store, hpBin: fake, queue: make(chan string, 16)}
	go d.run()
	srv := &Server{store: store, projects: &Projects{root: root}, deliver: d, prs: &PRWatcher{}, leases: map[string]int{}}
	r := &inboxRig{srv: srv, app: httptest.NewServer(srv.userMux()), agent: httptest.NewServer(srv.agentMux()), typed: typed,
		coordinator: Origin{Session: "s", Pane: "p0"}, thread: Origin{Session: "s", Pane: "p-t-1"}, otherThread: Origin{Session: "s", Pane: "p-t-2"}, t: t}
	t.Cleanup(func() { r.app.Close(); r.agent.Close() })
	return r
}

func (r *inboxRig) post(base, path string, body any) (*http.Response, map[string]any) {
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		r.t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func (r *inboxRig) ask(o Origin, questions ...string) string {
	var qs []Question
	for _, q := range questions {
		qs = append(qs, Question{Question: q, Options: []Option{{Label: "Yes"}, {Label: "No"}}})
	}
	_, out := r.post(r.agent.URL, "/v1/ask", map[string]any{"origin": o, "async": true, "questions": qs})
	return out["id"].(string)
}

func (r *inboxRig) state(id string) Item {
	it, _ := r.srv.store.Get(id)
	return it
}

// Asking the same thing again replaces the earlier ask; asking something else, or
// the same thing from another thread, doesn't.
func TestRepeatedAskReplacesOlder(t *testing.T) {
	r := newInboxRig(t)
	a := r.ask(r.thread, "Ship the spring banner?")
	b := r.ask(r.thread, "Use the warm photo?")
	c := r.ask(r.otherThread, "Ship the spring banner?")
	d := r.ask(r.thread, "Ship the  spring banner ?", "Use the warm photo?")
	for _, id := range []string{a, b} {
		if it := r.state(id); it.State != StateCancelled || it.Reason == "" {
			t.Errorf("%s should be replaced: %+v", id, it)
		}
	}
	if r.state(c).State != StatePending || r.state(d).State != StatePending {
		t.Error("other thread's ask and the new ask stay pending")
	}
	e := r.ask(r.coordinator, "Pick a launch date")
	f := r.ask(r.coordinator, "Pick the launch date for the spring sale")
	_, out := r.post(r.agent.URL, "/v1/ask", map[string]any{"origin": r.coordinator, "async": true, "replaces": f,
		"questions": []Question{{Question: "Launch on Friday or Monday?"}}})
	if r.state(e).State != StatePending || r.state(f).State != StateCancelled || out["state"] != StatePending {
		t.Errorf("-replaces withdraws only the named ask: e=%s f=%s", r.state(e).State, r.state(f).State)
	}
}

// Dismissing tells the agent, with a verifiable tag.
func TestDismissTellsAgent(t *testing.T) {
	r := newInboxRig(t)
	id := r.ask(r.thread, "Ship the spring banner?")
	resp, _ := r.post(r.app.URL, "/api/items/"+id+"/dismiss", map[string]string{"reason": "Decided in chat"})
	if resp.StatusCode != 200 || r.state(id).State != StateDismissed {
		t.Fatalf("dismiss: %s %+v", resp.Status, r.state(id))
	}
	deadline := time.Now().Add(5 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		if got, _ = os.ReadFile(r.typed); strings.Contains(string(got), "dismissed") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(got), "[sidekick verdict:"+id+"]") || !strings.Contains(string(got), "Decided in chat") || !strings.Contains(string(got), "thread prompt acme t-1") {
		t.Fatalf("the thread should be told:\n%s", got)
	}
	_, v := r.post(r.agent.URL, "/v1/verify", map[string]string{"kind": "verdict", "id": id})
	if v["verified"] != true {
		t.Error("the dismissal tag should verify")
	}
	if resp, _ := r.post(r.app.URL, "/api/items/"+id+"/dismiss", nil); resp.StatusCode == 200 {
		t.Error("dismissing twice should fail")
	}
}

// A thread withdraws its own asks; the coordinator any in its project.
func TestWithdraw(t *testing.T) {
	r := newInboxRig(t)
	mine := r.ask(r.thread, "Ship it?")
	theirs := r.ask(r.otherThread, "Hold it?")
	if resp, _ := r.post(r.agent.URL, "/v1/items/"+theirs+"/withdraw", map[string]any{"origin": r.thread}); resp.StatusCode != 404 {
		t.Errorf("a thread can't withdraw another's ask: %s", resp.Status)
	}
	if resp, _ := r.post(r.agent.URL, "/v1/items/"+mine+"/withdraw", map[string]any{"origin": r.thread, "reason": "moot"}); resp.StatusCode != 200 {
		t.Errorf("withdraw own: %s", resp.Status)
	}
	if resp, _ := r.post(r.agent.URL, "/v1/items/"+theirs+"/withdraw", map[string]any{"origin": r.coordinator}); resp.StatusCode != 200 {
		t.Errorf("coordinator withdraws a thread's ask: %s", resp.Status)
	}
	if it := r.state(mine); it.State != StateCancelled || it.Reason != "moot" || !it.Delivered {
		t.Errorf("withdrawn: %+v", it)
	}
	_, out := r.post(r.agent.URL, "/v1/inbox", map[string]any{"origin": r.coordinator})
	if items := out["items"].([]any); len(items) != 0 {
		t.Errorf("inbox should be empty: %v", items)
	}
}

// Pulling to refresh asks the coordinator (with the pending inbox) and threads
// with old summaries, once per few minutes, and the page shows it's checking.
func TestRefreshAsksCoordinatorAndThreads(t *testing.T) {
	r := newInboxRig(t)
	r.ask(r.thread, "Ship the spring banner?")
	r.srv.store.SetStatus("acme", "t-2", Status{Headline: "fresh"})
	resp, out := r.post(r.app.URL, "/api/projects/acme/refresh", nil)
	if resp.StatusCode != 200 || out["asked"] != true || out["threads"] != float64(1) {
		t.Fatalf("refresh: %s %v", resp.Status, out)
	}
	got, _ := os.ReadFile(r.typed)
	s := string(got)
	if !strings.Contains(s, "pulled to refresh") || !strings.Contains(s, "Ship the spring banner?") || !strings.Contains(s, "sidekick withdraw") {
		t.Errorf("coordinator message:\n%s", s)
	}
	if !strings.Contains(s, "thread prompt acme t-1") || strings.Contains(s, "thread prompt acme t-2") {
		t.Errorf("only the thread with an old summary is asked:\n%s", s)
	}
	resp, err := http.Get(r.app.URL + "/api/projects/acme")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	json.NewDecoder(resp.Body).Decode(&d)
	if d["checking"] != true {
		t.Error("the page should say it's checking")
	}
	if _, out := r.post(r.app.URL, "/api/projects/acme/refresh", nil); out["asked"] != false {
		t.Error("a second pull right away shouldn't ask again")
	}
	r.srv.reconciled("acme", false)
	if _, _, st := r.srv.page("acme"); st.checking(time.Now()) {
		t.Error("an update ends the check")
	}
}

func TestSimilarAsks(t *testing.T) {
	q := func(s string) Item { return Item{Kind: "question", Questions: []Question{{Question: s}}} }
	if !similar(q("Approve the two colour runs for the tumbler and notebook?"), q("Approve both colour runs for the tumbler and the notebook?")) {
		t.Error("rewordings should match")
	}
	if similar(q("Approve the colour runs for the tumbler?"), q("Which margin should the consumer workspace charge?")) {
		t.Error("different asks shouldn't match")
	}
}
