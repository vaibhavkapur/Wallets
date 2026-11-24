package appleauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type fixture struct {
	key      *rsa.PrivateKey
	kid      string
	verifier *Apple
	now      time.Time
	fetches  int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{key: key, kid: "kid-1", now: time.Unix(1_800_000_000, 0)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.fetches++
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": f.kid, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(srv.Close)
	v, err := NewApple(Config{Audiences: []string{"com.mywallet.ios"}, JWKSURL: srv.URL, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	f.verifier = v
	return f
}

func (f *fixture) token(t *testing.T, mutate func(c jwt.MapClaims), kid string) string {
	t.Helper()
	c := jwt.MapClaims{"iss": Issuer, "aud": "com.mywallet.ios", "sub": "001234.abc", "exp": f.now.Add(10 * time.Minute).Unix(),
		"iat": f.now.Unix(), "email": "user@privaterelay.appleid.com", "email_verified": "true"}
	if mutate != nil {
		mutate(c)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifyValidToken(t *testing.T) {
	f := newFixture(t)
	id, err := f.verifier.Verify(context.Background(), f.token(t, nil, f.kid), "")
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "001234.abc" || id.Email == nil || *id.Email != "user@privaterelay.appleid.com" || !id.EmailVerified {
		t.Fatalf("identity: %+v", id)
	}
	// Second verification uses the cached key.
	if _, err := f.verifier.Verify(context.Background(), f.token(t, nil, f.kid), ""); err != nil {
		t.Fatal(err)
	}
	if f.fetches != 1 {
		t.Fatalf("expected 1 JWKS fetch, got %d", f.fetches)
	}
}

func TestVerifyRejections(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cases := map[string]string{
		"wrong audience": f.token(t, func(c jwt.MapClaims) { c["aud"] = "com.other.app" }, f.kid),
		"wrong issuer":   f.token(t, func(c jwt.MapClaims) { c["iss"] = "https://evil.example" }, f.kid),
		"expired":        f.token(t, func(c jwt.MapClaims) { c["exp"] = f.now.Add(-time.Minute).Unix() }, f.kid),
		"no exp":         f.token(t, func(c jwt.MapClaims) { delete(c, "exp") }, f.kid),
		"no subject":     f.token(t, func(c jwt.MapClaims) { delete(c, "sub") }, f.kid),
		"unknown kid":    f.token(t, nil, "kid-unknown"),
		"garbage":        "not.a.jwt",
	}
	for name, tok := range cases {
		if _, err := f.verifier.Verify(ctx, tok, ""); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: expected ErrInvalidToken, got %v", name, err)
		}
	}
	// Symmetric algorithm with the "kid" of a real key must be rejected.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": Issuer, "aud": "com.mywallet.ios", "sub": "x", "exp": f.now.Add(time.Minute).Unix()})
	hs.Header["kid"] = f.kid
	s, _ := hs.SignedString([]byte("secret"))
	if _, err := f.verifier.Verify(ctx, s, ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("HS256 accepted: %v", err)
	}
}

func TestNonceBinding(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	raw := "random-nonce-123"
	sum := sha256.Sum256([]byte(raw))
	withNonce := f.token(t, func(c jwt.MapClaims) { c["nonce"] = hex.EncodeToString(sum[:]); c["nonce_supported"] = true }, f.kid)

	if _, err := f.verifier.Verify(ctx, withNonce, raw); err != nil {
		t.Fatalf("matching nonce rejected: %v", err)
	}
	if _, err := f.verifier.Verify(ctx, withNonce, "other"); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("mismatched nonce accepted")
	}
	if _, err := f.verifier.Verify(ctx, withNonce, ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("token nonce without client nonce accepted")
	}
	if _, err := f.verifier.Verify(ctx, f.token(t, nil, f.kid), raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("client nonce without token nonce accepted")
	}
}

func TestKeyRotationRefetch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.verifier.Verify(ctx, f.token(t, nil, f.kid), ""); err != nil {
		t.Fatal(err)
	}
	f.kid = "kid-2"
	// Within a minute of the last fetch, unknown kids are not refetched.
	if _, err := f.verifier.Verify(ctx, f.token(t, nil, "kid-2"), ""); err == nil {
		t.Fatal("expected unknown kid before refresh window")
	}
	f.now = f.now.Add(2 * time.Minute)
	if _, err := f.verifier.Verify(ctx, f.token(t, nil, "kid-2"), ""); err != nil {
		t.Fatalf("rotated key rejected: %v", err)
	}
	if f.fetches != 2 {
		t.Fatalf("expected 2 fetches, got %d", f.fetches)
	}
}

func TestDevVerifier(t *testing.T) {
	id, err := Dev{}.Verify(context.Background(), "dev:alice:alice@example.test", "")
	if err != nil || id.Subject != "dev-alice" || id.Email == nil || *id.Email != "alice@example.test" {
		t.Fatalf("%+v %v", id, err)
	}
	if _, err := (Dev{}).Verify(context.Background(), "eyJhbGciOi...", ""); err == nil {
		t.Fatal("dev verifier accepted a non-dev token")
	}
}
