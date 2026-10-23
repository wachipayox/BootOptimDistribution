package profileapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	_ "image/jpeg" // registers the JPEG decoder for image.DecodeConfig
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

// Limits of the 0.4.0 presentation contract (docs/PROFILE_PROTOCOL.md, section 2).
const (
	maxIconBytes    = 2 * 1024 * 1024
	maxArtworkBytes = 6 * 1024 * 1024
	maxLogoBytes    = 2 * 1024 * 1024
	artworkWidth    = 1920
	artworkHeight   = 760
	maxInheritDepth = 8
)

type artworkRef struct {
	Image  revision.ObjectRef `json:"image"`
	Author string             `json:"author"`
}

// profilePresentation is the own (mutable, unsigned) presentation of a profile.
type profilePresentation struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Icon        *revision.ObjectRef `json:"icon"`
	Artwork     *artworkRef         `json:"artwork"`
	Logo        *revision.ObjectRef `json:"logo"`
}

type inheritedFrom struct {
	Icon    *string `json:"icon"`
	Artwork *string `json:"artwork"`
	Logo    *string `json:"logo"`
}

type presentationStore interface {
	ProfilePresentation(context.Context, string) ([]byte, error)
	SetProfilePresentation(context.Context, string, []byte, string, []storage.PresentationAsset) error
}

type lineageStore interface {
	LatestRevisionAny(context.Context, string) (storage.StoredRevision, error)
}

