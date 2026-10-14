package profileapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
)

// followResult reports one automatic child re-pin performed after a parent
// publication. Status is "published" or "skipped"; skipped entries carry the
// stable error code and message so the admin can fix the child by hand.
type followResult struct {
	ProfileID       string       `json:"profile_id"`
	ParentProfileID string       `json:"parent_profile_id"`
	ParentRevision  string       `json:"parent_revision_id"`
	Status          string       `json:"status"`
	Revision        *revisionRef `json:"revision,omitempty"`
	Code            string       `json:"code,omitempty"`
	Message         string       `json:"message,omitempty"`
}

// followParent publishes, for every global profile whose latest revision
// inherits from an older revision of the just-published profile, a new signed
// revision with the same own content re-pinned to the new parent. It recurses
// to grandchildren up to the inheritance cap. Failures never affect the parent
// publication: the child is skipped and a warning is returned and logged.
func (a *API) followParent(ctx context.Context, published *revision.VerifiedRevision) []followResult {
	stored, err := a.store.ListSignedRevisions(ctx, 1000)
	if err != nil {
		log.Printf("auto-follow: profile inventory unavailable after publishing %s: %v", published.ProfileID(), err)
		return []followResult{{ProfileID: "*", ParentProfileID: published.ProfileID(), ParentRevision: published.RevisionID(),
			Status: "skipped", Code: "storage_error", Message: "profile inventory unavailable; children were not re-pinned"}}
	}
	// Rows are ordered by profile, sequence DESC: the first row is the latest.
	latest := make(map[string]revision.Manifest)
	order := make([]string, 0)
	for _, item := range stored {
		if _, ok := latest[item.ProfileID]; ok {
			continue
		}
		var manifest revision.Manifest
		if json.Unmarshal(item.Manifest, &manifest) != nil {
			continue
		}
		latest[item.ProfileID] = manifest
		order = append(order, item.ProfileID)
	}

	type pending struct {
		parent *revision.VerifiedRevision
		depth  int
	}
	queue := []pending{{published, 0}}
	visited := map[string]bool{published.ProfileID(): true}
	var results []followResult
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth >= revision.MaxInheritanceBases {
			continue
		}
		parentID := current.parent.ProfileID()
		for _, childID := range order {
			child := latest[childID]
			if visited[childID] || child.Base == nil || child.Base.ProfileID != parentID ||
				child.Base.RevisionID == current.parent.RevisionID() {
				continue
			}
			visited[childID] = true
			result := followResult{ProfileID: childID, ParentProfileID: parentID, ParentRevision: current.parent.RevisionID()}
			verified, code, message := a.repinChild(ctx, child, current.parent)
			if verified == nil {
				result.Status, result.Code, result.Message = "skipped", code, message
				log.Printf("auto-follow: WARN child %s not re-pinned to %s rev %s: %s: %s", childID, parentID, current.parent.RevisionID(), code, message)
			} else {
				result.Status = "published"
				result.Revision = &revisionRef{RevisionID: verified.RevisionID(), Sequence: verified.Sequence(), ManifestSHA256: verified.ManifestSHA256()}
				latest[childID] = verified.Manifest()
				queue = append(queue, pending{verified, current.depth + 1})
			}
			results = append(results, result)
		}
	}
	return results
}

func (a *API) repinChild(ctx context.Context, child revision.Manifest, parent *revision.VerifiedRevision) (*revision.VerifiedRevision, string, string) {
	if a.signer == nil {
		return nil, "publication_unavailable", "automatic signing is unavailable"
	}
	id, err := randomRevisionID()
	if err != nil {
		return nil, "internal_error", "revision id could not be generated"
	}
	child.Base = &revision.ParentRef{ProfileID: parent.ProfileID(), RevisionID: parent.RevisionID(), ManifestSHA256: parent.ManifestSHA256()}
	// A child must match its parent's game versions, so it follows them too.
	child.Game = parent.Manifest().Game
	child.Revision = revision.Revision{
		ID: id, Sequence: child.Revision.Sequence + 1, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		ReleaseNotes: "auto: sigue a " + parent.ProfileID() + " rev " + parent.RevisionID(),
	}
	raw, err := json.Marshal(child)
	if err != nil {
		return nil, "invalid_manifest", err.Error()
	}
	canonical, err := revision.CanonicalizeValidatedManifest(raw)
	if err != nil {
		return nil, "invalid_manifest", "re-pinned child manifest is invalid"
	}
	digest := sha256.Sum256(canonical)
	request, err := json.Marshal(map[string]any{
		"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": hex.EncodeToString(digest[:]), "manifest": json.RawMessage(canonical),
	})
	if err != nil {
		return nil, "invalid_manifest", err.Error()
	}
	envelope, err := a.signer.SignRequest(request)
	if err != nil {
		return nil, "signing_failed", "re-pinned child manifest could not be signed"
	}
	capture := &captureWriter{header: http.Header{}}
	verified, _ := a.publishCore(capture, ctx, envelope)
	if verified == nil {
		var body errorBody
		if json.Unmarshal(capture.body.Bytes(), &body) != nil || body.Error.Code == "" {
			return nil, "publish_failed", "re-pinned child was rejected"
		}
		return nil, body.Error.Code, body.Error.Message
	}
	return verified, "", ""
}

func randomRevisionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "rev_" + hex.EncodeToString(b[:]), nil
}

// captureWriter records the error response of an internal publication.
type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header         { return c.header }
func (c *captureWriter) Write(p []byte) (int, error) { return c.body.Write(p) }
func (c *captureWriter) WriteHeader(status int)      { c.status = status }
