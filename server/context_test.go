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
)

func TestQuestionContext(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "hp")
	os.MkdirAll(filepath.Join(root, "acme", ".state"), 0o755)
	os.WriteFile(filepath.Join(root, "acme", "PROJECT.md"), []byte("+++\nname = \"Acme\"\n+++\n"), 0o644)
	os.WriteFile(filepath.Join(root, "acme", ".state", "coordinator.json"), []byte(`{"session":"s","pane_id":"w1:p1"}`), 0o644)
	store, _ := OpenStore(filepath.Join(dir, "data"))
	srv := &Server{store: store, projects: &Projects{root: root}, artifactURL: "https://host:7444", leases: map[string]int{}}
	agent := httptest.NewServer(srv.agentMux())
	defer agent.Close()
	post := func(path string, body any) (*http.Response, map[string]any) {
		b, _ := json.Marshal(body)
		resp, _ := http.Post(agent.URL+path, "application/json", bytes.NewReader(b))
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	origin := Origin{Session: "s", Pane: "w1:p1"}
	page := writeSite(t, map[string]string{"a.png": "x", "b.png": "x",
		"index.html": `<h1>Two logo options</h1>` + goodText + `<img src="a.png"><img src="b.png">`})
	question := map[string]any{"origin": origin, "async": true, "questions": []map[string]any{{"question": "Which logo?"}}}

	// Published first, it attaches to the next question from the same pane only.
	if resp, out := post("/v1/context", map[string]any{"origin": origin, "path": page}); resp.StatusCode != 200 || !strings.HasPrefix(out["url"].(string), "https://host:7444/acme/") {
		t.Fatalf("context: %v %v", resp.Status, out)
	}
	_, first := post("/v1/ask", question)
	_, second := post("/v1/ask", question)
	if first["context"] == nil || second["context"] != nil {
		t.Fatalf("context should attach to exactly the next question: first=%v second=%v", first["context"], second["context"])
	}

	// Or passed with the question directly.
	withCtx := map[string]any{"origin": origin, "async": true, "context": page, "questions": []map[string]any{{"question": "Which logo?"}}}
	if _, out := post("/v1/ask", withCtx); out["context"] == nil {
		t.Fatal("-context didn't attach")
	}

	// The same rules as reviews: a folder of bare images is refused.
	bare := writeSite(t, map[string]string{"a.png": "x"})
	if resp, _ := post("/v1/context", map[string]any{"origin": origin, "path": bare}); resp.StatusCode == 200 {
		t.Error("a folder of images was accepted as context")
	}
}
