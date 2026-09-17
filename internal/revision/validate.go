package revision

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

func validateManifestValues(m Manifest) error {
	if m.SchemaVersion != 1 {
		return errors.New("schema_version must equal 1")
	}
	if !validRevisionID(m.Revision.ID) || m.Revision.Sequence < 1 {
		return errors.New("revision id or sequence is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, m.Revision.CreatedAt); err != nil {
		return errors.New("revision.created_at must be RFC3339 date-time")
	}
	if len(m.Revision.ReleaseNotes) > 8192 {
		return errors.New("revision.release_notes exceeds 8192 bytes")
	}
	if !validProfileID(m.Profile.ID) || len(m.Profile.Name) == 0 || len(m.Profile.Name) > 96 || !m.Profile.Official {
		return errors.New("profile is invalid or not official")
	}
	if len(m.Game.Minecraft) == 0 || len(m.Game.Minecraft) > 64 || len(m.Game.NeoForge) == 0 || len(m.Game.NeoForge) > 64 {
		return errors.New("game versions must be non-empty and at most 64 bytes")
	}
	if m.Permissions.MaxInheritanceDepth < 0 || m.Permissions.MaxInheritanceDepth > MaxInheritanceBases {
		return errors.New("permissions.max_inheritance_depth is outside 0..8")
	}
	if m.Base != nil {
		if !validProfileID(m.Base.ProfileID) || !validRevisionID(m.Base.RevisionID) || !isSHA256(m.Base.ManifestSHA256) {
			return errors.New("base pin is malformed")
		}
	} else if len(m.RemoveMods) != 0 || len(m.RemoveConfigs) != 0 {
		return errors.New("root manifest cannot contain removals")
	}

	modIDs := make(map[string]struct{}, len(m.Mods))
	paths := make(map[string]string, len(m.Mods)+len(m.Configs)+len(m.Objects))
	for i, entry := range m.Mods {
		if !validEntryID(entry.ID, "mod_") || !safePath(entry.Path) || !validObjectRef(entry.Object) {
			return fmt.Errorf("mods[%d] is invalid", i)
		}
		if _, exists := modIDs[entry.ID]; exists {
			return fmt.Errorf("duplicate mod id %q", entry.ID)
		}
		modIDs[entry.ID] = struct{}{}
		if err := addPath(paths, entry.Path, "mod"); err != nil {
			return err
		}
		if err := validateOptionalDigest(entry.ExpectBaseObjectSHA256); err != nil {
			return fmt.Errorf("mods[%d]: %v", i, err)
		}
		if m.Base == nil && entry.ExpectBaseObjectSHA256 != nil {
			return fmt.Errorf("root mods[%d] cannot expect a base object", i)
		}
	}
	for i, entry := range m.RemoveMods {
		if !validEntryID(entry.ID, "mod_") || !isSHA256(entry.ExpectBaseObjectSHA256) {
			return fmt.Errorf("remove_mods[%d] is invalid", i)
		}
	}
	for i, entry := range m.Configs {
		if !safePath(entry.Path) || !validObjectRef(entry.Object) || (entry.Policy != "enforced" && entry.Policy != "default_once") {
			return fmt.Errorf("configs[%d] is invalid", i)
		}
		if err := addPath(paths, entry.Path, "config"); err != nil {
			return err
		}
		if err := validateOptionalDigest(entry.ExpectBaseObjectSHA256); err != nil {
			return fmt.Errorf("configs[%d]: %v", i, err)
		}
		if m.Base == nil && entry.ExpectBaseObjectSHA256 != nil {
			return fmt.Errorf("root configs[%d] cannot expect a base object", i)
		}
	}
	for i, entry := range m.RemoveConfigs {
		if !safePath(entry.Path) || !isSHA256(entry.ExpectBaseObjectSHA256) {
			return fmt.Errorf("remove_configs[%d] is invalid", i)
		}
	}
	objectIDs := make(map[string]struct{}, len(m.Objects))
	for i, entry := range m.Objects {
		if !validEntryID(entry.ID, "obj_") || !safePath(entry.Path) || !validObjectRef(entry.Object) {
			return fmt.Errorf("objects[%d] is invalid", i)
		}
		if _, exists := objectIDs[entry.ID]; exists {
			return fmt.Errorf("duplicate object id %q", entry.ID)
		}
		objectIDs[entry.ID] = struct{}{}
		if err := addPath(paths, entry.Path, "object"); err != nil {
			return err
		}
		if err := validateOptionalDigest(entry.ExpectBaseObjectSHA256); err != nil {
			return fmt.Errorf("objects[%d]: %v", i, err)
		}
		if m.Base == nil && entry.ExpectBaseObjectSHA256 != nil {
			return fmt.Errorf("root objects[%d] cannot expect a base object", i)
		}
	}
	return nil
}

func validateOptionalDigest(value *string) error {
	if value != nil && !isSHA256(*value) {
		return errors.New("expected base digest is malformed")
	}
	return nil
}

func addPath(paths map[string]string, path, kind string) error {
	if previous, exists := paths[path]; exists {
		return fmt.Errorf("destination path %q collides between %s and %s", path, previous, kind)
	}
	paths[path] = kind
	return nil
}

func validObjectRef(ref ObjectRef) bool {
	return isSHA256(ref.SHA256) && ref.Size >= 0 && len(ref.MediaType) <= 128
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validProfileID(value string) bool {
	if !strings.HasPrefix(value, "profile_") {
		return false
	}
	tail := strings.TrimPrefix(value, "profile_")
	if len(tail) < 3 || len(tail) > 64 || !isLowerAlphaNum(tail[0]) {
		return false
	}
	for i := 1; i < len(tail); i++ {
		c := tail[i]
		if !isLowerAlphaNum(c) && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func validRevisionID(value string) bool {
	return validEntryIDRange(value, "rev_", 16, 80, false)
}

func validEntryID(value, prefix string) bool {
	return validEntryIDRange(value, prefix, 1, 120, true)
}

func validEntryIDRange(value, prefix string, min, max int, allowDot bool) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	tail := strings.TrimPrefix(value, prefix)
	if len(tail) < min || len(tail) > max {
		return false
	}
	for i := 0; i < len(tail); i++ {
		c := tail[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || (allowDot && c == '.') {
			continue
		}
		return false
	}
	return true
}

func validChannel(value string) bool {
	if len(value) < 1 || len(value) > 32 || !isLowerAlphaNum(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !isLowerAlphaNum(c) && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func isLowerAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func safePath(value string) bool {
	if len(value) == 0 || len(value) > 512 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "//") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == ".." {
			return false
		}
	}
	return true
}

func validateSignature(sig Signature) error {
	if len(sig.KeyID) == 0 || len(sig.KeyID) > 128 {
		return errors.New("signature.key_id must be 1..128 bytes")
	}
	if sig.Algorithm != SignatureAlgorithm {
		return errors.New("signature.algorithm must be Ed25519")
	}
	if strings.Contains(sig.Value, "=") || len(sig.Value) == 0 {
		return errors.New("signature.value must be unpadded base64url")
	}
	return nil
}
