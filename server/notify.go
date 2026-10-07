package main

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// notifyReply alerts the phone that an agent answered a message.
func (s *Server) notifyReply(it Item) {
	reply := it
	reply.Kind = "reply"
	s.notifyPush(reply)
}

// notifyPush alerts the phone about a new inbox item or a reply: natively through
// APNs when it's configured, and through an ntfy topic when one is set.
func (s *Server) notifyPush(it Item) {
	name := it.Project
	if pr, ok := s.projects.Get(it.Project); ok {
		name = pr.Name
	}
	var subtitle, body string
	switch it.Kind {
	case "question":
		subtitle = "Question"
		if len(it.Questions) > 0 {
			body = it.Questions[0].Question
		}
	case "review":
		subtitle, body = "Review", it.Title
	case "proposal":
		subtitle, body = "Proposed thread", it.Title
	case "reply":
		subtitle, body = "Reply", it.Reply
	default:
		return
	}
	if it.Thread != "" {
		for _, t := range s.projects.Threads(it.Project) {
			if t.ID == it.Thread {
				subtitle += " · " + t.Title
			}
		}
	}
	if len(body) > 400 {
		body = body[:397] + "…"
	}
	if s.apns != nil {
		s.apns.Send(Push{Title: name, Subtitle: subtitle, Body: body, Item: it.ID, Project: it.Project, Thread: it.Thread, Kind: it.Kind, Badge: s.pendingCount()})
	}
	if s.notifyURL != "" {
		s.ntfy(name, subtitle+": "+body, it.Kind)
	}
}

// ntfy posts to an ntfy topic (https://ntfy.sh or self-hosted). Optional.
func (s *Server) ntfy(title, body, kind string) {
	req, err := http.NewRequest("POST", s.notifyURL, strings.NewReader(body))
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", map[string]string{"question": "question", "review": "eyes", "proposal": "bulb", "reply": "speech_balloon"}[kind])
	go func() {
		c := &http.Client{Timeout: 10 * time.Second}
		resp, err := c.Do(req)
		if err != nil {
			log.Printf("notify: %v", err)
			return
		}
		resp.Body.Close()
	}()
}
