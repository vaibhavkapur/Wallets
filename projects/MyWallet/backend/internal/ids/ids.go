// Package ids generates unguessable, prefixed identifiers and credentials.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// New returns an identifier like "card_3f2a...".
func New(prefix string) string { return prefix + "_" + random(12) }

// NewSecret returns a credential string. Only HashSecret(secret) is stored.
func NewSecret(prefix string) string { return prefix + "_" + random(24) }

// HashSecret is the storage form of credentials.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
