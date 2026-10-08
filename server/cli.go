package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func currentOrigin(cwd string) Origin {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return Origin{Session: os.Getenv("HERDR_SESSION"), Pane: os.Getenv("HERDR_PANE_ID"), Cwd: cwd}
}

// call sends a JSON request to the local daemon and decodes the JSON reply.
func call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://sidekick"+path, rd)
	if err != nil {
		return err
	}
	resp, err := agentClient().Do(req)
	if err != nil {
		return fmt.Errorf("sidekick daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return &apiError{resp.StatusCode, strings.TrimSpace(string(msg))}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type apiError struct {
	code int
	msg  string
}

func (e *apiError) Error() string { return e.msg }

// interruptible returns a context cancelled on SIGINT/SIGTERM/SIGHUP.
func interruptible() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

// askAndWait pushes questions and blocks until the user answers, the question
// is cancelled, ctx ends, or the deadline passes. ok is false unless answered.
func askAndWait(ctx context.Context, origin Origin, qs []Question, deadline time.Time) (Item, bool, error) {
	var it Item
	if err := call(ctx, "POST", "/v1/ask", map[string]any{"origin": origin, "questions": qs}, &it); err != nil {
		return it, false, err
	}
	defer func() {
		if it.State == StatePending { // interrupted or timed out: withdraw it from the app
			call(context.Background(), "POST", "/v1/items/"+it.ID+"/cancel", nil, nil)
		}
	}()
	for it.State == StatePending && time.Now().Before(deadline) {
		var next Item
		err := call(ctx, "GET", "/v1/items/"+it.ID+"/wait?timeout=50", nil, &next)
		switch {
		case ctx.Err() != nil:
			return it, false, ctx.Err()
		case err != nil:
			// The daemon may be restarting; it keeps the question for a grace period.
			time.Sleep(3 * time.Second)
		default:
			it = next
		}
	}
	return it, it.State == StateAnswered, nil
}

func cmdAsk(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	var opts multiFlag
	fs.Var(&opts, "o", "an option the user can pick (repeatable); omit for a free-form answer")
	multi := fs.Bool("multi", false, "allow picking several options")
	header := fs.String("header", "", "short label for the question")
	wait := fs.Duration("wait", 0, "block until answered, up to this long (default: don't wait; the answer arrives in your pane)")
	jsonOut := fs.Bool("json", false, "print the full item as JSON")
	contextDir := fs.String("context", "", "a self-explaining HTML page (dir or file) shown with the question")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 {
		return errors.New(`usage: sidekick ask "<question>" [-o <option>]... [-context <dir>]`)
	}
	if *contextDir != "" {
		abs, err := filepath.Abs(*contextDir)
		if err != nil {
			return err
		}
		*contextDir = abs
	}
	q := Question{Question: fs.Arg(0), Header: *header, MultiSelect: *multi}
	for _, o := range opts {
		label, desc, _ := strings.Cut(o, "::")
		q.Options = append(q.Options, Option{Label: strings.TrimSpace(label), Description: strings.TrimSpace(desc)})
	}
	if *wait == 0 {
		var it Item
		if err := call(context.Background(), "POST", "/v1/ask", map[string]any{"origin": currentOrigin(""), "questions": []Question{q}, "async": true, "context": *contextDir}, &it); err != nil {
			return err
		}
		fmt.Printf("Sent to the user's Sidekick inbox. Keep working on anything that doesn't depend on it; their answer will arrive in this pane as a message starting with [sidekick answer:%s].\n", it.ID)
		return nil
	}
	ctx, stop := interruptible()
	defer stop()
	it, ok, err := askAndWait(ctx, currentOrigin(""), []Question{q}, time.Now().Add(*wait))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no answer (%s); hold and do not guess", it.State)
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(it)
	}
	fmt.Println(it.Answers[q.Question])
	return nil
}

func cmdReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	title := fs.String("title", "", "what the user is reviewing")
	summary := fs.String("summary", "", "what to look at and what decision you need")
	replaces := fs.String("replaces", "", "id of an earlier review this one replaces (withdraws it)")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 || *title == "" {
		return errors.New("usage: sidekick review <file-or-dir> -title <title> [-summary <summary>]")
	}
	p, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	var it Item
	if err := call(context.Background(), "POST", "/v1/review", map[string]any{
		"origin": currentOrigin(""), "path": p, "title": *title, "summary": *summary, "replaces": *replaces,
	}, &it); err != nil {
		return err
	}
	fmt.Printf("Published for review (id %s): %s\nThe user's verdict will arrive as a message starting with [sidekick]. Keep working; don't wait for it.\n", it.ID, it.URL)
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	state := fs.String("state", "on-track", "on-track | at-risk | blocked | done")
	summary := fs.String("summary", "", "3-4 sentences: where things stand and what's next")
	link := fs.String("link", "", "the one URL worth opening (PR, preview, doc)")
	var notes multiFlag
	fs.Var(&notes, "note", "a short supporting line (repeatable)")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 {
		return errors.New(`usage: sidekick status "<headline>" -summary "<3-4 sentences>" [-link <url>] [-state ...]`)
	}
	var out struct {
		Project, Thread string
	}
	if err := call(context.Background(), "POST", "/v1/status", map[string]any{
		"origin": currentOrigin(""), "headline": fs.Arg(0), "summary": *summary, "link": *link,
		"state": *state, "notes": []string(notes),
	}, &out); err != nil {
		return err
	}
	if out.Thread != "" {
		fmt.Printf("Updated the summary of thread %s in %s.\n", out.Thread, out.Project)
	} else {
		fmt.Printf("Updated the status of %s.\n", out.Project)
	}
	return nil
}

func cmdWhoami(args []string) error {
	var out map[string]string
	if err := call(context.Background(), "POST", "/v1/whoami", currentOrigin(""), &out); err != nil {
		return err
	}
	fmt.Printf("project=%s thread=%s\n", out["project"], orDash(out["thread"]))
	return nil
}

// cmdHook implements Claude Code's PreToolUse hook for AskUserQuestion. It relays
// the questions to the app and, once answered there, returns them as pre-filled
// answers so the terminal prompt never shows. In every other case (daemon down,
// pane not in a project, interrupted, timed out) it exits silently and Claude
// falls back to its normal terminal prompt.
func cmdHook(args []string) error {
	if len(args) == 1 && args[0] == "claude-stop" {
		return cmdStopHook()
	}
	if len(args) == 1 && args[0] == "claude-prompt" {
		return cmdPromptHook()
	}
	if len(args) != 1 || args[0] != "claude-ask" {
		return errors.New("usage: sidekick hook claude-ask|claude-stop")
	}
	var in struct {
		Cwd       string         `json:"cwd"`
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil || in.ToolName != "AskUserQuestion" {
		return nil
	}
	var qs []Question
	raw, _ := json.Marshal(in.ToolInput["questions"])
	if json.Unmarshal(raw, &qs) != nil || len(qs) == 0 {
		return nil
	}
	if pre, ok := in.ToolInput["answers"].(map[string]any); ok && len(pre) > 0 {
		return nil // already answered
	}

	// Asynchronous: file the question in the user's inbox and hand control straight
	// back, so the agent keeps working. The answer arrives later in the pane.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var it Item
	if err := call(ctx, "POST", "/v1/ask", map[string]any{"origin": currentOrigin(in.Cwd), "questions": qs, "async": true}, &it); err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			fmt.Fprintln(os.Stderr, "sidekick:", ae.msg) // e.g. not in a project: fall back to the terminal prompt
		}
		return nil
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "deny",
			"permissionDecisionReason": fmt.Sprintf(
				"Sent to the user's Sidekick inbox (question %s). This is not a refusal: questions are asynchronous so you never block on the user. "+
					"Don't ask again and don't wait. Continue with all work that doesn't depend on this decision, and hold anything that does. "+
					"Their answer will arrive in this pane as a message starting with [sidekick answer:%s].", it.ID, it.ID),
		},
	})
}

func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && !isBoolFlag(a) && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func isBoolFlag(a string) bool {
	switch strings.TrimLeft(a, "-") {
	case "multi", "json":
		return true
	}
	return false
}

