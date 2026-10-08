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

// A photo sent with a message is stored on this machine and its path is typed into
// the agent's pane, so the agent can open it.
func TestMessageWithAttachment(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hp")
	os.MkdirAll(filepath.Join(root, "acme", ".state"), 0o755)
	os.WriteFile(filepath.Join(root, "acme", "PROJECT.md"), []byte("+++\nname = \"Acme\"\n+++\n"), 0o644)
	typed := filepath.Join(dir, "typed.txt")
	fake := filepath.Join(dir, "fakehp")
	os.WriteFile(fake, []byte("#!/bin/sh\ncat >> "+typed+"\n"), 0o755)

	store, _ := OpenStore(filepath.Join(dir, "data"))
	srv := &Server{store: store, projects: &Projects{root: root}, deliver: &Deliverer{store: store, hpBin: fake},
		artifactURL: "https://host:7444", leases: map[string]int{}}
	api := httptest.NewServer(srv.userMux())
	defer api.Close()

	photo := []byte("\x89PNG fake image bytes")
	resp, err := http.Post(api.URL+"/api/uploads?project=acme&name=../../etc/My%20Screen%20shot.png", "image/png", bytes.NewReader(photo))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("upload: %v %v", err, resp.Status)
	}
	var att Attachment
	json.NewDecoder(resp.Body).Decode(&att)
	if att.Name != "My Screen shot.png" || att.Type != "image/png" || !strings.HasPrefix(att.Path, filepath.Join(dir, "data", "uploads", "acme")) {
		t.Fatalf("attachment %+v: the name must be cleaned and the file kept under uploads/acme", att)
	}
	if got, _ := os.ReadFile(att.Path); !bytes.Equal(got, photo) {
		t.Fatal("stored bytes differ")
	}

	body, _ := json.Marshal(map[string]any{"project": "acme", "text": "Make it look like this", "attachments": []string{att.ID}})
	resp, err = http.Post(api.URL+"/api/messages", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("message: %v %v", err, resp.Status)
	}
	var it Item
	json.NewDecoder(resp.Body).Decode(&it)
	if len(it.Attachments) != 1 || it.Attachments[0].URL != "https://host:7444/uploads/acme/"+att.ID+"/My Screen shot.png" {
		t.Fatalf("message attachments %+v", it.Attachments)
	}
	prompt, _ := os.ReadFile(typed)
	if !strings.Contains(string(prompt), att.Path) || !strings.Contains(string(prompt), "Make it look like this") {
		t.Fatalf("the agent's prompt must carry the file's path:\n%s", prompt)
	}

	// Another project's upload can't be attached.
	body, _ = json.Marshal(map[string]any{"project": "acme", "text": "x", "attachments": []string{"20260101t000000-deadbeef"}})
	if resp, _ := http.Post(api.URL+"/api/messages", "application/json", bytes.NewReader(body)); resp.StatusCode == 200 {
		t.Error("an unknown attachment was accepted")
	}
}

// Chatting back and forth is one inbox row per conversation; questions keep their own.
func TestInboxGroupsConversations(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hp")
	os.MkdirAll(filepath.Join(root, "acme", ".state"), 0o755)
	os.WriteFile(filepath.Join(root, "acme", "PROJECT.md"), []byte("+++\nname = \"Acme\"\n+++\n"), 0o644)
	store, _ := OpenStore(filepath.Join(dir, "data"))
	srv := &Server{store: store, projects: &Projects{root: root}, leases: map[string]int{}}
	reply := func(thread, text string) {
		it := &Item{Project: "acme", Thread: thread, Kind: "message", Text: "q", Reply: text}
		store.Add(it)
		store.Close(it.ID, "message", StateReplied, func(it *Item) { it.Reply = text })
		time.Sleep(2 * time.Millisecond) // distinct creation times
	}
	reply("", "first")
	reply("", "second")
	reply("", "third")
	reply("t-1", "thread reply")
	store.Add(&Item{Project: "acme", Kind: "question", Questions: []Question{{Question: "Which?"}}})

	rec := httptest.NewRecorder()
	srv.inbox(rec, httptest.NewRequest("GET", "/api/inbox", nil))
	var rows []inboxItem
	json.NewDecoder(rec.Body).Decode(&rows)
	got := map[string]int{}
	var coordinatorReply string
	for _, r := range rows {
		got[r.Kind+"/"+r.Thread]++
		if r.Kind == "message" && r.Thread == "" {
			coordinatorReply = r.Reply
			if r.Unread != 3 {
				t.Errorf("coordinator conversation unread = %d, want 3", r.Unread)
			}
		}
	}
	if got["message/"] != 1 || got["message/t-1"] != 1 || got["question/"] != 1 {
		t.Fatalf("rows %v: want one coordinator conversation, one thread conversation, one question", got)
	}
	if coordinatorReply != "third" {
		t.Errorf("conversation row shows %q, want the newest reply", coordinatorReply)
	}
	if n := srv.pendingCount(); n != 3 {
		t.Errorf("badge = %d, want 3 (two conversations, one question)", n)
	}
}
