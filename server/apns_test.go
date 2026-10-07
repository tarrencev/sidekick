package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPNsJWT(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	dir := t.TempDir()
	p8 := filepath.Join(dir, "AuthKey_TEST.p8")
	os.WriteFile(p8, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)

	a, err := NewAPNs(p8, "KEYID12345", "TEAM123456", "gg.cartridge.sidekick", dir)
	if err != nil || !a.Enabled() {
		t.Fatalf("load key: %v", err)
	}
	jwt, err := a.token()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	enc := base64.RawURLEncoding
	var header, claims map[string]any
	h, _ := enc.DecodeString(parts[0])
	c, _ := enc.DecodeString(parts[1])
	json.Unmarshal(h, &header)
	json.Unmarshal(c, &claims)
	if header["alg"] != "ES256" || header["kid"] != "KEYID12345" || claims["iss"] != "TEAM123456" {
		t.Fatalf("header %v claims %v", header, claims)
	}
	sig, _ := enc.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, sum[:], r, s) {
		t.Fatal("signature does not verify")
	}

	if err := a.Register("not-a-token", "development", ""); err == nil {
		t.Error("bad token accepted")
	}
	if err := a.Register(strings.Repeat("ab", 32), "development", "gg.cartridge.sidekick"); err != nil {
		t.Errorf("register iPhone: %v", err)
	}
	if err := a.Register(strings.Repeat("cd", 32), "development", "gg.cartridge.sidekick.mac"); err != nil || a.DeviceCount() != 2 {
		t.Errorf("register Mac: %v, %d devices", err, a.DeviceCount())
	}
	if err := a.Register(strings.Repeat("ef", 32), "development", "com.evil.app"); err == nil {
		t.Error("foreign bundle id accepted")
	}
}
