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
		nil)
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
	profile, err := objectShape(root["profile"], []string{"id", "name", "official"}, nil)
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
	game, err := objectShape(root["game"], []string{"minecraft", "neoforge"}, nil)
	if err != nil {
		return fmt.Errorf("game: %w", err)
	}
	if err := requireString(game["minecraft"], "game.minecraft"); err != nil {
		return err
	}
	if err := requireString(game["neoforge"], "game.neoforge"); err != nil {
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
	if err := requireInteger(root["schema_version"], "schema_version"); err != nil {
		return err
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
	if err := arrayObjectShapes(root["objects"], []string{"id", "path", "object"}, []string{"expect_base_object_sha256"}, objectRefField); err != nil {
		return fmt.Errorf("objects: %w", err)
	}
	return nil
}

const objectRefField = "object"

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
