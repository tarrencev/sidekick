package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Everything sidekick installs on the agent machine ships inside the binary, so
// `sidekick doctor --fix` can restore the whole setup from just the binary.
//
//go:embed assets
var assets embed.FS

const (
	apiPort      = "7443" // tailnet https port for the app API
	artifactPort = "7444" // tailnet https port for artifacts (separate origin)
)

type check struct {
	name string
	run  func() (ok bool, detail string)
	fix  func() error
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fix := fs.Bool("fix", false, "repair anything that's missing or stale")
	fs.Parse(args)

	failed := 0
	for _, c := range doctorChecks() {
		ok, detail := c.run()
		if !ok && *fix && c.fix != nil {
			if err := c.fix(); err != nil {
				detail = fmt.Sprintf("%s; fix failed: %v", detail, err)
			} else {
				ok, detail = c.run()
				if ok {
					detail += " (fixed)"
				}
			}
		}
		mark := "✓"
		if !ok {
			mark = "✗"
			failed++
		}
		fmt.Printf("%s %-22s %s\n", mark, c.name, detail)
	}
	if failed > 0 {
		if !*fix {
			return fmt.Errorf("%d problem(s); run `sidekick doctor --fix`", failed)
		}
		return fmt.Errorf("%d problem(s) remain", failed)
	}
	return nil
}

func doctorChecks() []check {
	return []check{
		{"service", checkService, fixService},
		{"tailscale serve", checkServe, fixServe},
		{"daemon", checkDaemon, func() error { return run("systemctl", "--user", "restart", "sidekick") }},
		{"skills", checkSkills, installSkills},
		{"claude hooks", checkHook, installHook},
		{"project instructions", checkInstructions, installInstructions},
	}
}

// ---- service ----

func unitPath() string { return home(".config", "systemd", "user", "sidekick.service") }
func envPath() string  { return home(".config", "sidekick", "env") }

func checkService() (bool, string) {
	want, _ := assets.ReadFile("assets/sidekick.service")
	have, err := os.ReadFile(unitPath())
	if err != nil {
		return false, "unit not installed"
	}
	if !bytes.Equal(want, have) {
		return false, "unit is stale"
	}
	env, _ := os.ReadFile(envPath())
	if !regexp.MustCompile(`(?m)^SIDEKICK_ARTIFACT_URL=https://\S+`).Match(env) {
		return false, "SIDEKICK_ARTIFACT_URL missing from " + envPath()
	}
	if out, _ := exec.Command("systemctl", "--user", "is-enabled", "sidekick").Output(); strings.TrimSpace(string(out)) != "enabled" {
		return false, "not enabled"
	}
	return true, "enabled, unit current"
}

func fixService() error {
	unit, _ := assets.ReadFile("assets/sidekick.service")
	if err := writeFile(unitPath(), unit); err != nil {
		return err
	}
	env, _ := os.ReadFile(envPath())
	if !strings.Contains(string(env), "SIDEKICK_ARTIFACT_URL=") {
		dns, err := tailnetName()
		if err != nil {
			return err
		}
		env = append(env, []byte(fmt.Sprintf("SIDEKICK_ARTIFACT_URL=https://%s:%s\n", dns, artifactPort))...)
	}
	if !strings.Contains(string(env), "SIDEKICK_NOTIFY_URL=") {
		env = append(env, []byte("SIDEKICK_NOTIFY_URL=\n")...)
	}
	if err := writeFile(envPath(), env); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "enable", "sidekick"); err != nil {
		return err
	}
	return run("systemctl", "--user", "restart", "sidekick")
}

