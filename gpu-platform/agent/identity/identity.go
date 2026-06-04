// Package identity manages the agent's stable fingerprint and its persisted
// credential. The credential file is the thing that makes re-runs idempotent:
// once enrolled, the agent reconnects instead of enrolling again.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type State struct {
	NodeID      string `json:"node_id"`
	Credential  string `json:"credential"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	// EnrollTokenHash is the hash of the enrollment token that produced the current
	// credential. It lets the agent detect when it's started with a *different* token
	// (e.g. an install command for another org) and re-enroll so the node re-homes,
	// instead of silently reconnecting into the org it first joined.
	EnrollTokenHash string `json:"enroll_token_hash,omitempty"`
}

// HashToken returns a stable hash of an enrollment token (so the raw token is never
// persisted to disk). Empty token → empty hash.
func HashToken(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Load reads persisted agent state. ok=false means there is no state file yet.
func Load(path string) (State, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false, err
	}
	return s, true, nil
}

// Save persists agent state with restrictive permissions.
func Save(path string, s State) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Fingerprint derives a stable hardware identity from machine-id + hostname.
func Fingerprint() string {
	host, _ := os.Hostname()
	sum := sha256.Sum256([]byte(machineID() + "/" + host))
	return hex.EncodeToString(sum[:])
}

// NewPublicKey returns a random opaque agent key (placeholder for a real keypair).
func NewPublicKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func machineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				return id
			}
		}
	}
	host, _ := os.Hostname()
	return host
}
