// Command mockresource is an x402-protected resource server for development
// and adversarial testing. It also exposes a facilitator-style settlement
// lookup so the control plane's reconciliation worker can be exercised.
//
// Paid endpoints (x402 v1, scheme "exact"):
//
//	GET  /v1/weather?city=...     price PRICE_WEATHER (default 100000 = 0.10 USDC)
//	POST /v1/summarize            price PRICE_SUMMARIZE (default 250000)
//	GET  /v1/free                 no payment required
//
// Chaos modes via ?mode=...:
//
//	redirect            302 to another host (gateway must refuse to follow)
//	changed-beneficiary challenge names an unapproved payTo
//	wrong-network       challenge names a different network
//	wrong-asset         challenge names a different asset
//	overpriced          challenge above the policy per-request max
//	slow                accepts payment, then stalls for SLOW_SECONDS before responding
//	huge                accepts payment, then streams a very large body
//	pay-then-fail       accepts payment, returns 500 (settled but no useful result)
//	lost                accepts payment, then closes the connection without a response
//	malformed           returns a 402 with an unparseable body
//
// Facilitator lookup: GET /settlements?payment_hash=<sha256 hex of X-PAYMENT header>
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type requirement struct {
	Scheme            string         `json:"scheme"`
	Network           string         `json:"network"`
	MaxAmountRequired string         `json:"maxAmountRequired"`
	Resource          string         `json:"resource"`
	Description       string         `json:"description"`
	MimeType          string         `json:"mimeType"`
	PayTo             string         `json:"payTo"`
	MaxTimeoutSeconds int            `json:"maxTimeoutSeconds"`
	Asset             string         `json:"asset"`
	Extra             map[string]any `json:"extra,omitempty"`
}

type challenge struct {
	X402Version int           `json:"x402Version"`
	Accepts     []requirement `json:"accepts"`
	Error       string        `json:"error,omitempty"`
}

type paymentPayload struct {
	X402Version int    `json:"x402Version"`
	Scheme      string `json:"scheme"`
	Network     string `json:"network"`
	Payload     struct {
		Signature     string `json:"signature"`
		Authorization struct {
			From        string `json:"from"`
			To          string `json:"to"`
			Value       string `json:"value"`
			ValidAfter  string `json:"validAfter"`
			ValidBefore string `json:"validBefore"`
			Nonce       string `json:"nonce"`
		} `json:"authorization"`
	} `json:"payload"`
}

type settlement struct {
	Status      string `json:"status"`
	Transaction string `json:"transaction"`
	Payer       string `json:"payer"`
	Amount      string `json:"amount"`
	Network     string `json:"network"`
}

type server struct {
	payTo, asset, network, baseURL string
	prices                         map[string]string
	mu                             sync.Mutex
	settlements                    map[string]settlement // by payment hash
	nonces                         map[string]bool
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	addr := env("LISTEN_ADDR", ":9402")
	s := &server{
		payTo:       env("PAY_TO", "0x209693Bc6afc0C5328bA36FaF03C514EF312287C"),
		asset:       env("ASSET", "0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
		network:     env("NETWORK", "base-sepolia"),
		baseURL:     env("PUBLIC_URL", "http://127.0.0.1"+addr),
		prices:      map[string]string{"/v1/weather": env("PRICE_WEATHER", "100000"), "/v1/summarize": env("PRICE_SUMMARIZE", "250000")},
		settlements: map[string]settlement{}, nonces: map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/weather", s.paid)
	mux.HandleFunc("POST /v1/summarize", s.paid)
	mux.HandleFunc("GET /v1/free", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"free": true})
	})
	mux.HandleFunc("GET /settlements", s.lookup)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	log.Printf("mock x402 resource listening on %s (payTo=%s network=%s)", addr, s.payTo, s.network)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) requirement(r *http.Request, mode string) requirement {
	req := requirement{
		Scheme: "exact", Network: s.network, MaxAmountRequired: s.prices[r.URL.Path], Resource: s.baseURL + r.URL.Path,
		Description: "mock paid resource", MimeType: "application/json", PayTo: s.payTo, MaxTimeoutSeconds: 60, Asset: s.asset,
		Extra: map[string]any{"name": "USDC", "version": "2"},
	}
	switch mode {
	case "changed-beneficiary":
		req.PayTo = "0x000000000000000000000000000000000000dEaD"
	case "wrong-network":
		req.Network = "base"
	case "wrong-asset":
		req.Asset = "0x1111111111111111111111111111111111111111"
	case "overpriced":
		req.MaxAmountRequired = "999999999999"
	}
	return req
}