func tailnetName() (string, error) {
	out, err := exec.Command("tailscale", "status", "--self", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	var st struct{ Self struct{ DNSName string } }
	if err := json.Unmarshal(out, &st); err != nil || st.Self.DNSName == "" {
		return "", errors.New("can't read this machine's tailnet name")
	}
	return strings.TrimSuffix(st.Self.DNSName, "."), nil
}

// ---- tailscale serve ----

func checkServe() (bool, string) {
	out, err := exec.Command("tailscale", "serve", "status", "--json").Output()
	if err != nil {
		return false, "tailscale serve status failed"
	}
	s := string(out)
	var missing []string
	for _, p := range []string{":" + apiPort + `"`, ":" + artifactPort + `"`} {
		if !strings.Contains(s, p) {
			missing = append(missing, strings.Trim(p, `:"`))
		}
	}
	if len(missing) > 0 {
		return false, "not serving port(s) " + strings.Join(missing, ", ")
	}
	return true, "https :" + apiPort + " (app), :" + artifactPort + " (artifacts)"
}

func fixServe() error {
	if err := run("tailscale", "serve", "--bg", "--https="+apiPort, "http://127.0.0.1:7600"); err != nil {
		return err
	}
	return run("tailscale", "serve", "--bg", "--https="+artifactPort, "http://127.0.0.1:7601")
}

// ---- daemon ----

func checkDaemon() (bool, string) {
	conn, err := net.DialTimeout("unix", defaultSocket(), 2*time.Second)
	if err != nil {
		return false, "agent socket unreachable: " + err.Error()
	}
	conn.Close()
	return true, "listening on " + defaultSocket()
}

// ---- skills ----

func skillNames() []string {
	entries, _ := assets.ReadDir("assets/skills")
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// skillLinkDirs are the per-agent skill folders that should link to ~/.agents/skills.
func skillLinkDirs() []string {
	var dirs []string
	for _, d := range []string{home(".claude", "skills"), home(".codex", "skills")} {
		if _, err := os.Stat(filepath.Dir(d)); err == nil {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func checkSkills() (bool, string) {
	var problems []string
	for _, name := range skillNames() {
		want, _ := assets.ReadFile("assets/skills/" + name + "/SKILL.md")
		have, err := os.ReadFile(home(".agents", "skills", name, "SKILL.md"))
		if err != nil {
			problems = append(problems, name+" missing")
			continue
		}
		if !bytes.Equal(want, have) {
			problems = append(problems, name+" stale")
		}
		for _, d := range skillLinkDirs() {
			target, err := os.Readlink(filepath.Join(d, name))
			if err != nil || target != home(".agents", "skills", name) {
				problems = append(problems, fmt.Sprintf("%s not linked in %s", name, d))
			}
		}
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, "; ")
	}
	return true, strings.Join(skillNames(), ", ") + " for " + strings.Join(agentNames(), " and ")
}

func agentNames() []string {
	var out []string
	for _, d := range skillLinkDirs() {
		out = append(out, strings.TrimPrefix(filepath.Base(filepath.Dir(d)), "."))
	}
	return out
}

func installSkills() error {
	return fs.WalkDir(assets, "assets/skills", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "assets/skills/")
		b, _ := assets.ReadFile(p)
		if err := writeFile(home(".agents", "skills", rel), b); err != nil {
			return err
		}
		name := strings.SplitN(rel, "/", 2)[0]
		for _, dir := range skillLinkDirs() {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			link := filepath.Join(dir, name)
			if t, err := os.Readlink(link); err == nil && t == home(".agents", "skills", name) {
				continue
			}
			os.RemoveAll(link)
			if err := os.Symlink(home(".agents", "skills", name), link); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- Claude hooks ----

type claudeHook struct {
	event, matcher, arg string
	timeout             int
	purpose             string
}

var claudeHooks = []claudeHook{
	{"PreToolUse", "AskUserQuestion", "claude-ask", 30, "questions go to the app"},
	{"Stop", "", "claude-stop", 30, "replies reach the app"},
	{"UserPromptSubmit", "", "claude-prompt", 10, "app messages are verified"},
}

func hookCommand(arg string) string { return home(".local", "bin", "sidekick") + " hook " + arg }

func claudeSettingsPath() string { return home(".claude", "settings.json") }

func checkHook() (bool, string) {
	b, err := os.ReadFile(claudeSettingsPath())
	if err != nil {
		return false, "can't read " + claudeSettingsPath()
	}
	var ok, missing []string
	for _, h := range claudeHooks {
		if strings.Contains(string(b), `"`+hookCommand(h.arg)+`"`) {
			ok = append(ok, h.purpose)
		} else {
			missing = append(missing, h.event+" ("+h.purpose+")")
		}
	}
	if len(missing) > 0 {
		return false, "missing " + strings.Join(missing, ", ")
	}
	return true, strings.Join(ok, "; ")
}

func installHook() error {
	path := claudeSettingsPath()
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := json.Unmarshal(b, &settings); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	for _, h := range claudeHooks {
		if strings.Contains(string(b), `"`+hookCommand(h.arg)+`"`) {
			continue
		}
		entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(h.arg), "timeout": h.timeout}}}
		if h.matcher != "" {
			entry["matcher"] = h.matcher
		}
		list, _ := hooks[h.event].([]any)
		hooks[h.event] = append(list, entry)
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false) // keep shell redirects like 2>/dev/null readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return err
	}
	backupOnce(path)
	return writeFile(path, out.Bytes())
}

// ---- herdr-projects instructions ----

var instructionBlock = regexp.MustCompile(`(?s)\n*## Sidekick\n+<!-- sidekick:start.*?<!-- sidekick:end -->\n?`)

func projectFiles() []string {
	files, _ := filepath.Glob(home(".herdr-projects", "*", "PROJECT.md"))
	return files
}

func withInstructions(text string) string {
	snippet, _ := assets.ReadFile("assets/project-instructions.md")
	return strings.TrimRight(instructionBlock.ReplaceAllString(text, ""), "\n") + "\n" + string(snippet)
}

func checkInstructions() (bool, string) {
	var stale []string
	files := projectFiles()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil || withInstructions(string(b)) != string(b) {
			stale = append(stale, filepath.Base(filepath.Dir(f)))
		}
	}
	if len(stale) > 0 {
		return false, "missing or stale in " + strings.Join(stale, ", ")
	}
	return true, fmt.Sprintf("current in %d project(s)", len(files))
}

func installInstructions() error {
	for _, f := range projectFiles() {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if next := withInstructions(string(b)); next != string(b) {
			backupOnce(f)
			if err := writeFile(f, []byte(next)); err != nil {
				return err
			}
			log.Printf("sidekick: updated instructions in %s", f)
		}
	}
	return nil
}

// keepAgentsBriefed re-applies the skills and project instructions periodically,
// so projects created later are onboarded without anyone running doctor.
func keepAgentsBriefed() {
	for {
		if ok, _ := checkSkills(); !ok {
			if err := installSkills(); err != nil {
				log.Printf("sidekick: install skills: %v", err)
			}
		}
		if ok, _ := checkInstructions(); !ok {
			if err := installInstructions(); err != nil {
				log.Printf("sidekick: project instructions: %v", err)
			}
		}
		time.Sleep(5 * time.Minute)
	}
}

// ---- helpers ----

func backupOnce(path string) {
	bak := path + ".pre-sidekick"
	if _, err := os.Stat(bak); err == nil {
		return
	}
	if b, err := os.ReadFile(path); err == nil {
		os.WriteFile(bak, b, 0o644)
	}
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".sidekick.tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
