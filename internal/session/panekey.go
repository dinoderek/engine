package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PaneKeyFile is a server run's pane key: the hex seed of the Ed25519 key
// its prompt hook signs the operator's pane input with. The server holds
// only the public half, so a post signed without the file shows.
const PaneKeyFile = "pane-key"

// NewPaneKey writes a fresh pane key into the run directory dir, readable
// only by its owner, and returns its public key in hex.
func NewPaneKey(dir string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, PaneKeyFile), []byte(hex.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write the pane key: %w", err)
	}
	return hex.EncodeToString(pub), nil
}

// PanePublicKey is the public key, in hex, of the pane key in dir.
func PanePublicKey(dir string) (string, error) {
	priv, err := readPaneKey(dir)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(priv.Public().(ed25519.PublicKey)), nil
}

// SignPane signs message with the pane key in dir and returns the
// signature in hex.
func SignPane(dir string, message []byte) (string, error) {
	priv, err := readPaneKey(dir)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(ed25519.Sign(priv, message)), nil
}

func readPaneKey(dir string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(filepath.Join(dir, PaneKeyFile))
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s in %s is not a pane key", PaneKeyFile, dir)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