func (s *server) challenge(w http.ResponseWriter, r *http.Request, mode, errMsg string) {
	if mode == "malformed" {
		w.WriteHeader(402)
		_, _ = w.Write([]byte("{not json"))
		return
	}
	writeJSON(w, http.StatusPaymentRequired, challenge{X402Version: 1, Accepts: []requirement{s.requirement(r, mode)}, Error: errMsg})
}

func (s *server) paid(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "redirect" {
		http.Redirect(w, r, "https://attacker.example/steal", http.StatusFound)
		return
	}
	header := r.Header.Get("X-PAYMENT")
	if header == "" {
		s.challenge(w, r, mode, "")
		return
	}
	req := s.requirement(r, "")
	raw, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		s.challenge(w, r, mode, "X-PAYMENT is not base64")
		return
	}
	var p paymentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		s.challenge(w, r, mode, "X-PAYMENT is not valid JSON")
		return
	}
	a := p.Payload.Authorization
	switch {
	case p.X402Version != 1:
		s.challenge(w, r, mode, "unsupported x402Version")
	case p.Scheme != req.Scheme || !strings.EqualFold(p.Network, req.Network):
		s.challenge(w, r, mode, "scheme/network mismatch")
	case !strings.EqualFold(a.To, req.PayTo):
		s.challenge(w, r, mode, "payment is not addressed to payTo")
	case cmpDecimal(a.Value, req.MaxAmountRequired) < 0:
		s.challenge(w, r, mode, "insufficient value")
	case p.Payload.Signature == "":
		s.challenge(w, r, mode, "missing signature")
	case expired(a.ValidBefore):
		s.challenge(w, r, mode, "authorization expired")
	default:
		s.settle(w, r, mode, header, p)
	}
}

func (s *server) settle(w http.ResponseWriter, r *http.Request, mode, header string, p paymentPayload) {
	hash := sha256.Sum256([]byte(header))
	key := hex.EncodeToString(hash[:])
	s.mu.Lock()
	if s.nonces[p.Payload.Authorization.Nonce] {
		s.mu.Unlock()
		s.challenge(w, r, mode, "nonce already used (replay)")
		return
	}
	s.nonces[p.Payload.Authorization.Nonce] = true
	tx := "0x" + key[:40]
	st := settlement{Status: "settled", Transaction: tx, Payer: p.Payload.Authorization.From, Amount: p.Payload.Authorization.Value, Network: p.Network}
	s.settlements[key] = st
	s.mu.Unlock()
	log.Printf("settled %s value=%s from=%s mode=%q", r.URL.Path, st.Amount, st.Payer, mode)

	settleHeader, _ := json.Marshal(map[string]any{"success": true, "transaction": tx, "network": p.Network, "payer": st.Payer})
	w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(settleHeader))

	switch mode {
	case "pay-then-fail":
		writeJSON(w, 500, map[string]string{"error": "upstream model failure"})
		return
	case "lost":
		// Settled, but the client never learns: simulate a dropped connection.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
		}
		time.Sleep(time.Hour)
		return
	case "slow":
		secs, _ := strconv.Atoi(env("SLOW_SECONDS", "15"))
		time.Sleep(time.Duration(secs) * time.Second)
	case "huge":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		chunk := make([]byte, 1<<20)
		for i := 0; i < 64; i++ { // 64 MiB
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		return
	}
	switch r.URL.Path {
	case "/v1/weather":
		writeJSON(w, 200, map[string]any{"city": r.URL.Query().Get("city"), "temperature_c": 21.5, "conditions": "partly cloudy", "paid": true, "transaction": tx})
	default:
		writeJSON(w, 200, map[string]any{"summary": "This is a mock summary of the submitted text.", "paid": true, "transaction": tx})
	}
}

func (s *server) lookup(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	st, ok := s.settlements[r.URL.Query().Get("payment_hash")]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, 200, settlement{Status: "not_found"})
		return
	}
	writeJSON(w, 200, st)
}

func cmpDecimal(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func expired(validBefore string) bool {
	n, err := strconv.ParseInt(validBefore, 10, 64)
	if err != nil {
		return true
	}
	return time.Now().Unix() >= n
}
