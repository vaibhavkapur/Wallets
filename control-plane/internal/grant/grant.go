// Package grant issues short-lived, single-use execution grants as EdDSA
// (Ed25519) JWTs. A grant binds organization, agent, task, endpoint, recipient,
// asset, network, scheme, amount ceiling, fee allowance, expiry, and a unique
// identifier. The Rust gateway verifies the signature and every bound field
// against the payment payload it is about to sign before it proceeds.
package grant

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
)

const (
	Issuer   = "asa-control-plane"
	Audience = "asa-gateway"
)

// Claims is the grant payload. Field names are part of the gateway contract.
type Claims struct {
	jwt.RegisteredClaims
	OrgID           string `json:"org"`
	TaskID          string `json:"task"`
	ReservationID   string `json:"rsv"`
	AttemptID       string `json:"att"`
	PolicyVersionID string `json:"pol"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	PayTo           string `json:"pay_to"`
	Asset           string `json:"asset"`
	Network         string `json:"network"`
	Scheme          string `json:"scheme"`
	AmountMax       string `json:"amount_max"`
	FeeMax          string `json:"fee_max"`
	RequirementsSHA string `json:"req_hash"`
}

// Signer holds the Ed25519 key pair.
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	kid  string
}

// NewSigner derives a signer from a 32-byte seed; Generate makes a fresh one.
func NewSigner(seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("grant: seed must be %d bytes", ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &Signer{priv: priv, pub: pub, kid: hex.EncodeToString(sum[:8])}, nil
}

// Generate creates a random signer (development only; keys are not persisted).
func Generate() (*Signer, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	return NewSigner(seed)
}

// ParseSeed decodes a base64 (std or url, padded or not) 32-byte seed.
func ParseSeed(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, errors.New("grant: seed must be base64 or hex")
}

// PublicKeyBase64 returns the raw Ed25519 public key (32 bytes) base64-encoded.
func (s *Signer) PublicKeyBase64() string { return base64.StdEncoding.EncodeToString(s.pub) }

// KeyID identifies the key in the JWT header.
func (s *Signer) KeyID() string { return s.kid }

// Input gathers everything a grant binds.
type Input struct {
	Grant       domain.ExecutionGrant
	Attempt     domain.PaymentAttempt
	Reservation domain.Reservation
	Policy      domain.BudgetPolicyVersion
	Quote       domain.ResourceQuote
}

// Issue signs a grant token.
func (s *Signer) Issue(in Input) (string, error) {
	c := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			Subject:   in.Attempt.AgentID,
			ID:        in.Grant.ID,
			IssuedAt:  jwt.NewNumericDate(in.Grant.IssuedAt),
			NotBefore: jwt.NewNumericDate(in.Grant.IssuedAt.Add(-5 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(in.Grant.ExpiresAt),
		},
		OrgID: in.Attempt.OrgID, TaskID: in.Attempt.TaskID, ReservationID: in.Reservation.ID, AttemptID: in.Attempt.ID,
		PolicyVersionID: in.Policy.ID, Method: strings.ToUpper(in.Attempt.Method), URL: in.Attempt.URL,
		PayTo: in.Quote.PayTo, Asset: in.Quote.Asset, Network: in.Quote.Network, Scheme: in.Quote.Scheme,
		AmountMax: in.Reservation.Amount.String(), FeeMax: in.Reservation.FeeAllowance.String(),
		RequirementsSHA: in.Quote.RequirementsHash,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	tok.Header["kid"] = s.kid
	return tok.SignedString(s.priv)
}

// Verify parses and validates a grant (used in tests and by operators).
func Verify(pub ed25519.PublicKey, token string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return pub, nil
	}, jwt.WithIssuer(Issuer), jwt.WithAudience(Audience), jwt.WithExpirationRequired(), jwt.WithLeeway(5*time.Second))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// RequirementsHash computes the canonical SHA-256 over the payment
// requirement fields. The Rust gateway implements the identical function;
// both sides must agree before a grant is issued or honored.
//
// Canonical form: lowercase scheme, network, asset and pay_to; the amount
// as a canonical decimal string; the resource verbatim; the timeout as a
// decimal integer; fields joined by "\n".
func RequirementsHash(q domain.ResourceQuote) string {
	canon := strings.Join([]string{
		strings.ToLower(q.Scheme),
		strings.ToLower(q.Network),
		strings.ToLower(q.Asset),
		strings.ToLower(q.PayTo),
		q.MaxAmountRequired.String(),
		q.Resource,
		fmt.Sprintf("%d", q.MaxTimeoutSeconds),
	}, "\n")
	sum := sha256.Sum256([]byte(canon))
	return hex.EncodeToString(sum[:])
}
