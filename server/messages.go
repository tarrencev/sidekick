package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Messages are the user writing to an agent from the app. The text is typed into
// the agent's pane; the agent answers as it normally would, and its answer comes
// back as the message's Reply (captured by the Claude Stop hook, or sent with
// `sidekick reply` by agents without one).

// messageMarker tags a delivered message so its reply can be found in a transcript.
func messageMarker(id string) string { return "[sidekick msg:" + id + "]" }

func (s *Server) sendMessage(project, thread, text string) (Item, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Item{}, errors.New("text required")
	}
	if _, ok := s.projects.Get(project); !ok {
		return Item{}, fmt.Errorf("no project %q", project)
	}
	if thread != "" && !s.hasThread(project, thread) {
		return Item{}, fmt.Errorf("no open thread %s in %s", thread, project)
	}
	it := &Item{ID: newID(), Project: project, Thread: thread, Kind: "message", Text: text}
	if err := s.store.Add(it); err != nil {
		return Item{}, err
	}
	prompt := fmt.Sprintf("%s The user wrote this from the Sidekick app on their phone. Answer in this pane as you normally would; your reply is relayed to them. "+
		"If your answer proposes new work (a thread to start), don't just offer it in prose: file it with `sidekick propose` so it lands in their inbox to approve.\n\n%s",
		messageMarker(it.ID), text)
	if err := s.deliver.Prompt(project, thread, prompt); err != nil {
		who := "The coordinator"
		if thread != "" {
			who = "That thread"
		}
		msg := who + " couldn't take a message right now (" + err.Error() + ")"
		s.store.Close(it.ID, "message", StateFailed, func(it *Item) { it.Error = msg })
		return Item{}, errors.New(msg)
	}
	return *it, nil
}

func (s *Server) hasThread(project, thread string) bool {
	for _, t := range s.projects.Threads(project) {
		if t.ID == thread {
			return true
		}
	}
	return false
}

// messagesFor lists a conversation oldest first: one project's coordinator, or one thread.
func (s *Server) messagesFor(project, thread string) []Item {
	out := []Item{}
	for _, it := range s.store.Items(project) {
		if it.Kind == "message" && it.Thread == thread {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	if len(out) > 50 {
		out = out[len(out)-50:]
	}
	return out
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	writeJSONResponse(w, s.messagesFor(r.URL.Query().Get("project"), r.URL.Query().Get("thread")))
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
		Thread  string `json:"thread"`
		Text    string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	it, err := s.sendMessage(body.Project, body.Thread, body.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSONResponse(w, it)
}

// pendingMessages lists messages the calling pane hasn't answered yet.
func (s *Server) pendingMessages(w http.ResponseWriter, r *http.Request) {
	var o Origin
	json.NewDecoder(r.Body).Decode(&o)
	project, thread, ok := s.projects.Resolve(o)
	out := []Item{}
	if ok {
		for _, it := range s.messagesFor(project, thread) {
			if it.State == StatePending {
				out = append(out, it)
			}
		}
	}
	writeJSONResponse(w, out)
}

func (s *Server) replyMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		http.Error(w, "text required", http.StatusBadRequest)
		return
	}
	it, err := s.store.Close(r.PathValue("id"), "message", StateReplied, func(it *Item) {
		it.Reply = strings.TrimSpace(body.Text)
	})
	if !checkClose(w, r, err) {
		return
	}
	s.notifyReply(it)
	writeJSONResponse(w, it)
}

// markSeen records that the user has read a conversation's replies (it drops them
// from the inbox on every device).
func (s *Server) markSeen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
		Thread  string `json:"thread"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	n := 0
	for _, it := range s.messagesFor(body.Project, body.Thread) {
		if it.State == StateReplied && !it.Seen {
			s.store.Update(it.ID, func(it *Item) error { it.Seen = true; return nil })
			n++
		}
	}
	writeJSONResponse(w, map[string]int{"seen": n})
}

// unreadReply reports whether an item is an agent's reply the user hasn't read.
func unreadReply(it Item) bool { return it.Kind == "message" && it.State == StateReplied && !it.Seen }

// replyAge bounds how long after a message its reply is still looked for.
const replyAge = 6 * time.Hour