// presentationRequest keeps every field raw so that an absent field (no
// change) differs from an explicit null (remove the own value).
type presentationRequest struct {
	Name        json.RawMessage `json:"name"`
	Description json.RawMessage `json:"description"`
	Icon        json.RawMessage `json:"icon"`
	Artwork     json.RawMessage `json:"artwork"`
	Logo        json.RawMessage `json:"logo"`
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

func strictUnmarshal(raw json.RawMessage, dst interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

// singleLine reports a non-empty one-line text of at most max bytes.
func singleLine(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n")
}

func (a *API) handlePresentation(w http.ResponseWriter, r *http.Request, id string) {
	store, ok := a.store.(presentationStore)
	if !ok {
		a.writeError(w, 503, "unavailable", "presentation storage unavailable")
		return
	}
	head, err := a.store.ProfileHead(r.Context(), id)
	if err != nil {
		a.writeError(w, 404, "profile_not_found", "profile not found")
		return
	}
	var req presentationRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeError(w, 400, "invalid_request", err.Error())
		return
	}
	// Start from the stored own presentation; a never-edited profile starts
	// from its signed name and description.
	var p profilePresentation
	raw, err := store.ProfilePresentation(r.Context(), id)
	if err != nil {
		a.writeError(w, 500, "storage_error", "profile presentation unavailable")
		return
	}
	if len(raw) > 0 {
		if json.Unmarshal(raw, &p) != nil {
			a.writeError(w, 500, "storage_error", "invalid stored presentation")
			return
		}
	} else {
		var signed revision.Manifest
		if json.Unmarshal(head.Manifest, &signed) == nil {
			p.Name, p.Description = signed.Profile.Name, signed.Profile.Description
		}
	}
	if req.Name != nil {
		var name string
		if json.Unmarshal(req.Name, &name) != nil {
			a.writeError(w, 422, "invalid_presentation", "invalid name or description")
			return
		}
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 384 {
			a.writeError(w, 422, "invalid_presentation", "invalid name or description")
			return
		}
		p.Name = name
	}
	if req.Description != nil {
		var description string
		if !isNull(req.Description) && json.Unmarshal(req.Description, &description) != nil {
			a.writeError(w, 422, "invalid_presentation", "invalid name or description")
			return
		}
		if len(description) > 8192 {
			a.writeError(w, 422, "invalid_presentation", "invalid name or description")
			return
		}
		p.Description = description
	}
	if req.Icon != nil {
		if p.Icon, err = a.parseIcon(r.Context(), req.Icon); err != nil {
			a.writeError(w, 422, "invalid_icon", err.Error())
			return
		}
	}
	if req.Artwork != nil {
		if p.Artwork, err = a.parseArtwork(r.Context(), req.Artwork); err != nil {
			a.writeError(w, 422, "invalid_artwork", err.Error())
			return
		}
	}
	if req.Logo != nil {
		if p.Logo, err = a.parseLogo(r.Context(), req.Logo); err != nil {
			a.writeError(w, 422, "invalid_logo", err.Error())
			return
		}
	}
	icon := ""
	if p.Icon != nil {
		icon = p.Icon.SHA256
	}
	var assets []storage.PresentationAsset
	if p.Artwork != nil {
		assets = append(assets, storage.PresentationAsset{Kind: "artwork", SHA256: p.Artwork.Image.SHA256, Size: p.Artwork.Image.Size})
	}
	if p.Logo != nil {
		assets = append(assets, storage.PresentationAsset{Kind: "logo", SHA256: p.Logo.SHA256, Size: p.Logo.Size})
	}
	raw, _ = json.Marshal(p)
	if err := store.SetProfilePresentation(r.Context(), id, raw, icon, assets); err != nil {
		a.writeError(w, 500, "storage_error", "could not save presentation")
		return
	}
	a.writeJSON(w, 200, p)
}

func (a *API) parseIcon(ctx context.Context, raw json.RawMessage) (*revision.ObjectRef, error) {
	if isNull(raw) {
		return nil, nil
	}
	var icon revision.ObjectRef
	if err := strictUnmarshal(raw, &icon); err != nil {
		return nil, errors.New("invalid icon")
	}
	if icon.MediaType != "image/png" || icon.Size < 1 || icon.Size > maxIconBytes {
		return nil, errors.New("invalid icon")
	}
	if err := a.validateProfileIcon(ctx, &icon); err != nil {
		return nil, err
	}
	// CAS bytes must match the declared digest and length before publication.
	if err := a.objects.Verify(ctx, storage.Object{SHA256: icon.SHA256, Size: icon.Size}); err != nil {
		return nil, errors.New("icon integrity check failed")
	}
	return &icon, nil
}

func (a *API) verifyImage(ctx context.Context, ref revision.ObjectRef) error {
	if err := a.objects.Verify(ctx, storage.Object{SHA256: ref.SHA256, Size: ref.Size}); err != nil {
		return errors.New("image integrity check failed")
	}
	return nil
}

func (a *API) parseArtwork(ctx context.Context, raw json.RawMessage) (*artworkRef, error) {
	if isNull(raw) {
		return nil, nil
	}
	var artwork artworkRef
	if err := strictUnmarshal(raw, &artwork); err != nil {
		return nil, errors.New("invalid artwork")
	}
	if !singleLine(artwork.Author, 64) {
		return nil, errors.New("artwork author must be 1 to 64 bytes on one line")
	}
	ref := artwork.Image
	if ref.MediaType != "image/jpeg" && ref.MediaType != "image/png" {
		return nil, errors.New("artwork must be JPEG or PNG")
	}
	if ref.Size < 1 || ref.Size > maxArtworkBytes {
		return nil, errors.New("invalid artwork size")
	}
	f, size, err := a.objects.Open(ctx, ref.SHA256)
	if err != nil {
		return nil, errors.New("artwork object not found")
	}
	defer f.Close()
	if size != ref.Size {
		return nil, errors.New("artwork size does not match the object")
	}
	cfg, format, err := image.DecodeConfig(io.LimitReader(f, maxArtworkBytes+1))
	if err != nil {
		return nil, errors.New("artwork is not a valid JPEG or PNG")
	}
	if "image/"+format != ref.MediaType {
		return nil, errors.New("artwork content does not match its media type")
	}
	if cfg.Width != artworkWidth || cfg.Height != artworkHeight {
		return nil, errors.New("artwork must be exactly 1920 by 760 pixels")
	}
	if err := a.verifyImage(ctx, ref); err != nil {
		return nil, err
	}
	return &artwork, nil
}

// hasAlpha reports whether a decoded PNG header describes transparency.
func hasAlpha(model color.Model) bool {
	if palette, ok := model.(color.Palette); ok {
		for _, c := range palette {
			if _, _, _, alpha := c.RGBA(); alpha != 0xffff {
				return true
			}
		}
		return false
	}
	return model == color.NRGBAModel || model == color.NRGBA64Model
}

func (a *API) parseLogo(ctx context.Context, raw json.RawMessage) (*revision.ObjectRef, error) {
	if isNull(raw) {
		return nil, nil
	}
	var logo revision.ObjectRef
	if err := strictUnmarshal(raw, &logo); err != nil {
		return nil, errors.New("invalid logo")
	}
	if logo.MediaType != "image/png" {
		return nil, errors.New("logo must be PNG")
	}
	if logo.Size < 1 || logo.Size > maxLogoBytes {
		return nil, errors.New("invalid logo size")
	}
	f, size, err := a.objects.Open(ctx, logo.SHA256)
	if err != nil {
		return nil, errors.New("logo object not found")
	}
	defer f.Close()
	if size != logo.Size {
		return nil, errors.New("logo size does not match the object")
	}
	cfg, err := png.DecodeConfig(io.LimitReader(f, maxLogoBytes+1))
	if err != nil {
		return nil, errors.New("logo is not a valid PNG")
	}
	if cfg.Width < 64 || cfg.Width > 1600 || cfg.Height < 32 || cfg.Height > 800 {
		return nil, errors.New("logo must be 64-1600 pixels wide and 32-800 pixels tall")
	}
	if !hasAlpha(cfg.ColorModel) {
		return nil, errors.New("logo must be a PNG with transparency")
	}
	if err := a.verifyImage(ctx, logo); err != nil {
		return nil, err
	}
	return &logo, nil
}

// ---------------------------------------------------------------------------
// Catalog: effective values with inheritance.

type lineageInfo struct {
	parent     string
	signedIcon *revision.ObjectRef
}

type presentationResolver struct {
	a    *API
	ctx  context.Context
	own  map[string]*profilePresentation
	info map[string]lineageInfo
}

func (a *API) newPresentationResolver(ctx context.Context) *presentationResolver {
	return &presentationResolver{a: a, ctx: ctx, own: map[string]*profilePresentation{}, info: map[string]lineageInfo{}}
}

func (r *presentationResolver) ownOf(id string) (*profilePresentation, error) {
	if p, ok := r.own[id]; ok {
		return p, nil
	}
	var p *profilePresentation
	if store, ok := r.a.store.(presentationStore); ok {
		raw, err := store.ProfilePresentation(r.ctx, id)
		if err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			p = &profilePresentation{}
			if json.Unmarshal(raw, p) != nil {
				return nil, errors.New("invalid stored presentation")
			}
		}
	}
	r.own[id] = p
	return p, nil
}

