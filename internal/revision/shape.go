package revision

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func validateManifestShape(raw []byte) error {
	root, err := objectShape(raw,
		[]string{"schema_version", "revision", "profile", "game", "base", "permissions", "mods", "remove_mods", "configs", "remove_configs", "objects"},
		[]string{"config_settings", "jvm_options", "remove_jvm_options"})
	if err != nil {
		return err
	}
	revision, err := objectShape(root["revision"], []string{"id", "sequence", "created_at"}, []string{"release_notes"})
	if err != nil {
		return fmt.Errorf("revision: %w", err)
	}
	if err := requireString(revision["id"], "revision.id"); err != nil {
		return err
	}
	if err := requireInteger(revision["sequence"], "revision.sequence"); err != nil {
		return err
	}
	if err := requireString(revision["created_at"], "revision.created_at"); err != nil {
		return err
	}
	if raw, ok := revision["release_notes"]; ok {
		if err := requireString(raw, "revision.release_notes"); err != nil {
			return err
		}
	}
	profile, err := objectShape(root["profile"], []string{"id", "name", "official"}, []string{"description", "icon"})
	if err != nil {
		return fmt.Errorf("profile: %w", err)
	}
	if err := requireString(profile["id"], "profile.id"); err != nil {
		return err
	}
	if err := requireString(profile["name"], "profile.name"); err != nil {
		return err
	}
	if err := requireBool(profile["official"], "profile.official"); err != nil {
		return err
	}
	if raw, ok := profile["description"]; ok {
		if err := requireString(raw, "profile.description"); err != nil {
			return err
		}
	}
	if raw, ok := profile["icon"]; ok {
		ref, err := objectShape(raw, []string{"sha256", "size"}, []string{"media_type"})
		if err != nil {
			return fmt.Errorf("profile.icon: %w", err)
		}
		if err := requireString(ref["sha256"], "profile.icon.sha256"); err != nil {
			return err
		}
		if err := requireInteger(ref["size"], "profile.icon.size"); err != nil {
			return err
		}
		if raw, ok := ref["media_type"]; ok {
			if err := requireString(raw, "profile.icon.media_type"); err != nil {
				return err
			}
		}
	}
	if err := requireInteger(root["schema_version"], "schema_version"); err != nil {
		return err
	}
	var schemaVersion int
	if err := json.Unmarshal(root["schema_version"], &schemaVersion); err != nil {
		return err
	}
	game, err := objectShape(root["game"], gameShapeKeys(schemaVersion), nil)
	if err != nil {
		return fmt.Errorf("game: %w", err)
	}
	if err := requireString(game["minecraft"], "game.minecraft"); err != nil {
		return err
	}
	if schemaVersion >= 4 {
		loader, err := objectShape(game["loader"], []string{"kind", "version"}, nil)
		if err != nil {
			return fmt.Errorf("game.loader: %w", err)
		}
		for _, key := range []string{"kind", "version"} {
			if err := requireString(loader[key], "game.loader."+key); err != nil {
				return err
			}
		}
	} else if err := requireString(game["neoforge"], "game.neoforge"); err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(root["base"]), []byte("null")) {
		base, err := objectShape(root["base"], []string{"profile_id", "revision_id", "manifest_sha256"}, nil)
		if err != nil {
			return fmt.Errorf("base: %w", err)
		}
		for _, key := range []string{"profile_id", "revision_id", "manifest_sha256"} {
			if err := requireString(base[key], "base."+key); err != nil {
				return err
			}
		}
	}
	permissions, err := objectShape(root["permissions"], []string{"derive_local", "mods", "configs", "max_inheritance_depth"}, nil)
	if err != nil {
		return fmt.Errorf("permissions: %w", err)
	}
	if err := requireBool(permissions["derive_local"], "permissions.derive_local"); err != nil {
		return err
	}
	if err := requireInteger(permissions["max_inheritance_depth"], "permissions.max_inheritance_depth"); err != nil {
		return err
	}
	modsPerm, err := objectShape(permissions["mods"], []string{"add", "remove"}, nil)
	if err != nil {
		return fmt.Errorf("permissions.mods: %w", err)
	}
	if err := requireBool(modsPerm["add"], "permissions.mods.add"); err != nil {
		return err
	}
	if err := requireBool(modsPerm["remove"], "permissions.mods.remove"); err != nil {
		return err
	}
	configsPerm, err := objectShape(permissions["configs"], []string{"override_enforced", "override_default_once"}, nil)
	if err != nil {
		return fmt.Errorf("permissions.configs: %w", err)
	}
	if err := requireBool(configsPerm["override_enforced"], "permissions.configs.override_enforced"); err != nil {
		return err
	}
	if err := requireBool(configsPerm["override_default_once"], "permissions.configs.override_default_once"); err != nil {
		return err
	}
	if schemaVersion >= 2 {
		if _, ok := root["config_settings"]; !ok {
			return fmt.Errorf("schema_version %d requires config_settings", schemaVersion)
		}
	}
	if schemaVersion >= 3 {
		for _, key := range []string{"jvm_options", "remove_jvm_options"} {
			if _, ok := root[key]; !ok {
				return fmt.Errorf("schema_version %d requires %s", schemaVersion, key)
			}
		}
	}
	for _, key := range []string{"config_settings", "jvm_options", "remove_jvm_options"} {
		if raw, ok := root[key]; ok {
			if err := requireArray(raw, key); err != nil {
				return err
			}
		}
	}
	if raw, ok := root["jvm_options"]; ok {
		if err := validateJVMOptionsShape(raw); err != nil {
			return fmt.Errorf("jvm_options: %w", err)
		}
	}
	if raw, ok := root["remove_jvm_options"]; ok {
		var ids []json.RawMessage
		if err := decodeRaw(raw, &ids); err != nil {
			return err
		}
		for index, id := range ids {
			if err := requireString(id, fmt.Sprintf("remove_jvm_options[%d]", index)); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"mods", "remove_mods", "configs", "remove_configs", "objects"} {
		if err := requireArray(root[key], key); err != nil {
			return err
		}
	}
	if err := arrayObjectShapes(root["mods"], []string{"id", "path", "object"}, []string{"expect_base_object_sha256"}, objectRefField); err != nil {
		return fmt.Errorf("mods: %w", err)
	}
	if err := arrayObjectShapes(root["remove_mods"], []string{"id", "expect_base_object_sha256"}, nil, ""); err != nil {
		return fmt.Errorf("remove_mods: %w", err)
	}
	if err := arrayObjectShapes(root["configs"], []string{"path", "object", "policy"}, []string{"expect_base_object_sha256"}, objectRefField); err != nil {
		return fmt.Errorf("configs: %w", err)
	}
	if err := arrayObjectShapes(root["remove_configs"], []string{"path", "expect_base_object_sha256"}, nil, ""); err != nil {
		return fmt.Errorf("remove_configs: %w", err)
	}
	if raw, ok := root["config_settings"]; ok {
		if err := arrayObjectShapes(raw, []string{"path", "format", "key", "value", "policy"}, nil, ""); err != nil {
			return fmt.Errorf("config_settings: %w", err)
		}
		var entries []map[string]json.RawMessage
		if err := decodeRaw(raw, &entries); err != nil {
			return err
		}
		for index, entry := range entries {
			for _, key := range []string{"path", "format", "key", "policy"} {
				if err := requireString(entry[key], fmt.Sprintf("config_settings[%d].%s", index, key)); err != nil {
					return err
				}
			}
		}
	}
	if err := arrayObjectShapes(root["objects"], []string{"id", "path", "object"}, []string{"expect_base_object_sha256"}, objectRefField); err != nil {
		return fmt.Errorf("objects: %w", err)
	}
	return nil
}

