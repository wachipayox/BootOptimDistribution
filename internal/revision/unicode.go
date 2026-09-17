package revision

import (
	"errors"
	"strings"
	"unicode/utf8"
)

func validateJSONStringUnicode(raw []byte) error {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		for i++; i < len(raw); i++ {
			c := raw[i]
			switch {
			case c == '"':
				goto nextString
			case c < 0x20:
				return errors.New("unescaped control character in string")
			case c == '\\':
				if i+1 >= len(raw) {
					return errors.New("truncated escape")
				}
				i++
				if raw[i] != 'u' {
					if !strings.ContainsRune(`"\\/bfnrt`, rune(raw[i]) {
						return errors.New("invalid string escape")
				}
				continue
				}
				unit, next, err := parseUTF16Escape(raw, i)
				if err != nil {
					return err
				}
				i = next
				if unit >= 0xD800 && unit <= 0xDBFF {
					if i+2 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
						return errors.New("high surrogate is not followed by low surrogate")
					}
					low, lowNext, err := parseUTF16Escape(raw, i+2)
					if err != nil || low < 0xDC00 || low > 0xDFFF {
						return errors.New("invalid low surrogate")
					}
					i = lowNext
				} else if unit >= 0xDC00 && unit <= 0xDFFF {
					return errors.New("unpaired low surrogate")
				}
			case c >= utf8.RuneSelf:
				_, size := utf8.DecodeRune(raw[i:])
				if size == 1 {
					return errors.New("invalid UTF-8 sequence in string")
				}
				i += size - 1
			}
		}
		return errors.New("unterminated JSON string")
	nextString:
	}
	return nil
}

func parseUTF16Escape(raw []byte, uIndex int) (uint16, int, error) {
	if uIndex+4 >= len(raw) || raw[uIndex] != 'u' {
		return 0, uIndex, errors.New("truncated unicode escape")
	}
	var value uint16
	for j := 1; j <= 4; j++ {
		c := raw[uIndex+j]
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value |= uint16(c - '0')
		case c >= 'a' && c <= 'f':
			value |= uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			value |= uint16(c-'A') + 10
		default:
			return 0, uIndex, errors.New("invalid unicode escape")
		}
	}
	return value, uIndex + 4, nil
}
