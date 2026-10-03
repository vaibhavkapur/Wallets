package stripe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mywallet/backend/internal/provider"
)

// WebhookTolerance bounds how old a signed delivery may be.
const WebhookTolerance = 5 * time.Minute

// ParseWebhook implements provider.Provider using the Stripe-Signature
// scheme: HMAC-SHA256 over "<timestamp>.<payload>" with the endpoint secret.
func (c *Client) ParseWebhook(payload []byte, header string, now time.Time) (provider.Event, error) {
	if c.cfg.WebhookSecret == "" {
		return provider.Event{}, fmt.Errorf("%w: webhook secret not configured", provider.ErrInvalidSignature)
	}
	ts, sigs, err := parseSignatureHeader(header)
	if err != nil {
		return provider.Event{}, err
	}
	if d := now.Sub(time.Unix(ts, 0)); d > WebhookTolerance || d < -WebhookTolerance {
		return provider.Event{}, fmt.Errorf("%w: timestamp outside tolerance", provider.ErrInvalidSignature)
	}
	expected := Sign(c.cfg.WebhookSecret, ts, payload)
	ok := false
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(expected)) {
			ok = true
			break
		}
	}
	if !ok {
		return provider.Event{}, provider.ErrInvalidSignature
	}
	var ev struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return provider.Event{}, fmt.Errorf("stripe: decode event: %w", err)
	}
	var obj struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(ev.Data.Object, &obj)
	if ev.ID == "" || ev.Type == "" {
		return provider.Event{}, fmt.Errorf("stripe: event missing id or type")
	}
	return provider.Event{ID: ev.ID, Type: ev.Type, ObjectID: obj.ID, Object: ev.Data.Object}, nil
}

// Sign computes the v1 signature for a payload; exported for tests and
// the local webhook replay tool.
func Sign(secret string, ts int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func parseSignatureHeader(h string) (int64, []string, error) {
	var ts int64 = -1
	var sigs []string
	for _, part := range strings.Split(h, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			v, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return 0, nil, fmt.Errorf("%w: bad timestamp", provider.ErrInvalidSignature)
			}
			ts = v
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if ts < 0 || len(sigs) == 0 {
		return 0, nil, fmt.Errorf("%w: missing timestamp or signature", provider.ErrInvalidSignature)
	}
	return ts, sigs, nil
}
