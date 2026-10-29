package recipe_service

import (
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/models"
)

func TestMissingRecipeLocales(t *testing.T) {
	state := models.RecipeTranslationState{
		SourceLocale: "en",
		SourceHash:   "v2",
		Translations: map[string]string{"fr": "v2", "fi": "v1"},
	}
	got := missingRecipeLocales(state, []string{"en", "fr", "fi", "zh"})
	if want := []string{"fi", "zh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missingRecipeLocales = %v, want %v", got, want)
	}
	state.Translations = map[string]string{"fr": "v2", "fi": "v2", "zh": "v2"}
	if got := missingRecipeLocales(state, []string{"en", "fr", "fi", "zh"}); got != nil {
		t.Fatalf("missingRecipeLocales on a fully translated recipe = %v, want none", got)
	}
}

func TestToolboxNeedsTranslation(t *testing.T) {
	targets := []string{"en", "fr", "zh"}
	full := map[string]models.ToolboxUnitTranslation{"fr": {}, "zh": {}}
	if !toolboxNeedsTranslation("", full, targets) {
		t.Error("an entry with no source locale must be translated")
	}
	if toolboxNeedsTranslation("en-US", full, targets) {
		t.Error("an entry with every target but its own language needs nothing")
	}
	if !toolboxNeedsTranslation("en", map[string]models.ToolboxUnitTranslation{"fr": {}}, targets) {
		t.Error("an entry missing zh must be translated")
	}
}

func TestBackfillRecipeTranslations(t *testing.T) {
	restore := translateBackoffs
	translateBackoffs = []time.Duration{0}
	defer func() { translateBackoffs = restore }()

	stale, fresh := primitive.NewObjectID(), primitive.NewObjectID()
	var mu sync.Mutex
	var translated []string
	s := &Service{
		db: &fakeStore{
			getRecipeByIdFn: func(id string) (models.Recipe, error) {
				oid, _ := primitive.ObjectIDFromHex(id)
				return models.Recipe{Id: &oid, SourceLocale: "en", SourceHash: "v1"}, nil
			},
			addLocaleRecipeFn: func(recipe models.Recipe, locale string) (models.Recipe, error) {
				mu.Lock()
				defer mu.Unlock()
				translated = append(translated, recipe.Id.Hex()+"/"+locale)
				return recipe, nil
			},
		},
		translator:     fakeTranslator{translateFn: func(r models.Recipe, _ string) (models.Recipe, error) { return r, nil }},
		targetLocales:  []string{"en", "fr", "zh"},
		translateSlots: make(chan struct{}, translateConcurrency),
	}

	s.backfillRecipeTranslations([]models.RecipeTranslationState{
		{ID: stale, SourceLocale: "en", SourceHash: "v1", Translations: map[string]string{"fr": "v1"}},
		{ID: fresh, SourceLocale: "en", SourceHash: "v1", Translations: map[string]string{"fr": "v1", "zh": "v1"}},
	})

	sort.Strings(translated)
	if want := []string{stale.Hex() + "/zh"}; !reflect.DeepEqual(translated, want) {
		t.Fatalf("translated = %v, want %v", translated, want)
	}
}
