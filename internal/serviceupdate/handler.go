// Package serviceupdate checks the public deployable main branch and lets an
// authenticated administrator request the narrowly-scoped systemd updater.
package serviceupdate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	commitURL = "https://api.github.com/repos/wachipayox/BootOptimDistribution/commits/main"
	rawURL    = "https://raw.githubusercontent.com/wachipayox/BootOptimDistribution/"
)

var (
	commitPattern  = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-.][0-9A-Za-z.-]+)?$`)
)

type Handler struct {
	currentVersion string
	currentCommit  string
	client         *http.Client
	startUpdate    func(context.Context) error
}

type status struct {
	CurrentVersion   string    `json:"current_version"`
	CurrentCommit    string    `json:"current_commit"`
	AvailableVersion string    `json:"available_version"`
	AvailableCommit  string    `json:"available_commit"`
	UpdateAvailable  bool      `json:"update_available"`
	CheckedAt        time.Time `json:"checked_at"`
}

type response struct {
	status
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

func NewHandler(currentVersion, currentCommit string) http.Handler {
	return &Handler{
		currentVersion: currentVersion,
		currentCommit:  currentCommit,
		client:         &http.Client{Timeout: 12 * time.Second},
		startUpdate:    startSystemdUpdate,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch r.Method {
	case http.MethodGet:
		result, err := h.check(r.Context())
		if err != nil {
			log.Printf("admin service update check failed: %v", err)
			writeError(w, http.StatusBadGateway, "update_check_failed", "No se pudo consultar la versión desplegable de main en GitHub.")
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	case http.MethodPost:
		result, err := h.check(r.Context())
		if err != nil {
			log.Printf("admin service update before install failed: %v", err)
			writeError(w, http.StatusBadGateway, "update_check_failed", "No se pudo consultar la versión desplegable de main en GitHub.")
			return
		}
		if !result.UpdateAvailable {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(response{status: result, State: "current", Message: "El servicio ya está actualizado."})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if err := h.startUpdate(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "update_runner_unavailable", "No se pudo iniciar el actualizador del sistema. Comprueba el socket de actualización.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(response{
			status:  result,
			State:   "updating",
			Message: "Actualización iniciada. El servicio se reiniciará al terminar.",
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Método no permitido.")
	}
}

func (h *Handler) check(ctx context.Context) (status, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, commitURL, nil)
	if err != nil {
		return status{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "BootOptim-Distribution-Update-Check")
	commitResponse, err := h.client.Do(request)
	if err != nil {
		return status{}, fmt.Errorf("request GitHub commit: %w", err)
	}
	defer commitResponse.Body.Close()
	if commitResponse.StatusCode != http.StatusOK {
		return status{}, fmt.Errorf("GitHub commit endpoint returned %s", commitResponse.Status)
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(commitResponse.Body, 16*1024)).Decode(&commit); err != nil {
		return status{}, fmt.Errorf("decode GitHub commit response: %w", err)
	}
	commit.SHA = strings.ToLower(commit.SHA)
	if !commitPattern.MatchString(commit.SHA) {
		return status{}, fmt.Errorf("GitHub returned an invalid commit SHA %q", commit.SHA)
	}

	versionRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL+commit.SHA+"/VERSION", nil)
	if err != nil {
		return status{}, err
	}
	versionRequest.Header.Set("User-Agent", "BootOptim-Distribution-Update-Check")
	versionResponse, err := h.client.Do(versionRequest)
	if err != nil {
		return status{}, fmt.Errorf("request GitHub VERSION: %w", err)
	}
	defer versionResponse.Body.Close()
	if versionResponse.StatusCode != http.StatusOK {
		return status{}, fmt.Errorf("GitHub VERSION endpoint returned %s", versionResponse.Status)
	}
	versionBytes, err := io.ReadAll(io.LimitReader(versionResponse.Body, 256))
	if err != nil {
		return status{}, fmt.Errorf("read GitHub VERSION response: %w", err)
	}
	version := strings.TrimSpace(string(versionBytes))
	if !versionPattern.MatchString(version) {
		return status{}, fmt.Errorf("GitHub returned an invalid service version %q", version)
	}

	return status{
		CurrentVersion:   h.currentVersion,
		CurrentCommit:    h.currentCommit,
		AvailableVersion: version,
		AvailableCommit:  commit.SHA,
		UpdateAvailable:  commit.SHA != strings.ToLower(h.currentCommit),
		CheckedAt:        time.Now().UTC(),
	}, nil
}

func startSystemdUpdate(ctx context.Context) error {
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", "/run/bootoptim-distribution-update.sock")
	if err != nil {
		return errors.New("systemd update trigger is unavailable")
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return errors.New("could not set update trigger deadline")
	}
	if _, err := io.WriteString(connection, "update\n"); err != nil {
		return errors.New("could not contact systemd update trigger")
	}
	result, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil || result != "started\n" {
		return errors.New("systemd update unit could not be started")
	}
	return nil
}

func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
