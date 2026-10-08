package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// APNs sends native push notifications to the Sidekick app. It is configured by
// an APNs auth key (.p8) from the Apple developer account plus its key id and
// team id; without them push is simply off.
// device is one registered app install: which APNs environment it uses, and the
// bundle id to address (the iPhone and Mac apps have different ones).
type device struct {
	Env   string `json:"env"`   // development | production
	Topic string `json:"topic"` // bundle id
}

type APNs struct {
	keyID, teamID, topic string
	key                  *ecdsa.PrivateKey
	client               *http.Client
	devicesPath          string

	mu        sync.Mutex
	devices   map[string]device // by token
	jwt       string
	jwtIssued time.Time
}

func NewAPNs(keyPath, keyID, teamID, topic, dataDir string) (*APNs, error) {
	a := &APNs{keyID: keyID, teamID: teamID, topic: topic, devices: map[string]device{},
		devicesPath: filepath.Join(dataDir, "devices.json"), client: &http.Client{Timeout: 15 * time.Second}}
	if readJSON(a.devicesPath, &a.devices) != nil {
		// Older files stored just the environment per token.
		var old map[string]string
		if readJSON(a.devicesPath, &old) == nil {
			for t, env := range old {
				a.devices[t] = device{Env: env, Topic: topic}
			}
		}
	}
	if keyPath == "" || keyID == "" || teamID == "" {
		return a, nil // registration still works, so devices are ready once a key arrives
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return a, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return a, errors.New("APNs key is not a PEM .p8 file")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return a, err
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return a, errors.New("APNs key is not an EC key")
	}
	a.key = ec
	return a, nil
}

func (a *APNs) Enabled() bool { return a.key != nil }

func (a *APNs) Register(token, env, topic string) error {
	if len(token) < 32 || strings.Trim(token, "0123456789abcdef") != "" { // gitleaks:allow (hex digits, not a key)
		return errors.New("bad device token")
	}
	if env != "production" {
		env = "development"
	}
	if topic == "" {
		topic = a.topic
	}
	if !strings.HasPrefix(topic, a.topic) {
		return errors.New("unknown app")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.devices[token] = device{Env: env, Topic: topic}
	return writeJSON(a.devicesPath, a.devices)
}

func (a *APNs) DeviceCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.devices)
}

// Push is one notification. Item/Project/Thread/Kind let the app open the right screen.
type Push struct {
	Title, Subtitle, Body       string
	Item, Project, Thread, Kind string
	Badge                       int
}

func (a *APNs) Send(p Push) {
	if !a.Enabled() {
		return
	}
	a.mu.Lock()
	devices := make(map[string]device, len(a.devices))
	for t, d := range a.devices {
		devices[t] = d
	}
	a.mu.Unlock()
	alert := map[string]any{"title": p.Title, "subtitle": p.Subtitle, "body": p.Body}
	payload, _ := json.Marshal(map[string]any{
		"aps":     map[string]any{"alert": alert, "sound": "default", "badge": p.Badge, "thread-id": p.Project},
		"item":    p.Item,
		"project": p.Project,
		"thread":  p.Thread,
		"kind":    p.Kind,
	})
	for token, d := range devices {
		go a.send(token, d, payload)
	}
}

func (a *APNs) send(token string, d device, payload []byte) {
	host := "https://api.push.apple.com"
	if d.Env == "development" {
		host = "https://api.sandbox.push.apple.com"
	}
	jwt, err := a.token()
	if err != nil {
		log.Printf("apns: %v", err)
		return
	}
	req, _ := http.NewRequest("POST", host+"/3/device/"+token, bytes.NewReader(payload))
	req.Header.Set("authorization", "bearer "+jwt)
	req.Header.Set("apns-topic", d.Topic)
	req.Header.Set("apns-push-type", "alert")
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("apns: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		log.Printf("apns: delivered to %s… (%s %s, apns-id %s)", token[:8], d.Topic, d.Env, resp.Header.Get("apns-id"))
		return
	}
	body, _ := io.ReadAll(resp.Body)
	log.Printf("apns: %s %s: %s", resp.Status, token[:8], strings.TrimSpace(string(body)))
	// Forget tokens Apple says are dead.
	if resp.StatusCode == http.StatusGone || strings.Contains(string(body), "BadDeviceToken") || strings.Contains(string(body), "Unregistered") {
		a.mu.Lock()
		delete(a.devices, token)
		writeJSON(a.devicesPath, a.devices)
		a.mu.Unlock()
	}
}

// token returns the provider JWT, reissued every 50 minutes (Apple allows up to 60).
func (a *APNs) token() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.jwt != "" && time.Since(a.jwtIssued) < 50*time.Minute {
		return a.jwt, nil
	}
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": a.keyID})
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{"iss": a.teamID, "iat": now.Unix()})
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, a.key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64) // JWS ES256 wants raw r||s, 32 bytes each
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	a.jwt, a.jwtIssued = signing+"."+enc.EncodeToString(sig), now
	return a.jwt, nil
}

// ---- HTTP ----

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
		Env   string `json:"env"`
		Topic string `json:"topic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if err := s.apns.Register(strings.ToLower(body.Token), body.Env, body.Topic); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSONResponse(w, map[string]bool{"push": s.apns.Enabled()})
}

// pendingCount is the app icon badge: everything waiting in the inbox.
func (s *Server) pendingCount() int {
	n := 0
	conversations := map[string]bool{}
	for _, it := range s.store.Items("") {
		if it.State == StatePending && (it.Kind == "question" || it.Kind == "review" || it.Kind == "proposal") {
			n++
		} else if unreadReply(it) && !conversations[it.Project+"/"+it.Thread] {
			conversations[it.Project+"/"+it.Thread] = true // one per conversation, like the inbox
			n++
		}
	}
	return n
}

// pushTest sends a test notification to every registered device.
func (s *Server) pushTest(w http.ResponseWriter, r *http.Request) {
	if !s.apns.Enabled() {
		http.Error(w, "native push is off: no APNs key configured", http.StatusConflict)
		return
	}
	s.apns.Send(Push{Title: "Sidekick", Subtitle: "Test", Body: "Push notifications work.", Badge: s.pendingCount()})
	writeJSONResponse(w, map[string]int{"devices": s.apns.DeviceCount()})
}
