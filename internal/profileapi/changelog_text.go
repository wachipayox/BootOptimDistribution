package profileapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

const (
	maxChangelogTitle       = 120
	maxChangelogDescription = 8192
	maxChangelogChanges     = 100
	maxChangelogChange      = 300
	autoReleaseNotesPrefix  = "auto: sigue a "
)

type changelogText struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Changes     []string `json:"changes"`
}

type changelogStore interface {
	ProfileRevisionsAscending(context.Context, string) ([]storage.StoredSignedRevision, error)
	RevisionChangelogs(context.Context, []string) (map[string]storage.RevisionChangelog, error)
	SetRevisionChangelog(context.Context, string, storage.RevisionChangelog) (storage.RevisionChangelog, error)
}

func hasControl(value string, allowNewlines bool) bool {
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' {
			if r == '\t' || allowNewlines {
				continue
			}
			return true
		}
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// validateChangelogText applies the contract limits (bytes, one line, counts)
// and returns the normalized text (trimmed one-liners, LF line endings).
func validateChangelogText(in changelogText) (changelogText, error) {
	out := changelogText{Changes: []string{}}
	out.Title = strings.TrimSpace(in.Title)
	if !utf8.ValidString(out.Title) || len(out.Title) > maxChangelogTitle || hasControl(out.Title, false) {
		return out, fmt.Errorf("title must be a single line of at most %d bytes", maxChangelogTitle)
	}
	out.Description = strings.TrimSpace(strings.ReplaceAll(in.Description, "\r\n", "\n"))
	if !utf8.ValidString(out.Description) || len(out.Description) > maxChangelogDescription || hasControl(out.Description, true) {
		return out, fmt.Errorf("description must be plain text of at most %d bytes", maxChangelogDescription)
	}
	if len(in.Changes) > maxChangelogChanges {
		return out, fmt.Errorf("changes accepts at most %d items", maxChangelogChanges)
	}
	for i, change := range in.Changes {
		change = strings.TrimSpace(change)
		if change == "" || !utf8.ValidString(change) || len(change) > maxChangelogChange || hasControl(change, false) {
			return out, fmt.Errorf("changes[%d] must be a single line of 1 to %d bytes", i, maxChangelogChange)
		}
		out.Changes = append(out.Changes, change)
	}
	return out, nil
}

func cutBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func isAutoReleaseNotes(notes string) bool {
	return strings.HasPrefix(strings.TrimSpace(notes), autoReleaseNotesPrefix)
}

// deriveChangelogText builds the text of a revision nobody edited from its
// signed release notes: first line = title, "- " lines = changes, the rest =
// description. Automatic follow notes are never shown.
func deriveChangelogText(notes string) changelogText {
	out := changelogText{Changes: []string{}}
	if isAutoReleaseNotes(notes) {
		return out
	}
	lines := strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start < len(lines) && !strings.HasPrefix(lines[start], "- ") {
		out.Title = cutBytes(strings.TrimSpace(lines[start]), maxChangelogTitle)
		start++
	}
	var rest []string
	for _, line := range lines[start:] {
		if strings.HasPrefix(line, "- ") {
			change := cutBytes(strings.TrimSpace(line[2:]), maxChangelogChange)
			if change != "" && len(out.Changes) < maxChangelogChanges {
				out.Changes = append(out.Changes, change)
			}
			continue
		}
		rest = append(rest, strings.TrimRight(line, " \t"))
	}
	out.Description = cutBytes(strings.TrimSpace(strings.Join(rest, "\n")), maxChangelogDescription)
	return out
}

// handleRevisionChangelog serves PUT /v1/admin/revisions/{revision_id}/changelog.
func (a *API) handleRevisionChangelog(w http.ResponseWriter, r *http.Request, revisionID string) {
	store, ok := a.store.(changelogStore)
	if !ok {
		a.writeError(w, http.StatusNotImplemented, "changelog_unavailable", "this store does not keep changelogs")
		return
	}
	var req changelogText
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	text, err := validateChangelogText(req)
	if err != nil {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_changelog", err.Error())
		return
	}
	saved, err := store.SetRevisionChangelog(r.Context(), revisionID, storage.RevisionChangelog{
		Title: text.Title, Description: text.Description, Changes: text.Changes,
	})
	if errors.Is(err, storage.ErrRevisionNotFound) {
		a.writeError(w, http.StatusNotFound, "revision_not_found", "revision not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "changelog could not be saved")
		return
	}
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion   int      `json:"schema_version"`
		ProtocolVersion int      `json:"protocol_version"`
		RevisionID      string   `json:"revision_id"`
		Title           string   `json:"title"`
		Description     string   `json:"description"`
		Changes         []string `json:"changes"`
		UpdatedAt       string   `json:"updated_at"`
	}{SchemaVersion, ProtocolVersion, revisionID, saved.Title, saved.Description, saved.Changes, saved.UpdatedAt.Format("2006-01-02T15:04:05Z")})
}
