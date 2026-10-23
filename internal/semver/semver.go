// Package semver implements the strict subset of Semantic Versioning 2.0.0
// that launcher releases use: parsing and precedence comparison.
package semver

import (
	"errors"
	"strings"
)

// MaxLength bounds a version string accepted by Parse.
const MaxLength = 64

var ErrInvalid = errors.New("invalid semantic version")

// Version is a parsed SemVer 2.0.0 version.
type Version struct {
	Major, Minor, Patch string // decimal, no leading zeros
	Pre                 []string
	Build               string
}

// Parse accepts MAJOR.MINOR.PATCH[-pre][+build] and rejects everything else:
// leading zeros in numeric fields, empty identifiers, a "v" prefix, spaces.
func Parse(s string) (Version, error) {
	var v Version
	if s == "" || len(s) > MaxLength {
		return v, ErrInvalid
	}
	rest := s
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		v.Build = rest[i+1:]
		rest = rest[:i]
		if !validIdentifiers(v.Build, false) {
			return Version{}, ErrInvalid
		}
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		pre := rest[i+1:]
		rest = rest[:i]
		if !validIdentifiers(pre, true) {
			return Version{}, ErrInvalid
		}
		v.Pre = strings.Split(pre, ".")
	}
	core := strings.Split(rest, ".")
	if len(core) != 3 {
		return Version{}, ErrInvalid
	}
	for _, part := range core {
		if !numeric(part) || (len(part) > 1 && part[0] == '0') {
			return Version{}, ErrInvalid
		}
	}
	v.Major, v.Minor, v.Patch = core[0], core[1], core[2]
	return v, nil
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func validIdentifiers(s string, noLeadingZero bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		for i := 0; i < len(id); i++ {
			c := id[i]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
				return false
			}
		}
		if noLeadingZero && numeric(id) && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

func compareNumeric(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// Compare returns -1, 0 or 1 following SemVer 2.0.0 precedence. Build
// metadata is ignored.
func Compare(a, b Version) int {
	if c := compareNumeric(a.Major, b.Major); c != 0 {
		return c
	}
	if c := compareNumeric(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := compareNumeric(a.Patch, b.Patch); c != 0 {
		return c
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		x, y := a.Pre[i], b.Pre[i]
		xn, yn := numeric(x), numeric(y)
		var c int
		switch {
		case xn && yn:
			c = compareNumeric(x, y)
		case xn:
			c = -1
		case yn:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	switch {
	case len(a.Pre) < len(b.Pre):
		return -1
	case len(a.Pre) > len(b.Pre):
		return 1
	}
	return 0
}

// CompareStrings parses both versions and compares them.
func CompareStrings(a, b string) (int, error) {
	va, err := Parse(a)
	if err != nil {
		return 0, err
	}
	vb, err := Parse(b)
	if err != nil {
		return 0, err
	}
	return Compare(va, vb), nil
}
