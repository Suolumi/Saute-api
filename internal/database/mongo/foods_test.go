package mongo

import (
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
