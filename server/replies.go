package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// cmdReply answers a message from the app, for agents without the Stop hook (Codex).
func cmdReply(args []string) error {
	fs := flag.NewFlagSet("reply", flag.ExitOnError)
	id := fs.String("id", "", "message id (default: the oldest unanswered message for this pane)")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 {
		return errors.New(`usage: sidekick reply "<answer>" [-id <message id>]`)
	}
	ctx := context.Background()
	if *id == "" {
		var pending []Item
		if err := call(ctx, "POST", "/v1/messages/pending", currentOrigin(""), &pending); err != nil {
			return err
		}
		if len(pending) == 0 {
			return errors.New("no unanswered message from the user for this pane")
		}
		*id = pending[0].ID
	}
	if err := call(ctx, "POST", "/v1/messages/"+*id+"/reply", map[string]string{"text": fs.Arg(0)}, nil); err != nil {
		return err
	}
	fmt.Println("Sent to the user's phone.")
	return nil
}

// cmdStopHook is Claude Code's Stop hook: when a turn ends, any message from the
// app that this turn answered gets the turn's final text as its reply. It never
// blocks Claude and prints nothing.
func cmdStopHook() error {
	var in struct {
		TranscriptPath string `json:"transcript_path"`
		Cwd            string `json:"cwd"`
	}
	if json.NewDecoder(os.Stdin).Decode(&in) != nil || in.TranscriptPath == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var pending []Item
	if call(ctx, "POST", "/v1/messages/pending", currentOrigin(in.Cwd), &pending) != nil || len(pending) == 0 {
		return nil
	}
	for _, it := range pending {
		if time.Since(it.Created) > replyAge {
			continue
		}
		// The transcript can lag the hook by a moment; look twice.
		reply := ""
		for attempt := 0; attempt < 3 && reply == ""; attempt++ {
			if attempt > 0 {
				time.Sleep(400 * time.Millisecond)
			}
			reply = replyInTranscript(in.TranscriptPath, messageMarker(it.ID))
		}
		if reply != "" {
			call(ctx, "POST", "/v1/messages/"+it.ID+"/reply", map[string]string{"text": reply}, nil)
		}
	}
	return nil
}

// replyInTranscript finds the user turn carrying marker in a Claude Code JSONL
// transcript and returns the assistant's closing text after it: the text written
// after its last tool call. Empty if the agent hasn't answered yet.
func replyInTranscript(path, marker string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	type block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		// A prompt typed while the agent was busy is queued and reaches the
		// transcript as a queued_command attachment rather than a user message.
		Attachment struct {
			Type   string `json:"type"`
			Prompt string `json:"prompt"`
		} `json:"attachment"`
	}
	found := false
	var texts []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !found && !strings.Contains(string(line), marker) {
			continue
		}
		entry.Type, entry.Message.Content, entry.Attachment.Type, entry.Attachment.Prompt = "", nil, "", ""
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry.Attachment.Type == "queued_command" {
			if strings.Contains(entry.Attachment.Prompt, marker) {
				found, texts = true, nil
			} else if found && len(texts) > 0 {
				return strings.Join(texts, "\n\n") // the next prompt starts a new turn
			}
			continue
		}
		var blocks []block
		var plain string
		if json.Unmarshal(entry.Message.Content, &plain) == nil {
			blocks = []block{{Type: "text", Text: plain}}
		} else {
			json.Unmarshal(entry.Message.Content, &blocks)
		}
		switch entry.Type {
		case "user":
			prompt, ours := false, false
			for _, b := range blocks {
				if b.Type == "text" {
					prompt = true
					ours = ours || strings.Contains(b.Text, marker)
				}
			}
			if ours {
				found, texts = true, nil // the latest delivery of this message wins
			} else if prompt && found && len(texts) > 0 {
				return strings.Join(texts, "\n\n") // the next prompt starts a new turn
			}
		case "assistant":
			if !found {
				continue
			}
			for _, b := range blocks {
				switch b.Type {
				case "tool_use":
					texts = nil // narration before a tool call isn't the answer
				case "text":
					if t := strings.TrimSpace(b.Text); t != "" {
						texts = append(texts, t)
					}
				}
			}
		}
	}
	return strings.Join(texts, "\n\n")
}
