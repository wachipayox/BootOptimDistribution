package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	buildVersion = "0.1.0-dev"
	buildCommit  = "unknown"
)

type versionResponse struct {
	Service        string   `json:"service"`
	Version        string   `json:"version"`
	Commit         string   `json:"commit"`
	ProtocolSchema int      `json:"protocol_schema"`
	Capabilities   []string `json:"capabilities"`
	ServerTimeUTC  string   `json:"server_time_utc"`
}

func main() {
	listen := flag.String("listen", envOr("BOOTOPTIM_LISTEN", "127.0.0.1:8088"), "HTTP listen address")
	flag.Parse()

	server := &http.Server{
		Addr:              *listen,
		Handler:           securityHeaders(newHandler()),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Printf("bootoptim distribution %s (%s) listening on %s", buildVersion, buildCommit, *listen)
	log.Fatal(server.ListenAndServe())
}

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/meta/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(versionResponse{
			Service:        "bootoptim-distribution",
			Version:        buildVersion,
			Commit:         buildCommit,
			ProtocolSchema: 1,
			Capabilities: []string{
				"health", "build-version",
			},
			ServerTimeUTC: time.Now().UTC().Format(time.RFC3339),
		})
	})

	return mux
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
