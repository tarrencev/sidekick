// Command sidekick is both the daemon that relays agent questions and artifacts
// to the Sidekick app, and the CLI agents use to push them.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "ask":
		err = cmdAsk(args)
	case "review":
		err = cmdReview(args)
	case "status":
		err = cmdStatus(args)
	case "whoami":
		err = cmdWhoami(args)
	case "hook":
		err = cmdHook(args)
	case "template":
		err = cmdTemplate(args)
	case "plan":
		err = cmdPlan(args)
	case "propose":
		err = cmdPropose(args)
	case "verify":
		err = cmdVerify(args)
	case "reply":
		err = cmdReply(args)
	case "push-test":
		var out map[string]int
		if err = call(context.Background(), "POST", "/v1/push-test", nil, &out); err == nil {
			fmt.Printf("Sent a test push to %d device(s); see `journalctl --user -u sidekick` for Apple's answer.\n", out["devices"])
		}
	case "doctor":
		err = cmdDoctor(args)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidekick:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  sidekick ask "<question>" [-o <option>]... [-multi]   ask the user; prints the answer
  sidekick review <path> -title <title> [-summary <s>]  publish a file or static site for review
  sidekick status "<headline>" -summary "<3-4 sentences>" [-link <url>] [-state ...]
  sidekick template <dir>                                start a review page in <dir>/index.html
  sidekick plan [-f plan.json]                           publish the project plan (coordinator; JSON on stdin)
  sidekick propose "<title>" -why "<why>" [-plan f.md]   propose a new thread for the user to approve (coordinator)
  sidekick reply "<answer>" [-id <id>]                   answer a message the user sent from the app
  sidekick verify '<[sidekick msg:id] tag>'              check that a pasted Sidekick prompt is genuine
  sidekick whoami                                        show the project/thread this pane maps to
  sidekick hook claude-ask|claude-stop                   Claude Code hooks (questions; replies to app messages)
  sidekick push-test                                     send a test push notification to registered phones
  sidekick doctor [-fix]                                 check (and repair) the install on this machine
  sidekick serve [flags]                                 run the daemon
`)
	os.Exit(2)
}

func home(p ...string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(append([]string{h}, p...)...)
}

func defaultSocket() string {
	if s := os.Getenv("SIDEKICK_SOCKET"); s != "" {
		return s
	}
	return home(".sidekick", "agent.sock")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	data := fs.String("data", home(".sidekick"), "state directory")
	root := fs.String("projects-root", home(".herdr-projects"), "herdr-projects root")
	listen := fs.String("listen", "127.0.0.1:7600", "user API listen address (put tailscale serve in front)")
	artListen := fs.String("artifacts-listen", "127.0.0.1:7601", "artifact static-site listen address")
	artURL := fs.String("artifact-url", "", "public base URL of the artifact listener, e.g. https://host.ts.net:7444")
	sock := fs.String("socket", defaultSocket(), "agent API unix socket")
	hp := fs.String("herdr-projects", home(".local", "bin", "herdr-projects"), "herdr-projects binary")
	herdr := fs.String("herdr", home(".local", "bin", "herdr"), "herdr binary")
	notify := fs.String("notify-url", "", "optional ntfy topic URL for push alerts")
	apnsKey := fs.String("apns-key", os.Getenv("SIDEKICK_APNS_KEY"), "APNs auth key (.p8) for native push")
	apnsKeyID := fs.String("apns-key-id", os.Getenv("SIDEKICK_APNS_KEY_ID"), "APNs key id")
	apnsTeam := fs.String("apns-team", os.Getenv("SIDEKICK_APNS_TEAM_ID"), "Apple developer team id")
	bundleID := fs.String("bundle-id", "gg.cartridge.sidekick", "the app's bundle id (APNs topic)")
	agentHTTP := fs.String("agent-http", "", "TESTING ONLY: also serve the agent API on this TCP address (e.g. 127.0.0.1:17602) so UI tests can act as agents")
	fs.Parse(args)
	if *artURL == "" {
		return fmt.Errorf("-artifact-url is required")
	}

	store, err := OpenStore(*data)
	if err != nil {
		return err
	}
	srv := &Server{
		store:       store,
		projects:    &Projects{root: *root},
		deliver:     NewDeliverer(store, *hp, *herdr),
		artifactURL: *artURL,
		notifyURL:   *notify,
		leases:      map[string]int{},
	}
	srv.prs = NewPRWatcher(func() { store.Publish("") })
	srv.apns, err = NewAPNs(*apnsKey, *apnsKeyID, *apnsTeam, *bundleID, *data)
	if err != nil {
		log.Printf("sidekick: APNs disabled: %v", err)
	}
	log.Printf("sidekick: native push %s, %d device(s) registered", map[bool]string{true: "on", false: "off (no APNs key)"}[srv.apns.Enabled()], srv.apns.DeviceCount())
	srv.resumeQuestions()

	// Only the real install briefs agents; a test daemon on a fake root must not
	// touch this machine's skills or projects.
	if filepath.Clean(*root) == home(".herdr-projects") {
		go keepAgentsBriefed()
		go srv.watchBlocked()
	}

	os.Remove(*sock)
	ln, err := net.Listen("unix", *sock)
	if err != nil {
		return err
	}
	os.Chmod(*sock, 0o600)

	errc := make(chan error, 4)
	go func() { errc <- http.Serve(ln, srv.agentMux()) }()
	if *agentHTTP != "" {
		if host, _, _ := net.SplitHostPort(*agentHTTP); host != "127.0.0.1" && host != "localhost" {
			return fmt.Errorf("-agent-http must be a loopback address")
		}
		log.Printf("sidekick: TEST agent API on http://%s", *agentHTTP)
		go func() { errc <- http.ListenAndServe(*agentHTTP, srv.agentMux()) }()
	}
	go func() { errc <- http.ListenAndServe(*listen, srv.userMux()) }()
	go func() {
		files := http.FileServer(http.Dir(filepath.Join(*data, "artifacts")))
		lib := srv.libraryHandler()
		uploads := srv.uploadsHandler()
		errc <- http.ListenAndServe(*artListen, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			switch strings.ToLower(filepath.Ext(r.URL.Path)) {
			case ".md", ".csv", ".txt", ".log", ".json", ".yaml", ".yml", ".toml", ".diff", ".patch":
				// Show text in the browser instead of offering a download.
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			}
			if strings.HasPrefix(r.URL.Path, "/lib/") {
				lib.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/uploads/") {
				uploads.ServeHTTP(w, r)
				return
			}
			files.ServeHTTP(w, r)
		}))
	}()
	log.Printf("sidekick: user API %s, artifacts %s (%s), agents %s", *listen, *artListen, *artURL, *sock)
	return <-errc
}

func agentClient() *http.Client {
	sock := defaultSocket()
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}
