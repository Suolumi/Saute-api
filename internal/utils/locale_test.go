package utils

import "testing"

func TestParseLocale(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"en":      "en",
		"en-US":   "en-US",
		"fr":      "fr",
		"zh":      "zh",
		"zh-CN":   "zh",
		"zh-cn":   "zh",
		"zh-Hans": "zh",
		"zh-SG":   "zh",
		"zh-TW":   "zh-TW",
		"zh-Hant": "zh-Hant",
	}
	for in, want := range cases {
		got, err := ParseLocale(in)
		if err != nil || got != want {
			t.Errorf("ParseLocale(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseLocale("not a locale!"); err == nil {
		t.Error("ParseLocale accepted an invalid tag")
	}
}
