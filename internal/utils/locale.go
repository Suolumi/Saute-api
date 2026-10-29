package utils

import (
	"errors"
	"strings"

	"golang.org/x/text/language"
)

// ErrInvalidLocale is returned by ParseLocale for a value that isn't a BCP-47 tag.
var ErrInvalidLocale = errors.New("invalid locale")

// CanonicalLocale formats tag the way locales are stored and compared across
// the API. Simplified Chinese collapses to "zh" whatever its region or script
// subtag: the site serves it under /zh/, while Google detects it as "zh-CN".
func CanonicalLocale(tag language.Tag) string {
	if base, _ := tag.Base(); base.String() == "zh" {
		if script, _ := tag.Script(); script.String() == "Hans" {
			return "zh"
		}
	}
	return tag.String()
}

// ParseLocale parses value as a BCP-47 tag and returns its CanonicalLocale.
// A blank value yields "" and no error.
func ParseLocale(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	tag, err := language.Parse(value)
	if err != nil || tag == language.Und {
		return "", ErrInvalidLocale
	}
	return CanonicalLocale(tag), nil
}
