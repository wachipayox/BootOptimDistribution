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
	return chain, nil
}
