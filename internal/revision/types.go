package revision

import "encoding/json"

const (
	CanonicalizationRFC8785 = "RFC8785-JCS"
	SignatureAlgorithm      = "Ed25519"
	MaxInheritanceBases     = 8
)

type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	Revision      Revision       `json:"revision"`
	Profile       Profile        `json:"profile"`
	Game          Game           `json:"game"`
	Base          *ParentRef     `json:"base"`
	Permissions   Permissions    `json:"permissions"`
	Mods          []ModEntry     `json:"mods"`
	RemoveMods    []RemoveMod    `json:"remove_mods"`
	Configs       []ConfigEntry  `json:"configs"`
	RemoveConfigs []RemoveConfig `json:"remove_configs"`
	Objects       []ObjectEntry  `json:"objects"`
}

type Revision struct {
	ID           string `json:"id"`
	Sequence     int64  `json:"sequence"`
	CreatedAt    string `json:"created_at"`
	ReleaseNotes string `json:"release_notes,omitempty"`
}

type Profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Official bool   `json:"official"`
}

type Game struct {
	Minecraft string `json:"minecraft"`
	NeoForge  string `json:"neoforge"`
}

type ParentRef struct {
	ProfileID      string `json:"profile_id"`
	RevisionID     string `json:"revision_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

type Permissions struct {
	DeriveLocal         bool              `json:"derive_local"`
	Mods                ModPermissions    `json:"mods"`
	Configs             ConfigPermissions `json:"configs"`
	MaxInheritanceDepth int               `json:"max_inheritance_depth"`
}

type ModPermissions struct {
	Add    bool `json:"add"`
	Remove bool `json:"remove"`
}

type ConfigPermissions struct {
	OverrideEnforced    bool `json:"override_enforced"`
	OverrideDefaultOnce bool `json:"override_default_once"`
}

type ObjectRef struct {
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type,omitempty"`
}

type ModEntry struct {
	ID                     string    `json:"id"`
	Path                   string    `json:"path"`
	Object                 ObjectRef `json:"object"`
	ExpectBaseObjectSHA256 *string   `json:"expect_base_object_sha256,omitempty"`
}

type RemoveMod struct {
	ID                     string `json:"id"`
	ExpectBaseObjectSHA256 string `json:"expect_base_object_sha256"`
}

type ConfigEntry struct {
	Path                   string    `json:"path"`
	Object                 ObjectRef `json:"object"`
	Policy                 string    `json:"policy"`
	ExpectBaseObjectSHA256 *string   `json:"expect_base_object_sha256,omitempty"`
}

type RemoveConfig struct {
	Path                   string `json:"path"`
	ExpectBaseObjectSHA256 string `json:"expect_base_object_sha256"`
}

type ObjectEntry struct {
	ID                     string    `json:"id"`
	Path                   string    `json:"path"`
	Object                 ObjectRef `json:"object"`
	ExpectBaseObjectSHA256 *string   `json:"expect_base_object_sha256,omitempty"`
}

type Signature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type SignedRevisionEnvelope struct {
	Canonicalization string          `json:"canonicalization"`
	ManifestSHA256   string          `json:"manifest_sha256"`
	Manifest         json.RawMessage `json:"manifest"`
	Signature        Signature       `json:"signature"`
}

type RollbackStatement struct {
	ProfileID      string `json:"profile_id"`
	Channel        string `json:"channel"`
	FromRevisionID string `json:"from_revision_id"`
	FromSequence   int64  `json:"from_sequence"`
	ToRevisionID   string `json:"to_revision_id"`
	Reason         string `json:"reason"`
	IssuedAt       string `json:"issued_at"`
}

type RollbackEvent struct {
	EventID         string          `json:"event_id"`
	StatementSHA256 string          `json:"statement_sha256"`
	Statement       json.RawMessage `json:"statement"`
	Signature       Signature       `json:"signature"`
}

type ChannelState struct {
	ProfileID      string
	Channel        string
	RevisionID     string
	Sequence       int64
	ManifestSHA256 string
}

type VerifiedRevision struct {
	manifest  Manifest
	digest    string
	canonical []byte
	signature Signature
}

func (v *VerifiedRevision) Manifest() Manifest        { return cloneManifest(v.manifest) }
func (v *VerifiedRevision) ManifestSHA256() string    { return v.digest }
func (v *VerifiedRevision) CanonicalManifest() []byte { return append([]byte(nil), v.canonical...) }
func (v *VerifiedRevision) Signature() Signature      { return v.signature }
func (v *VerifiedRevision) ProfileID() string         { return v.manifest.Profile.ID }
func (v *VerifiedRevision) RevisionID() string        { return v.manifest.Revision.ID }
func (v *VerifiedRevision) Sequence() int64           { return v.manifest.Revision.Sequence }

func (v *VerifiedRevision) Parent() *ParentRef {
	if v.manifest.Base == nil {
		return nil
	}
	p := *v.manifest.Base
	return &p
}

type VerifiedRollbackEvent struct {
	eventID   string
	digest    string
	statement RollbackStatement
	signature Signature
	canonical []byte
}

func (v *VerifiedRollbackEvent) EventID() string              { return v.eventID }
func (v *VerifiedRollbackEvent) StatementSHA256() string      { return v.digest }
func (v *VerifiedRollbackEvent) Statement() RollbackStatement { return v.statement }
func (v *VerifiedRollbackEvent) Signature() Signature         { return v.signature }
func (v *VerifiedRollbackEvent) CanonicalStatement() []byte {
	return append([]byte(nil), v.canonical...)
}

func cloneManifest(m Manifest) Manifest {
	out := m
	if m.Base != nil {
		base := *m.Base
		out.Base = &base
	}
	out.Mods = append([]ModEntry(nil), m.Mods...)
	out.RemoveMods = append([]RemoveMod(nil), m.RemoveMods...)
	out.Configs = append([]ConfigEntry(nil), m.Configs...)
	out.RemoveConfigs = append([]RemoveConfig(nil), m.RemoveConfigs...)
	out.Objects = append([]ObjectEntry(nil), m.Objects...)
	for i := range out.Mods {
		out.Mods[i].ExpectBaseObjectSHA256 = cloneStringPtr(out.Mods[i].ExpectBaseObjectSHA256)
	}
	for i := range out.Configs {
		out.Configs[i].ExpectBaseObjectSHA256 = cloneStringPtr(out.Configs[i].ExpectBaseObjectSHA256)
	}
	for i := range out.Objects {
		out.Objects[i].ExpectBaseObjectSHA256 = cloneStringPtr(out.Objects[i].ExpectBaseObjectSHA256)
	}
	return out
}

func cloneStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}
