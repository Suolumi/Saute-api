package recipe_service

import (
	"testing"

	"recipes/internal/models"
)

func testFoods() []models.FoodItem {
	return []models.FoodItem{
		{Id: "egg", Names: map[string]string{"en": "Egg", "fr": "Œuf"}, Aliases: map[string][]string{"en": {"eggs"}, "fr": {"oeuf", "oeufs"}}},
		{Id: "bread", Names: map[string]string{"en": "Bread", "fr": "Pain"}},
	}
}

func TestFoodIndexMatchesNameAndAliasInLocale(t *testing.T) {
	cases := []struct{ locale, name, want string }{
		{"fr", "oeufs", "egg"},
		{"fr", " Œuf ", "egg"},
		{"en", "Eggs", "egg"},
		{"en-GB", "bread", "bread"},
	}
	for _, tc := range cases {
		food, ok := newFoodIndex(testFoods(), tc.locale).match(tc.name)
		if !ok || food.Id != tc.want {
			t.Errorf("match(%q) in %s = %q/%v, want %q", tc.name, tc.locale, food.Id, ok, tc.want)
		}
	}
}

func TestFoodIndexIgnoresOtherLocales(t *testing.T) {
	if food, ok := newFoodIndex(testFoods(), "fr").match("eggs"); ok {
		t.Fatalf("match(eggs) in fr = %q, want none", food.Id)
	}
}

func TestFoodIndexUnsupportedLocaleSearchesAll(t *testing.T) {
	for _, locale := range []string{"", "de"} {
		if food, ok := newFoodIndex(testFoods(), locale).match("pain"); !ok || food.Id != "bread" {
			t.Errorf("match(pain) in %q = %q/%v, want bread", locale, food.Id, ok)
		}
	}
}

func TestFoodIndexFindsById(t *testing.T) {
	if food, ok := newFoodIndex(testFoods(), "en").food("bread"); !ok || food.Names["fr"] != "Pain" {
		t.Fatalf("food(bread) = %+v/%v", food, ok)
	}
}