const objectRefField = "object"

// validateJVMOptionsShape enforces required keys and rejects null anywhere inside
// jvm_options, so an omitted or null field can never decode to a silent Go zero value.
func validateJVMOptionsShape(raw []byte) error {
	if bytes.Contains(raw, []byte("null")) {
		var probe any
		if err := decodeRaw(raw, &probe); err != nil {
			return err
		}
		if containsJSONNull(probe) {
			return errors.New("null values are not allowed")
		}
	}
	var options []json.RawMessage
	if err := decodeRaw(raw, &options); err != nil {
		return err
	}
	for i, item := range options {
		option, err := objectShape(item,
			[]string{"id", "label", "template", "type", "help", "java", "platforms"},
			[]string{"min", "max", "values", "vm"})
		if err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
		if _, err := objectShape(option["java"], []string{"min"}, []string{"max"}); err != nil {
			return fmt.Errorf("item %d.java: %w", i, err)
		}
		var platforms map[string]json.RawMessage
		if err := decodeRaw(option["platforms"], &platforms); err != nil {
			return fmt.Errorf("item %d.platforms: %w", i, err)
		}
		for name, rawPlatform := range platforms {
			platform, err := objectShape(rawPlatform, []string{"default_enabled"}, []string{"default_value", "auto"})
			if err != nil {
				return fmt.Errorf("item %d.platforms.%s: %w", i, name, err)
			}
			if err := requireBool(platform["default_enabled"], "default_enabled"); err != nil {
				return fmt.Errorf("item %d.platforms.%s: %w", i, name, err)
			}
			if rawAuto, ok := platform["auto"]; ok {
				if err := validateJVMAutoShape(rawAuto); err != nil {
					return fmt.Errorf("item %d.platforms.%s.auto: %w", i, name, err)
				}
			}
		}
	}
	return nil
}

