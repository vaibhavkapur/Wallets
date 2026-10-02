package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unowallet/checkout/internal/checkout"
)

func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func main() {
	if env("PAYMENT_MODE", "sandbox") != "sandbox" {
		log.Fatal("Live payments are not enabled. Configure and validate real partner adapters before changing this guard.")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var store checkout.Store
	var err error
	if url := os.Getenv("DATABASE_URL"); url != "" {
		store, err = checkout.NewPostgresStore(ctx, url)
	} else {
		store, err = checkout.NewFileStore(env("DATA_FILE", "../.local/state.json"))
	}
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			log.Fatal(err)
		}
		secret = hex.EncodeToString(b)
	}
	service := checkout.NewService(store)
	api := (&checkout.Server{Service: service, Config: checkout.HTTPConfig{SessionSecret: secret, OperatorToken: os.Getenv("OPERATOR_TOKEN"), WebhookSecret: os.Getenv("WEBHOOK_SECRET"), AllowedOrigin: env("APP_ORIGIN", "http://127.0.0.1:5188"), Demo: true, SecureCookies: os.Getenv("SECURE_COOKIES") == "true"}}).Handler()
	mux := http.NewServeMux()
	mux.Handle("/v1/", api)
	mux.Handle("/healthz", api)
	dist := env("WEB_DIST", "../web/dist")
	fs := http.FileServer(http.Dir(dist))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: env("ADDR", "127.0.0.1:8088"), Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	if os.Getenv("WORKER_MODE") != "external" {
		go service.RunWorker(ctx, func(err error) { log.Printf("worker operation failed (%T); retry preserved", err) })
	} else if os.Getenv("DATABASE_URL") == "" {
		log.Fatal("external worker mode requires DATABASE_URL")
	}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Agent Checkout sandbox listening on %s", server.Addr)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
