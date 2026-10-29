package config

import (
	"reflect"
	"testing"
)

func TestParseLocales(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{"default", "en,fr,fi,zh", []string{"en", "fr", "fi", "zh"}},
		{"spaces and blanks", " en , , fr ,", []string{"en", "fr"}},
		{"duplicates collapse", "en,en,fr", []string{"en", "fr"}},
		{"normalized", "EN-us,FR", []string{"en-US", "fr"}},
		{"simplified chinese collapses", "zh-CN,zh-Hans,zh", []string{"zh"}},
		{"empty disables", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLocales(tc.raw)
			if err != nil {
				t.Fatalf("parseLocales(%q): %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseLocales(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseLocalesInvalid(t *testing.T) {
	for _, raw := range []string{"en,not a locale", "en,zzzz", "@@"} {
		if _, err := parseLocales(raw); err == nil {
			t.Fatalf("parseLocales(%q) = nil error, want failure", raw)
		}
	}
}
