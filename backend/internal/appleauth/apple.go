// Package appleauth verifies Sign in with Apple identity tokens on the
// server. The app never becomes authenticated from a client-side claim; the
// token's signature, issuer, audience, expiry, and nonce are checked here.
package appleauth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer is Apple's token issuer.
const Issuer = "https://appleid.apple.com"

// DefaultJWKSURL is Apple's public key endpoint.
const DefaultJWKSURL = "https://appleid.apple.com/auth/keys"

// ErrInvalidToken is returned for any verification failure. The detail is
// wrapped for logs but handlers should not echo it to clients.
var ErrInvalidToken = errors.New("appleauth: invalid identity token")

// Identity is the verified subject.
type Identity struct {
	Subject       string
	Email         *string
	EmailVerified bool
}

// Verifier validates an identity token and optional raw nonce.
type Verifier interface {
	Verify(ctx context.Context, identityToken string, rawNonce string) (Identity, error)
}

// Config configures the Apple verifier.
type Config struct {
	// Audiences are the accepted client IDs (the app bundle identifier).
	Audiences []string
	// JWKSURL overrides the key endpoint (tests).
	JWKSURL string
	// HTTPClient defaults to a 10 second timeout client.
	HTTPClient *http.Client
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Apple verifies tokens against Apple's published keys.
type Apple struct {
	cfg  Config
	http *http.Client

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewApple builds a verifier.
func NewApple(cfg Config) (*Apple, error) {
	if len(cfg.Audiences) == 0 {
		return nil, errors.New("appleauth: at least one audience (bundle ID) is required")
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = DefaultJWKSURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Apple{cfg: cfg, http: cfg.HTTPClient, keys: map[string]*rsa.PublicKey{}}, nil
}

type claims struct {
	jwt.RegisteredClaims
	Email          string `json:"email"`
	EmailVerified  any    `json:"email_verified"`
	Nonce          string `json:"nonce"`
	NonceSupported bool   `json:"nonce_supported"`
}

// Verify implements Verifier.
func (a *Apple) Verify(ctx context.Context, identityToken string, rawNonce string) (Identity, error) {
	var c claims
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(a.cfg.Now),
	)
	_, err := parser.ParseWithClaims(identityToken, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		return a.key(ctx, kid)
	})
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !a.audienceOK(c.Audience) {
		return Identity{}, fmt.Errorf("%w: audience mismatch", ErrInvalidToken)
	}
	if c.Subject == "" {
		return Identity{}, fmt.Errorf("%w: missing subject", ErrInvalidToken)
	}
	// Nonce binding: when the token carries a nonce the caller must present
	// the raw value whose SHA-256 matches it.
	if c.Nonce != "" || rawNonce != "" {
		if c.Nonce == "" || rawNonce == "" {
			return Identity{}, fmt.Errorf("%w: nonce mismatch", ErrInvalidToken)
		}
		sum := sha256.Sum256([]byte(rawNonce))
		if !strings.EqualFold(hex.EncodeToString(sum[:]), c.Nonce) {
			return Identity{}, fmt.Errorf("%w: nonce mismatch", ErrInvalidToken)
		}
	}
	id := Identity{Subject: c.Subject, EmailVerified: boolish(c.EmailVerified)}
	if c.Email != "" {
		e := c.Email
		id.Email = &e
	}
	return id, nil
}

func (a *Apple) audienceOK(aud jwt.ClaimStrings) bool {
	for _, got := range aud {
		for _, want := range a.cfg.Audiences {
			if got == want {
				return true
			}
		}
	}
	return false
}

func boolish(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true"
	}
	return false
}

// key returns the public key for kid, refreshing the JWKS cache when the
// key is unknown (at most once per minute to bound traffic on bad input).
func (a *Apple) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if k, ok := a.keys[kid]; ok {
		return k, nil
	}
	if a.cfg.Now().Sub(a.fetchedAt) < time.Minute && len(a.keys) > 0 {
		return nil, errors.New("unknown kid")
	}
	if err := a.refreshLocked(ctx); err != nil {
		return nil, err
	}
	if k, ok := a.keys[kid]; ok {
		return k, nil
	}
	return nil, errors.New("unknown kid")
}

func (a *Apple) refreshLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.JWKSURL, nil)
	if err != nil {
		return err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(keys) == 0 {
		return errors.New("jwks contained no RSA keys")
	}
	a.keys = keys
	a.fetchedAt = a.cfg.Now()
	return nil
}

// Dev accepts tokens of the form "dev:<subject>[:<email>]". It exists so the
// app can be exercised against a local backend without an Apple Developer
// account and must never be enabled outside development.
type Dev struct{}

// Verify implements Verifier.
func (Dev) Verify(_ context.Context, token string, _ string) (Identity, error) {
	parts := strings.SplitN(token, ":", 3)
	if len(parts) < 2 || parts[0] != "dev" || parts[1] == "" {
		return Identity{}, ErrInvalidToken
	}
	id := Identity{Subject: "dev-" + parts[1]}
	if len(parts) == 3 && parts[2] != "" {
		e := parts[2]
		id.Email = &e
		id.EmailVerified = true
	}
	return id, nil
}
