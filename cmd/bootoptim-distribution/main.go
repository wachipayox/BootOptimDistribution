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
	adminUILAN := flag.Bool("admin-ui-lan", false, "enable the read-only admin UI directly on one private LAN address")
	adminUIAllowCIDR := flag.String("admin-ui-allow-cidr", "", "private client subnet allowed to access the service in LAN UI mode (for example 192.168.1.0/24)")
	dataDir := flag.String("data-dir", envOr("BOOTOPTIM_DATA_DIR", "./data"), "directory for private CAS objects and SQLite metadata")
	flag.Parse()

	allowedNetwork, err := validateAdminUIExposureMode(*devAdminUI, *adminUILAN, *listen, *adminUIAllowCIDR)
	if err != nil {
		log.Fatal(err)
	}
	adminUIEnabled := *devAdminUI || *adminUILAN

	var model adminui.ReadModel = adminui.EmptyReadModel{}
	var store *storage.SQLiteStore
	if adminUIEnabled {
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
	handler := securityHeaders(newHandlerWithConfig(handlerConfig{
		AdminUIEnabled: adminUIEnabled,
		AdminUIModel:   model,
	}))
	if allowedNetwork != nil {
		handler = restrictToCIDR(handler, allowedNetwork)
	}
	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Printf("bootoptim distribution %s (%s) listening on %s", buildVersion, buildCommit, *listen)
	if adminUIEnabled {
		log.Printf("admin UI enabled in read-only mode; state directory %s", *dataDir)
	}
	if *adminUILAN {
		log.Printf("direct LAN mode allows client addresses in %s; use firewall rules and do not port-forward this listener", allowedNetwork)
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

// validateAdminUIExposure preserves the original loopback-only validation
// entrypoint for existing callers and tests.
func validateAdminUIExposure(enabled bool, listen string) error {
	_, err := validateAdminUIExposureMode(enabled, false, listen, "")
	return err
}

func validateAdminUIExposureMode(devUI, lanUI bool, listen, allowedCIDR string) (*net.IPNet, error) {
	if devUI && lanUI {
		return nil, errors.New("choose either --dev-admin-ui or --admin-ui-lan, not both")
	}
	if !devUI && !lanUI {
		if allowedCIDR != "" {
			return nil, errors.New("--admin-ui-allow-cidr requires --admin-ui-lan")
		}
		return nil, nil
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("admin UI requires a literal host:port listener: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, errors.New("admin UI listener host must be a literal IP address")
	}
	if devUI {
		if !ip.IsLoopback() {
			return nil, errors.New("development admin UI requires a literal loopback listener such as 127.0.0.1 or ::1")
		}
		return nil, nil
	}
	if !ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast() {
		return nil, errors.New("LAN admin UI requires a specific RFC1918 or IPv6 ULA listener address")
	}
	if allowedCIDR == "" {
		return nil, errors.New("LAN admin UI requires --admin-ui-allow-cidr with the trusted private subnet")
	}
	_, network, err := net.ParseCIDR(allowedCIDR)
	if err != nil {
		return nil, fmt.Errorf("parse --admin-ui-allow-cidr: %w", err)
	}
	if !isPrivateNetwork(network) {
		return nil, errors.New("--admin-ui-allow-cidr must describe a subnet contained in RFC1918 or IPv6 ULA space")
	}
	if !network.Contains(ip) {
		return nil, errors.New("--listen address must be inside --admin-ui-allow-cidr")
	}
	return network, nil
}

func isPrivateNetwork(network *net.IPNet) bool {
	if network == nil {
		return false
	}
	first := network.IP
	if ip4 := first.To4(); ip4 != nil {
		first = ip4
		mask := network.Mask
		if len(mask) == net.IPv6len {
			mask = mask[12:]
		}
		last := make(net.IP, net.IPv4len)
		for i := range last {
			last[i] = first[i] | ^mask[i]
		}
		return first.IsPrivate() && last.IsPrivate()
	}
	last := make(net.IP, len(first))
	for i := range last {
		last[i] = first[i] | ^network.Mask[i]
	}
	return first.IsPrivate() && last.IsPrivate()
}

func restrictToCIDR(next http.Handler, allowed *net.IPNet) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientHost, _, err := net.SplitHostPort(r.RemoteAddr)
		clientIP := net.ParseIP(clientHost)
		if err != nil || clientIP == nil || !allowed.Contains(clientIP) {
			http.Error(w, "client address is outside the configured LAN", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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
