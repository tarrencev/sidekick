package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Deliverer sends review verdicts back to the agent that asked, through
// herdr-projects so its prompt-box safety checks (e.g. an unsent draft) apply.
type Deliverer struct {
	store *Store
	hpBin string
	herdr string
	queue chan string
}

func NewDeliverer(store *Store, hpBin, herdrBin string) *Deliverer {
	d := &Deliverer{store: store, hpBin: hpBin, herdr: herdrBin, queue: make(chan string, 256)}
	go d.run()
	// Resume verdicts that were decided but never delivered (e.g. across a restart).
	for _, it := range store.Items("") {
		if it.Kind == "question" && it.Async && it.State == StateAnswered && !it.Delivered {
			d.Enqueue(it.ID)
			continue
		}
		if (it.Kind == "review" || it.Kind == "proposal") && (it.State == StateApproved || it.State == StateChanges || it.State == StateRejected) && !it.Delivered {
			d.Enqueue(it.ID)
		}
	}
	return d
}

func (d *Deliverer) Enqueue(id string) { d.queue <- id }

func (d *Deliverer) run() {
	for id := range d.queue {
		go d.deliver(id)
	}
}

func (d *Deliverer) deliver(id string) {
	backoff := 15 * time.Second
	for attempt := 1; ; attempt++ {
		it, ok := d.store.Get(id)
		if !ok || it.Delivered {
			return
		}
		err := d.send(it)
		if err == nil {
			d.store.Update(id, func(it *Item) error { it.Delivered = true; return nil })
			log.Printf("delivered review verdict %s to %s/%s", id, it.Project, orDash(it.Thread))
			return
		}
		log.Printf("deliver %s (attempt %d): %v", id, attempt, err)
		time.Sleep(backoff)
		if backoff < 10*time.Minute {
			backoff *= 2
		}
	}
}

func (d *Deliverer) send(it Item) error {
	err := d.Prompt(it.Project, it.Thread, verdictMessage(it))
	if err != nil && it.Thread != "" && threadGone(err) {
		// The thread's agent has exited or the thread was resolved: hand the
		// decision to the coordinator, which can restart or redirect the work.
		return d.Prompt(it.Project, "", fmt.Sprintf("(For thread %s, which has no running agent.) %s", it.Thread, verdictMessage(it)))
	}
	return err
}

// threadGone reports whether herdr-projects refused because the thread has no agent to type into.
func threadGone(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no agent is detected") || strings.Contains(msg, "not open") ||
		strings.Contains(msg, "resolved") || strings.Contains(msg, "no such thread") || strings.Contains(msg, "unknown thread")
}

// Prompt types text into the project's coordinator pane, or a thread's pane.
func (d *Deliverer) Prompt(project, thread, text string) error {
	args := []string{"coordinator", "prompt", project, "--text-file", "-"}
	if thread != "" {
		args = []string{"thread", "prompt", project, thread, "--text-file", "-"}
	}
	cmd := exec.Command(d.hpBin, args...)
	cmd.Env = append(os.Environ(), "HERDR_BIN_PATH="+d.herdr)
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func verdictMessage(it Item) string {
	switch it.Kind {
	case "proposal":
		return proposalMessage(it)
	case "question":
		return answerMessage(it)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s Review of %q (%s): ", verdictMarker(it.ID), it.Title, it.URL)
	switch it.State {
	case StateApproved:
		b.WriteString("approved by the user.")
		if it.Comment != "" {
			fmt.Fprintf(&b, " Note: %s", it.Comment)
		}
	case StateRejected:
		b.WriteString("rejected by the user. Don't pursue this direction further without asking them.")
		if it.Comment != "" {
			fmt.Fprintf(&b, " Their reason: %s", it.Comment)
		}
	default:
		comment := it.Comment
		if comment == "" {
			comment = "see the attached files"
		}
		fmt.Fprintf(&b, "the user requested changes: %s", comment)
	}
	b.WriteString(attachmentNote(it.Attachments))
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "coordinator"
	}
	return s
}

func proposalMessage(it Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s Your proposed thread %q: ", verdictMarker(it.ID), it.Title)
	switch it.State {
	case StateApproved:
		b.WriteString("approved by the user. Start it now.")
		if it.Comment != "" {
			fmt.Fprintf(&b, " Their note: %s", it.Comment)
		}
	case StateRejected:
		b.WriteString("declined by the user. Don't start it.")
		if it.Comment != "" {
			fmt.Fprintf(&b, " Their reason: %s", it.Comment)
		}
	default:
		fmt.Fprintf(&b, "the user wants changes before it starts: %s. Revise it and propose it again.", it.Comment)
	}
	b.WriteString(attachmentNote(it.Attachments))
	return b.String()
}

// verdictMarker tags a delivered decision so the prompt hook can verify it came
// from the user through Sidekick.
func verdictMarker(id string) string { return "[sidekick verdict:" + id + "]" }

// answerMessage delivers the user's answer to an async question.
func answerMessage(it Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[sidekick answer:%s] The user answered your question from Sidekick:\n", it.ID)
	for _, q := range it.Questions {
		fmt.Fprintf(&b, "\nQ: %s\nA: %s\n", q.Question, it.Answers[q.Question])
	}
	b.WriteString(attachmentNote(it.Attachments))
	return b.String()
}