// cmdPlan publishes the coordinator's structured plan, read as JSON from a file or stdin.
func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	file := fs.String("f", "-", "plan JSON file (- for stdin)")
	show := fs.Bool("show", false, "print the plan as the user sees it (JSON to edit and republish) and what differs from reality")
	confirm := fs.Bool("confirm", false, "say the page (plan, headline, summary) is right as it stands")
	fs.Parse(args)
	switch {
	case *show:
		var out struct {
			Plan  *Plan
			Drift []Drift
		}
		if err := call(context.Background(), "POST", "/v1/plan/show", map[string]any{"origin": currentOrigin("")}, &out); err != nil {
			return err
		}
		if out.Plan != nil {
			var m map[string]any // the agent's own fields only, ready to edit
			b, _ := json.Marshal(out.Plan.authored())
			json.Unmarshal(b, &m)
			delete(m, "updated")
			b, _ = json.MarshalIndent(m, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Println(`{"focus": "", "work": [], "mergeOrder": [], "next": []}`)
		}
		if len(out.Drift) > 0 {
			fmt.Fprintln(os.Stderr, "\nWhat differs from what Sidekick observes:")
			for _, d := range out.Drift {
				fmt.Fprintln(os.Stderr, "- "+d.Text)
			}
		}
		return nil
	case *confirm:
		var out map[string]string
		if err := call(context.Background(), "POST", "/v1/plan/confirm", map[string]any{"origin": currentOrigin("")}, &out); err != nil {
			return err
		}
		fmt.Printf("Confirmed the page for %s is current.\n", out["project"])
		return nil
	}
	var raw []byte
	var err error
	if *file == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(*file)
	}
	if err != nil {
		return err
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("plan is not valid JSON: %w", err)
	}
	if err := plan.validate(); err != nil {
		return err
	}
	var out map[string]string
	if err := call(context.Background(), "POST", "/v1/plan", map[string]any{"origin": currentOrigin(""), "plan": plan}, &out); err != nil {
		return err
	}
	fmt.Printf("Updated the plan for %s.\n", out["project"])
	return nil
}

// cmdPropose asks the user to approve a new thread.
func cmdPropose(args []string) error {
	fs := flag.NewFlagSet("propose", flag.ExitOnError)
	why := fs.String("why", "", "1-2 sentences: why this thread, why now")
	planFile := fs.String("plan", "", "markdown file with the thread's plan (optional)")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 || *why == "" {
		return errors.New(`usage: sidekick propose "<thread title>" -why "<why now>" [-plan plan.md]`)
	}
	var plan []byte
	if *planFile != "" {
		var err error
		if plan, err = os.ReadFile(*planFile); err != nil {
			return err
		}
	}
	var it Item
	if err := call(context.Background(), "POST", "/v1/propose", map[string]any{
		"origin": currentOrigin(""), "title": fs.Arg(0), "why": *why, "plan": string(plan),
	}, &it); err != nil {
		return err
	}
	fmt.Println("Proposed. Don't start it yet: the user's decision arrives as a message starting with [sidekick].")
	return nil
}

// cmdTemplate writes the starter review page into a folder.
func cmdTemplate(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: sidekick template <dir>   (writes <dir>/index.html to fill in, then: sidekick review <dir>)")
	}
	if err := os.MkdirAll(args[0], 0o755); err != nil {
		return err
	}
	out := filepath.Join(args[0], "index.html")
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists", out)
	}
	page, _ := assets.ReadFile("assets/artifact-template.html")
	if err := os.WriteFile(out, page, 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s. Put your images and videos next to it, replace every [[…]], then: sidekick review %s -title \"…\" -summary \"…\"\n", out, args[0])
	return nil
}

// cmdContext publishes a page that the pane's next question will show.
func cmdContext(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: sidekick context <dir-or-html-file>   (then ask your question)")
	}
	p, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	var out map[string]string
	if err := call(context.Background(), "POST", "/v1/context", map[string]any{"origin": currentOrigin(""), "path": p}, &out); err != nil {
		return err
	}
	fmt.Printf("Context ready (%s). Ask your question now (AskUserQuestion or sidekick ask): the next question from this pane shows it.\n", out["url"])
	return nil
}
