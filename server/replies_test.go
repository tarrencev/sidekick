package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplyInTranscript(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"earlier"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"old answer"}]}}`,
		`{"type":"user","message":{"role":"user","content":"[sidekick msg:abc] The user wrote...\n\nhow is CI?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me check."},{"type":"tool_use","name":"Bash"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"CI is green on #2803."}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Merging next."}]}}`,
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644)
	if got, want := replyInTranscript(p, messageMarker("abc")), "CI is green on #2803.\n\nMerging next."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	queued := strings.Join([]string{
		`{"type":"queue-operation","operation":"enqueue","content":"[sidekick msg:q1] hi"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"finishing other work"}]}}`,
		`{"attachment":{"type":"queued_command","prompt":"[sidekick msg:q1] The user wrote...\n\nhi"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hi! All good."}]}}`,
		`{"type":"user","message":{"role":"user","content":"next request"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"unrelated later turn"}]}}`,
	}, "\n")
	os.WriteFile(p, []byte(queued), 0o644)
	if got, want := replyInTranscript(p, messageMarker("q1")), "Hi! All good."; got != want {
		t.Fatalf("queued: got %q, want %q", got, want)
	}
	if got := replyInTranscript(p, messageMarker("zzz")); got != "" {
		t.Fatalf("unknown marker: got %q", got)
	}
}

// Run on dl against a real transcript: REAL_TRANSCRIPT=… REAL_MARKER=… go test -run Real
func TestReplyInRealTranscript(t *testing.T) {
	p, m := os.Getenv("REAL_TRANSCRIPT"), os.Getenv("REAL_MARKER")
	if p == "" {
		t.Skip("REAL_TRANSCRIPT not set")
	}
	t.Logf("reply:\n%s", replyInTranscript(p, m))
}
