// Package ids generates opaque, unguessable identifiers.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

var encoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// New returns prefix + "_" + 26 characters carrying 128 random bits.
func New(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("ids: system randomness unavailable: " + err.Error())
	}
	return prefix + "_" + encoding.EncodeToString(b[:])
}

// NewAPIKey returns a new API key secret. Only its hash is ever stored.
func NewAPIKey() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("ids: system randomness unavailable: " + err.Error())
	}
	return "uno_opt_" + hex.EncodeToString(b[:])
}

// HashSecret returns the hex SHA-256 of a secret, the only form persisted.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return hex.EncodeToString(sum[:])
}

// Fingerprint returns the hex SHA-256 of arbitrary bytes.
func Fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