func validateJVMAutoShape(raw []byte) error {
	auto, err := objectShape(raw, []string{"rules", "otherwise"}, nil)
	if err != nil {
		return err
	}
	var rules []json.RawMessage
	if err := decodeRaw(auto["rules"], &rules); err != nil {
		return err
	}
	for i, rawRule := range rules {
		rule, err := objectShape(rawRule, []string{"when", "enabled"}, []string{"value"})
		if err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
		if err := requireBool(rule["enabled"], "enabled"); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
		var facts map[string]json.RawMessage
		if err := decodeRaw(rule["when"], &facts); err != nil {
			return fmt.Errorf("rules[%d].when: %w", i, err)
		}
		for fact, rawRange := range facts {
			if _, err := objectShape(rawRange, nil, []string{"min", "max"}); err != nil {
				return fmt.Errorf("rules[%d].when.%s: %w", i, fact, err)
			}
		}
	}
	otherwise, err := objectShape(auto["otherwise"], []string{"enabled"}, []string{"value"})
	if err != nil {
		return fmt.Errorf("otherwise: %w", err)
	}
	return requireBool(otherwise["enabled"], "otherwise.enabled")
}

func containsJSONNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, item := range typed {
			if containsJSONNull(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsJSONNull(item) {
				return true
			}
		}
	}
	return false
}

func arrayObjectShapes(raw []byte, required, optional []string, nestedObjectRef string) error {
	var items []json.RawMessage
	if err := decodeRaw(raw, &items); err != nil {
		return err
	}
	for i, item := range items {
		fields, err := objectShape(item, required, optional)
		if err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
		for key, raw := range fields {
			if key == nestedObjectRef {
				continue
			}
			switch key {
			case "id", "path", "policy":
				if err := requireString(raw, fmt.Sprintf("item %d.%s", i, key)); err != nil {
					return err
				}
			case "expect_base_object_sha256":
				if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					if err := requireString(raw, fmt.Sprintf("item %d.%s", i, key)); err != nil {
						return err
					}
				}
			}
		}
		if nestedObjectRef != "" {
			ref, err := objectShape(fields[nestedObjectRef], []string{"sha256", "size"}, []string{"media_type"})
			if err != nil {
				return fmt.Errorf("item %d.%s: %w", i, nestedObjectRef, err)
			}
			if err := requireString(ref["sha256"], fmt.Sprintf("item %d.object.sha256", i)); err != nil {
				return err
			}
			if err := requireInteger(ref["size"], fmt.Sprintf("item %d.object.size", i)); err != nil {
				return err
			}
			if raw, ok := ref["media_type"]; ok {
				if err := requireString(raw, fmt.Sprintf("item %d.object.media_type", i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func objectShape(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := decodeRaw(raw, &object); err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(required)+len(optional))
	for _, key := range required {
		allowed[key] = struct{}{}
		if _, ok := object[key]; !ok {
			return nil, fmt.Errorf("required property %q is missing", key)
		}
	}
	for _, key := range optional {
		allowed[key] = struct{}{}
	}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("additional property %q is not allowed", key)
		}
	}
	return object, nil
}

func decodeRaw(raw []byte, dst interface{}) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("empty JSON value")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra interface{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func requireString(raw []byte, name string) error {
	var v string
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &v) != nil {
		return fmt.Errorf("%s must be a string", name)
	}
	return nil
}

func requireBool(raw []byte, name string) error {
	var v bool
	trimmed := bytes.TrimSpace(raw)
	if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
		return fmt.Errorf("%s must be a boolean", name)
	}
	return json.Unmarshal(raw, &v)
}

func requireInteger(raw []byte, name string) error {
	var v int64
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &v) != nil {
		return fmt.Errorf("%s must be an integer", name)
	}
	return nil
}

func requireArray(raw []byte, name string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return fmt.Errorf("%s must be an array", name)
	}
	return nil
}

// gameShapeKeys lists the required game keys: schema 4 replaces the NeoForge-only
// field with a loader object so older manifests keep their exact signed shape.
func gameShapeKeys(schemaVersion int) []string {
	if schemaVersion >= 4 {
		return []string{"minecraft", "loader"}
	}
	return []string{"minecraft", "neoforge"}
}
