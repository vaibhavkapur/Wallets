// Command mocksigner stands in for the isolated wallet/signing provider.
//
// The real provider holds the keys and enforces its own constraints; the
// gateway only ever sends it a fully constrained EIP-3009 style authorization
// (to, value, validBefore, nonce) and receives a signature. This mock
// returns a deterministic HMAC "signature" so the end-to-end flow can run
// without any blockchain, and it refuses anything outside its configured
// constraints to demonstrate provider-side enforcement.
//
// Environment:
//
//	LISTEN_ADDR     default :9403
//	SIGNER_TOKEN    bearer token the gateway must present (default "dev-signer-token")
//	WALLET_ADDRESS  the payer address reported as "from"
//	ALLOWED_TO      optional comma-separated recipient allowlist enforced by the provider
//	MAX_VALUE       optional per-authorization ceiling enforced by the provider
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
)

type signRequest struct {
	Network     string `json:"network"`
	Asset       string `json:"asset"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"valid_after"`
	ValidBefore string `json:"valid_before"`
	Nonce       string `json:"nonce"`
	// GrantID is echoed into the audit log so the provider can correlate.
	GrantID string `json:"grant_id"`
}

type signResponse struct {
	From      string `json:"from"`
	Signature string `json:"signature"`
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	addr := env("LISTEN_ADDR", ":9403")
	token := env("SIGNER_TOKEN", "dev-signer-token")
	wallet := env("WALLET_ADDRESS", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	secret := []byte(env("SIGNER_SECRET", "mock-signer-secret-not-a-real-key"))
	var allowedTo []string
	if v := os.Getenv("ALLOWED_TO"); v != "" {
		for _, s := range strings.Split(v, ",") {
			allowedTo = append(allowedTo, strings.ToLower(strings.TrimSpace(s)))
		}
	}
	var maxValue *big.Int
	if v := os.Getenv("MAX_VALUE"); v != "" {
		maxValue, _ = new(big.Int).SetString(v, 10)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("POST /v1/sign", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var req signRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		if req.To == "" || req.Value == "" || req.ValidBefore == "" || req.Nonce == "" {
			http.Error(w, `{"error":"to, value, valid_before and nonce are required"}`, http.StatusBadRequest)
			return
		}
		// Provider-side constraints (defense in depth beyond the control plane).
		if len(allowedTo) > 0 && !contains(allowedTo, strings.ToLower(req.To)) {
			http.Error(w, `{"error":"recipient not permitted by signing provider"}`, http.StatusForbidden)
			return
		}
		if maxValue != nil {
			v, ok := new(big.Int).SetString(req.Value, 10)
			if !ok || v.Sign() < 0 || v.Cmp(maxValue) > 0 {
				http.Error(w, `{"error":"value exceeds signing provider ceiling"}`, http.StatusForbidden)
				return
			}
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(strings.Join([]string{wallet, req.Network, req.Asset, req.To, req.Value, req.ValidAfter, req.ValidBefore, req.Nonce}, "|")))
		sig := mac.Sum(nil)
		log.Printf("signed authorization grant=%s to=%s value=%s nonce=%s", req.GrantID, req.To, req.Value, req.Nonce)
		w.Header().Set("Content-Type", "application/json")
		// 65 bytes like a secp256k1 signature: HMAC twice + recovery byte.
		full := append(append([]byte{}, sig...), sig...)
		full = append(full, 0x1b)
		_ = json.NewEncoder(w).Encode(signResponse{From: wallet, Signature: "0x" + hex.EncodeToString(full)})
	})
	log.Printf("mock signing provider listening on %s (wallet=%s)", addr, wallet)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
