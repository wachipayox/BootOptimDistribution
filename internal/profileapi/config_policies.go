package profileapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

// Panel-only config policy rules (folder rules and file exceptions). They are
// unsigned and launchers never see them: the panel expands them into the
// signed per-file configs[].policy before publishing. See docs/ADMIN_UI.md.

const (
	maxConfigPolicyRules    = 20000
	maxConfigPolicyRulePath = 1024
	configPolicyRoot        = "config/"
)

type configPolicyStore interface {
	ConfigPolicyRules(context.Context, string) ([]storage.ConfigPolicyRule, time.Time, error)
	SetConfigPolicyRules(context.Context, string, []storage.ConfigPolicyRule) (time.Time, error)
}

type deletedProfilesStore interface {
	DeletedProfileIDs(context.Context) ([]string, error)
}

type configPolicyBody struct {
	SchemaVersion   int                        `json:"schema_version"`
	ProtocolVersion int                        `json:"protocol_version"`
	ProfileID       string                     `json:"profile_id"`
	Rules           []storage.ConfigPolicyRule `json:"rules"`
	UpdatedAt       string                     `json:"updated_at,omitempty"`
}

// validateConfigPolicyRules accepts folder rules ("config/…/") and file rules
// ("config/…") under config/, with policy enforced or default_once.
func validateConfigPolicyRules(rules []storage.ConfigPolicyRule) error {
	if len(rules) > maxConfigPolicyRules {
		return fmt.Errorf("at most %d rules", maxConfigPolicyRules)
	}
	seen := make(map[string]struct{}, len(rules))
	for i, rule := range rules {
		if rule.Policy != "enforced" && rule.Policy != "default_once" {
			return fmt.Errorf("rules[%d].policy must be enforced or default_once", i)
		}
		if !validConfigPolicyPath(rule.Path) {
			return fmt.Errorf("rules[%d].path must be a relative path under %s (folders end with /)", i, configPolicyRoot)
		}
		if _, dup := seen[rule.Path]; dup {
			return fmt.Errorf("rules[%d].path is duplicated", i)
		}
		seen[rule.Path] = struct{}{}
	}
	return nil
}

func validConfigPolicyPath(path string) bool {
	if len(path) > maxConfigPolicyRulePath || !strings.HasPrefix(path, configPolicyRoot) || strings.Contains(path, "\\") {
		return false
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if path == configPolicyRoot {
		return true
	}
	trimmed := strings.TrimSuffix(path, "/")
	for _, part := range strings.Split(trimmed, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// handleConfigPolicies serves GET/PUT /v1/admin/profiles/{id}/config-policies.
func (a *API) handleConfigPolicies(w http.ResponseWriter, r *http.Request, profileID string) {
	store, ok := a.store.(configPolicyStore)
	if !ok {
		a.writeError(w, http.StatusNotImplemented, "config_policies_unavailable", "this store does not keep config policy rules")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rules, updated, err := store.ConfigPolicyRules(r.Context(), profileID)
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "storage_error", "config policy rules unavailable")
			return
		}
		body := configPolicyBody{SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion, ProfileID: profileID, Rules: rules}
		if !updated.IsZero() {
			body.UpdatedAt = updated.Format("2006-01-02T15:04:05Z")
		}
		a.writeJSON(w, http.StatusOK, body)
	case http.MethodPut:
		var req struct {
			Rules []storage.ConfigPolicyRule `json:"rules"`
		}
		if err := a.decodeJSON(w, r, &req); err != nil {
			a.writeBodyReadError(w, err)
			return
		}
		if req.Rules == nil {
			req.Rules = []storage.ConfigPolicyRule{}
		}
		if err := validateConfigPolicyRules(req.Rules); err != nil {
			a.writeError(w, http.StatusUnprocessableEntity, "invalid_config_policies", err.Error())
			return
		}
		updated, err := store.SetConfigPolicyRules(r.Context(), profileID, req.Rules)
		if errors.Is(err, storage.ErrRevisionNotFound) {
			a.writeError(w, http.StatusNotFound, "profile_not_found", "profile not found")
			return
		}
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "storage_error", "config policy rules could not be saved")
			return
		}
		a.writeJSON(w, http.StatusOK, configPolicyBody{SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion,
			ProfileID: profileID, Rules: req.Rules, UpdatedAt: updated.Format("2006-01-02T15:04:05Z")})
	default:
		a.methodNotAllowed(w, http.MethodGet, http.MethodPut)
	}
}

// handleDeletedProfiles serves GET /v1/admin/deleted-profiles: identifiers the
// panel must not propose because publication would answer immutable_conflict.
func (a *API) handleDeletedProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.methodNotAllowed(w, http.MethodGet)
		return
	}
	store, ok := a.store.(deletedProfilesStore)
	if !ok {
		a.writeError(w, http.StatusNotImplemented, "deleted_profiles_unavailable", "this store does not keep deleted profiles")
		return
	}
	ids, err := store.DeletedProfileIDs(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "deleted profiles unavailable")
		return
	}
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion     int      `json:"schema_version"`
		ProtocolVersion   int      `json:"protocol_version"`
		DeletedProfileIDs []string `json:"deleted_profile_ids"`
	}{SchemaVersion, ProtocolVersion, ids})
}
