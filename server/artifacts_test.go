package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodText = `<p>This is the price update report for active products only. Before this change,
draft products showed up in the list and could be selected by mistake. Now they are hidden by
default and counted, and you can show them with a toggle. Please check that the toggle wording and
the Draft badge read clearly before I merge it.</p>`

func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	return dir
}

func TestArtifactRules(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		problem string // "" means it publishes
	}{
		{"folder of images, no page", map[string]string{"before.png": "x", "after.png": "x"}, "no index.html"},
		{"good page", map[string]string{"before.png": "x", "after.png": "x",
			"index.html": `<h1>Price report</h1>` + goodText + `<img src="before.png"><img src="./after.png">`}, ""},
		{"images linked, not embedded", map[string]string{"before.png": "x",
			"index.html": `<h1>Price report</h1>` + goodText + `<a href="before.png">before</a>`}, "aren't shown on the page"},
		{"embeds a missing file", map[string]string{
			"index.html": `<h1>Price report</h1>` + goodText + `<img src="gone.png">`}, "aren't in the artifact"},
		{"just a list of links", map[string]string{"a.png": "x",
			"index.html": `<title>x</title><a href="a.png">a.png</a>`}, "words of explanation"},
		{"template left unfilled", map[string]string{
			"index.html": `<h1>[[Short title]]</h1>` + goodText}, "placeholder"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := writeSite(t, c.files)
			_, err := publishArtifact(src, filepath.Join(t.TempDir(), "out"))
			switch {
			case c.problem == "" && err != nil:
				t.Fatalf("rejected a good page: %v", err)
			case c.problem != "" && (err == nil || !strings.Contains(err.Error(), c.problem)):
				t.Fatalf("want rejection mentioning %q, got %v", c.problem, err)
			}
		})
	}

	// A bare image file is never an artifact on its own.
	img := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(img, []byte("x"), 0o644)
	if _, err := publishArtifact(img, filepath.Join(t.TempDir(), "out")); err == nil {
		t.Error("published a bare image")
	}
	// The shipped template, unfilled, is refused.
	page, _ := assets.ReadFile("assets/artifact-template.html")
	src := writeSite(t, map[string]string{"index.html": string(page)})
	if _, err := publishArtifact(src, filepath.Join(t.TempDir(), "out")); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("unfilled template: %v", err)
	}
}
