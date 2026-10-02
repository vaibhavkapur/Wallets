// Package engine is the client for the Rust evaluation service. The engine is
// the only place a cost is computed; this package moves bytes and checks that
// the result is bound to the request that was sent.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
)

const maxResponseBytes = 4 << 20

// RejectedError means the engine refused the request as malformed or invalid.
type RejectedError struct {
	Status  int
	Code    string
	Message string
	Field   string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("engine rejected request: %s: %s", e.Code, e.Message)
}

// ErrUnavailable means no usable answer was obtained from the engine.
var ErrUnavailable = errors.New("engine: unavailable")

// Evaluation is a result bound to the exact request bytes that produced it.
type Evaluation struct {
	RequestJSON []byte          // exactly what was sent
	InputSHA256 string          // hash of RequestJSON, confirmed by the engine
	ResultJSON  json.RawMessage // the engine's result, verbatim
	Result      contract.Result
}

// Evaluator computes an evaluation for a request.
type Evaluator interface {
	Evaluate(ctx context.Context, req *contract.Request) (*Evaluation, error)
}

// Client talks to the engine over HTTP with a shared bearer token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a client with a conservative timeout.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Evaluate sends the request and verifies the response hash and versions.
func (c *Client) Evaluate(ctx context.Context, req *contract.Request) (*Evaluation, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("engine: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/evaluate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity:
		var envelope struct {
			Error struct {
				Code    string  `json:"code"`
				Message string  `json:"message"`
				Field   *string `json:"field"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil || envelope.Error.Code == "" {
			return nil, fmt.Errorf("%w: unreadable %d response", ErrUnavailable, resp.StatusCode)
		}
		rejected := &RejectedError{Status: resp.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
		if envelope.Error.Field != nil {
			rejected.Field = *envelope.Error.Field
		}
		return nil, rejected
	default:
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}

	var envelope struct {
		InputSHA256 string          `json:"input_sha256"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("%w: unreadable response: %v", ErrUnavailable, err)
	}
	if want := ids.Fingerprint(body); envelope.InputSHA256 != want {
		return nil, fmt.Errorf("%w: result is not bound to the request sent", ErrUnavailable)
	}
	out := &Evaluation{RequestJSON: body, InputSHA256: envelope.InputSHA256, ResultJSON: envelope.Result}
	if err := json.Unmarshal(envelope.Result, &out.Result); err != nil {
		return nil, fmt.Errorf("%w: unreadable result: %v", ErrUnavailable, err)
	}
	if out.Result.ContractVersion != contract.Version {
		return nil, fmt.Errorf("%w: contract version %q", ErrUnavailable, out.Result.ContractVersion)
	}
	return out, nil
}
