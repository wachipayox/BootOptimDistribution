package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wachipayox/BootOptimDistribution/internal/adminui"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
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

type handlerConfig struct {
	AdminUIEnabled bool
	AdminUIModel   adminui.ReadModel
}

func main() {
	listen := flag.String("listen", envOr("BOOTOPTIM_LISTEN", "127.0.0.1:8088"), "HTTP listen address")
	devAdminUI := flag.Bool("dev-admin-ui", false, "enable the local development-only administrative UI (loopback listeners only)")
	dataDir := flag.String("data-dir", envOr("BOOTOPTIM_DATA_DIR", "./data"), "directory for private CAS objects and SQLite metadata")
	flag.Parse()

	if err := validateAdminUIExposure(*devAdminUI, *listen); err != nil {
		log.Fatal(err)
	}

	var model adminui.ReadModel = adminui.EmptyReadModel{}
	var store *storage.SQLiteStore
	if *devAdminUI {
		cas, err := storage.OpenCAS(*dataDir, storage.DefaultMaxObjectBytes)
		if err != nil {
			log.Fatalf("open content store: %v", err)
		}
		store, err = storage.OpenSQLite(filepath.Join(*dataDir, "metadata.sqlite3"), cas)
		if err != nil {
			log.Fatalf("open metadata store: %v", err)
		}
		defer store.Close()
		model = adminui.SQLiteReadModel{Store: store}
	}
	server := &http.Server{
		Addr: *listen,
		Handler: securityHeaders(newHandlerWithConfig(handlerConfig{
			AdminUIEnabled: *devAdminUI,
			AdminUIModel:   model,
		})),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Printf("bootoptim distribution %s (%s) listening on %s", buildVersion, buildCommit, *listen)
	if *devAdminUI {
		log.Printf("admin UI enabled in read-only mode; state directory %s", *dataDir)
	}
	log.Fatal(server.ListenAndServe())
}

func newHandler() http.Handler {
	return newHandlerWithConfig(handlerConfig{})
}

func newHandlerWithConfig(cfg handlerConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/meta/version", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
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

	if cfg.AdminUIEnabled {
		model := cfg.AdminUIModel
		if model == nil {
			model = adminui.EmptyReadModel{}
		}
		mux.Handle("/admin/", adminui.NewHandler(model, adminui.BuildInfo{
			Version: buildVersion,
			Commit:  buildCommit,
		}))
	}

	return mux
}

func validateAdminUIExposure(enabled bool, listen string) error {
	if !enabled {
		return nil
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("development admin UI requires host:port loopback listener: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("development admin UI requires a literal loopback listener such as 127.0.0.1 or ::1")
	}
	return nil
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
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