func (r *presentationResolver) lineage(id string) (lineageInfo, error) {
	if info, ok := r.info[id]; ok {
		return info, nil
	}
	var info lineageInfo
	if store, ok := r.a.store.(lineageStore); ok {
		stored, err := store.LatestRevisionAny(r.ctx, id)
		if err != nil && !errors.Is(err, storage.ErrRevisionNotFound) {
			return info, err
		}
		if err == nil {
			var manifest struct {
				Base *struct {
					ProfileID string `json:"profile_id"`
				} `json:"base"`
				Profile struct {
					Icon *revision.ObjectRef `json:"icon"`
				} `json:"profile"`
			}
			if json.Unmarshal(stored.Manifest, &manifest) == nil {
				if manifest.Base != nil {
					info.parent = manifest.Base.ProfileID
				}
				info.signedIcon = manifest.Profile.Icon
			}
		}
	}
	r.info[id] = info
	return info, nil
}

// apply fills the presentation fields of a catalog entry.
func (r *presentationResolver) apply(view *profileView, admin bool) error {
	chain := []string{view.ProfileID}
	for len(chain) <= maxInheritDepth {
		info, err := r.lineage(chain[len(chain)-1])
		if err != nil {
			return err
		}
		if info.parent == "" || info.parent == view.ProfileID {
			break
		}
		chain = append(chain, info.parent)
	}
	if info, err := r.lineage(view.ProfileID); err != nil {
		return err
	} else if info.parent != "" {
		parent := info.parent
		view.ParentProfileID = &parent
	}
	view.Icon, view.Artwork, view.Logo = nil, nil, nil
	view.InheritedFrom = inheritedFrom{}
	for depth, id := range chain {
		own, err := r.ownOf(id)
		if err != nil {
			return err
		}
		if depth == 0 && own != nil {
			if own.Name != "" {
				view.Name = own.Name
			}
			view.Description = own.Description
			if admin {
				view.OwnPresentation = own
			}
		}
		from := func() *string {
			if depth == 0 {
				return nil
			}
			value := id
			return &value
		}
		if view.Icon == nil {
			var icon *revision.ObjectRef
			if own != nil {
				icon = own.Icon
			}
			if icon == nil {
				info, err := r.lineage(id)
				if err != nil {
					return err
				}
				icon = info.signedIcon
			}
			if icon != nil {
				view.Icon, view.InheritedFrom.Icon = icon, from()
			}
		}
		if view.Artwork == nil && own != nil && own.Artwork != nil {
			view.Artwork, view.InheritedFrom.Artwork = own.Artwork, from()
		}
		if view.Logo == nil && own != nil && own.Logo != nil {
			view.Logo, view.InheritedFrom.Logo = own.Logo, from()
		}
	}
	return nil
}

func (a *API) applyPresentations(ctx context.Context, views []profileView, admin bool) error {
	resolver := a.newPresentationResolver(ctx)
	for i := range views {
		if err := resolver.apply(&views[i], admin); err != nil {
			return err
		}
	}
	return nil
}

// writeCatalog sends the catalog. The public one carries an ETag over the body.
func (a *API) writeCatalog(w http.ResponseWriter, r *http.Request, admin bool, body interface{}) {
	if admin {
		a.writeJSON(w, http.StatusOK, body)
		return
	}
	raw, err := json.Marshal(body)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "catalog unavailable")
		return
	}
	raw = append(raw, '\n')
	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-BootOptim-Protocol-Version", strconv.Itoa(ProtocolVersion))
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
