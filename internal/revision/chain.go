package revision

import (
	"errors"
	"fmt"
)

var (
	ErrParentMissing      = errors.New("pinned parent revision is missing")
	ErrParentPinMismatch  = errors.New("pinned parent does not match verified revision")
	ErrInheritanceCycle   = errors.New("inheritance cycle")
	ErrInheritanceTooDeep = errors.New("inheritance depth exceeds cap")
	ErrGameMismatch       = errors.New("parent game versions do not match child")
)

type RevisionKey struct {
	ProfileID  string
	RevisionID string
}

// ValidateInheritanceChain follows only immutable profile/revision/digest pins.
// parents is an in-memory set of already verified revisions supplied by the caller.
// The returned chain is target first, root last.
func ValidateInheritanceChain(target *VerifiedRevision, parents map[RevisionKey]*VerifiedRevision, implementationMax int) ([]*VerifiedRevision, error) {
	if target == nil {
		return nil, errors.New("target revision is nil")
	}
	if implementationMax <= 0 || implementationMax > MaxInheritanceBases {
		implementationMax = MaxInheritanceBases
	}
	cap := implementationMax
	if signed := target.manifest.Permissions.MaxInheritanceDepth; signed < cap {
		cap = signed
	}

	chain := []*VerifiedRevision{target}
	seen := map[RevisionKey]struct{}{{ProfileID: target.ProfileID(), RevisionID: target.RevisionID()}: {}}
	current := target
	bases := 0
	for current.manifest.Base != nil {
		bases++
		if bases > cap {
			return nil, ErrInheritanceTooDeep
		}
		pin := *current.manifest.Base
		key := RevisionKey{ProfileID: pin.ProfileID, RevisionID: pin.RevisionID}
		if _, exists := seen[key]; exists {
			return nil, ErrInheritanceCycle
		}
		parent, exists := parents[key]
		if !exists || parent == nil {
			return nil, ErrParentMissing
		}
		if parent.ProfileID() != pin.ProfileID || parent.RevisionID() != pin.RevisionID || parent.ManifestSHA256() != pin.ManifestSHA256 {
			return nil, ErrParentPinMismatch
		}
		if parent.manifest.Game != current.manifest.Game {
			return nil, fmt.Errorf("%w: %s/%s", ErrGameMismatch, pin.ProfileID, pin.RevisionID)
		}
		seen[key] = struct{}{}
		chain = append(chain, parent)
		current = parent
	}
	if err := validateConfigSettingChain(chain); err != nil {
		return nil, err
	}
	return chain, nil
}

func validateConfigSettingChain(chain []*VerifiedRevision) error {
	effectiveConfigs := make(map[string]struct{})
	effectiveSettings := make(map[string]ConfigSetting)
	var parentPermissions *ConfigPermissions

	for index := len(chain) - 1; index >= 0; index-- {
		manifest := chain[index].manifest
		for _, removal := range manifest.RemoveConfigs {
			delete(effectiveConfigs, removal.Path)
			for identity, setting := range effectiveSettings {
				if setting.Path == removal.Path {
					delete(effectiveSettings, identity)
				}
			}
		}
		for _, config := range manifest.Configs {
			effectiveConfigs[config.Path] = struct{}{}
		}
		for _, setting := range manifest.ConfigSettings {
			if _, exists := effectiveConfigs[setting.Path]; !exists {
				return fmt.Errorf("config setting %s/%s refers to a config absent from the effective profile", setting.Path, setting.Key)
			}
			identity := setting.Identity()
			if previous, exists := effectiveSettings[identity]; exists {
				if parentPermissions == nil {
					return errors.New("config setting lineage has no parent permissions")
				}
				allowed := parentPermissions.OverrideDefaultOnce
				if previous.Policy == "enforced" {
					allowed = parentPermissions.OverrideEnforced
				}
				if !allowed {
					return fmt.Errorf("parent profile does not permit overriding config setting %s/%s", setting.Path, setting.Key)
				}
			}
			effectiveSettings[identity] = setting
		}
		permissions := manifest.Permissions.Configs
		parentPermissions = &permissions
	}
	return nil
}
