package outbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSignIsHMACOverTimestampAndBody(t *testing.T) {
	secret := []byte("s3cret")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("1700000000.{\"a\":1}"))
	want := hex.EncodeToString(mac.Sum(nil))
	if got := Sign(secret, "1700000000", []byte(`{"a":1}`)); got != want {
		t.Errorf("Sign = %s, want %s", got, want)
	}
	if Sign(secret, "1700000001", []byte(`{"a":1}`)) == want {
		t.Error("the timestamp must be part of the signature")
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	if backoff(1) >= backoff(3) || backoff(20) != backoff(30) {
		t.Errorf("backoff: %v %v %v %v", backoff(1), backoff(3), backoff(20), backoff(30))
	}
}
