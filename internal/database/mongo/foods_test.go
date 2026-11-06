package mongo

import (
	"fmt"
	"strings"
	"testing"

	"recipes/internal/models"
)

func TestSubstituteProblemFood(t *testing.T) {
	foods := []models.FoodItem{
		{Id: "buttermilk", Names: map[string]string{"en": "Buttermilk", "fr": "Babeurre"}},
		{Id: "butter", Names: map[string]string{"en": "Butter", "fr": "Beurre"}, Aliases: map[string][]string{"en": {"unsalted butter"}}},
	}
	cases := map[string]string{
		"No buttermilk":        "buttermilk",
		"Pas de beurre":        "butter",
		" unsalted butter ":    "butter",
		"Something else":       "",
		"No buttermilk? Easy.": "",
	}
	for problem, want := range cases {
		if got := substituteProblemFood(problem, foods); got != want {
			t.Errorf("substituteProblemFood(%q) = %q, want %q", problem, got, want)
		}
	}
}

func TestMergeSubstitute(t *testing.T) {
	lines := []string{"Olive oil"}
	if got, ok := mergeSubstitute(lines, " olive OIL "); !ok || len(got) != 1 {
		t.Fatalf("case-insensitive repeat: %q/%v", got, ok)
	}
	if _, ok := mergeSubstitute(lines, strings.Repeat("x", 301)); ok {
		t.Fatal("a line over 300 characters must be refused")
	}
	full := make([]string, 10)
	for i := range full {
		full[i] = fmt.Sprint(i)
	}
	if _, ok := mergeSubstitute(full, "eleventh"); ok {
		t.Fatal("an 11th line must be refused")
	}
	if got, ok := mergeSubstitute(lines, " Margarine "); !ok || len(got) != 2 || got[1] != "Margarine" {
		t.Fatalf("append: %q/%v", got, ok)
	}
}
