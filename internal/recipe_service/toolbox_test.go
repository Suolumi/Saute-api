package recipe_service

import (
	"context"
	"reflect"
	"testing"

	"recipes/internal/models"
)

func TestTranslateTextsCasedLowersBeforeTranslatingAndRestoresCapitalization(t *testing.T) {
	var gotTexts []string
	s := &Service{translator: fakeTranslator{
		translateTextsFn: func(texts []string, to string) ([]string, error) {
			gotTexts = texts
			// Simulates the real quirk this works around: the translator
			// would mistranslate a capitalized "Centiliter"/"Honey" if it
			// ever saw them, so the test fails loudly unless the input
			// actually arrived lowercased.
			out := make([]string, len(texts))
			for i, text := range texts {
				switch text {
				case "centiliter":
					out[i] = "centilitre"
				case "honey":
					out[i] = "miel"
				case "dairy":
					out[i] = "laitier"
				default:
					out[i] = text + "-" + to
				}
			}
			return out, nil
		},
	}}

	got, err := s.translateTextsCased([]string{"Centiliter", "Honey", "dairy"}, "fr")
	if err != nil {
		t.Fatalf("translateTextsCased: %v", err)
	}
	wantSent := []string{"centiliter", "honey", "dairy"}
	if !reflect.DeepEqual(gotTexts, wantSent) {
		t.Fatalf("sent %+v to the translator, want %+v (lowercased)", gotTexts, wantSent)
	}
	want := []string{"Centilitre", "Miel", "laitier"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveToolboxIngredientLocaleServesTranslation(t *testing.T) {
	item := models.ToolboxIngredient{
		Name: "Milk", Note: "source note", SourceLocale: "en",
		Translations: map[string]models.ToolboxIngredientTranslation{
			"fr": {Name: "Lait", Note: "note source"},
		},
	}

	got := resolveToolboxIngredientLocale(item, "fr")
	if got.Name != "Lait" || got.Note != "note source" {
		t.Fatalf("got name=%q note=%q, want Lait/note source", got.Name, got.Note)
	}
}

func TestResolveToolboxIngredientLocaleFallsBackWithoutTranslation(t *testing.T) {
	item := models.ToolboxIngredient{Name: "Milk", SourceLocale: "en"}

	got := resolveToolboxIngredientLocale(item, "fi")
	if got.Name != "Milk" {
		t.Fatalf("got name=%q, want Milk (no fi translation stored)", got.Name)
	}
}

func TestResolveToolboxIngredientLocaleSkipsSameBaseLocale(t *testing.T) {
	item := models.ToolboxIngredient{
		Name: "Milk", SourceLocale: "en",
		Translations: map[string]models.ToolboxIngredientTranslation{"en-GB": {Name: "should never be served"}},
	}

	got := resolveToolboxIngredientLocale(item, "en-US")
	if got.Name != "Milk" {
		t.Fatalf("got name=%q, want Milk (en-US shares en's base language)", got.Name)
	}
}

func TestResolveToolboxUnitLocaleServesTranslation(t *testing.T) {
	item := models.ToolboxUnit{
		Name: "Gram", Symbol: "g", SourceLocale: "en",
		Translations: map[string]models.ToolboxUnitTranslation{"fr": {Name: "Gramme"}},
	}

	got := resolveToolboxUnitLocale(item, "fr")
	if got.Name != "Gramme" || got.Symbol != "g" {
		t.Fatalf("got name=%q symbol=%q, want Gramme/g (symbol is never translated)", got.Name, got.Symbol)
	}
}

func TestResolveToolboxSubstitutionLocaleServesTranslation(t *testing.T) {
	item := models.ToolboxSubstitution{
		Problem: "No buttermilk", Solution: "Milk + lemon juice", Tag: "dairy", SourceLocale: "en",
		Translations: map[string]models.ToolboxSubstitutionTranslation{
			"fr": {Problem: "Pas de babeurre", Solution: "Lait + jus de citron", Tag: "laitier"},
		},
	}

	got := resolveToolboxSubstitutionLocale(item, "fr")
	if got.Problem != "Pas de babeurre" || got.Solution != "Lait + jus de citron" || got.Tag != "laitier" {
		t.Fatalf("got = %+v, want the fr translation", got)
	}
}

func TestListToolboxIngredientsResolvesRequestedLocale(t *testing.T) {
	store := &fakeStore{
		listToolboxIngredientsFn: func() ([]models.ToolboxIngredient, error) {
			return []models.ToolboxIngredient{{
				Name: "Milk", SourceLocale: "en",
				Translations: map[string]models.ToolboxIngredientTranslation{"fr": {Name: "Lait"}},
			}}, nil
		},
	}
	s := &Service{db: store}

	items, err := s.ListToolboxIngredients(context.Background(), "fr")
	if err != nil {
		t.Fatalf("ListToolboxIngredients: %v", err)
	}
	if len(items) != 1 || items[0].Name != "Lait" {
		t.Fatalf("items = %+v, want one entry named Lait", items)
	}
}

func TestListToolboxIngredientsDefaultsToSourceTextWithNoLocale(t *testing.T) {
	store := &fakeStore{
		listToolboxIngredientsFn: func() ([]models.ToolboxIngredient, error) {
			return []models.ToolboxIngredient{{
				Name: "Milk", SourceLocale: "en",
				Translations: map[string]models.ToolboxIngredientTranslation{"fr": {Name: "Lait"}},
			}}, nil
		},
	}
	s := &Service{db: store}

	items, err := s.ListToolboxIngredients(context.Background(), "")
	if err != nil {
		t.Fatalf("ListToolboxIngredients: %v", err)
	}
	if len(items) != 1 || items[0].Name != "Milk" {
		t.Fatalf("items = %+v, want the source-language entry", items)
	}
}

func TestTranslateToolboxEntryDetectsSourceAndTranslatesIntoEveryOtherTarget(t *testing.T) {
	id := ptrObjectID()
	var gotID, gotSourceLocale string
	var gotTranslations map[string]models.ToolboxIngredientTranslation
	store := &fakeStore{
		getToolboxIngredientByIdFn: func(string) (models.ToolboxIngredient, error) {
			return models.ToolboxIngredient{Id: id, Name: "Milk", Note: "creamy"}, nil
		},
		setToolboxIngredientTranslationsFn: func(entryID, sourceLocale string, translations map[string]models.ToolboxIngredientTranslation) error {
			gotID, gotSourceLocale, gotTranslations = entryID, sourceLocale, translations
			return nil
		},
	}
	s := &Service{
		db:             store,
		translator:     fakeTranslator{},
		targetLocales:  []string{"en", "fr", "fi"},
		translateSlots: make(chan struct{}, 1),
	}

	if err := s.translateToolboxEntry(models.ToolboxSuggestionIngredient, *id); err != nil {
		t.Fatalf("translateToolboxEntry: %v", err)
	}
	if gotID != id.Hex() || gotSourceLocale != "en" {
		t.Fatalf("got id=%q sourceLocale=%q, want %q/en", gotID, gotSourceLocale, id.Hex())
	}
	want := map[string]models.ToolboxIngredientTranslation{
		"fr": {Name: "Milk-fr", Note: "creamy-fr"},
		"fi": {Name: "Milk-fi", Note: "creamy-fi"},
	}
	if !reflect.DeepEqual(gotTranslations, want) {
		t.Fatalf("translations = %+v, want %+v (en dropped as the detected source locale)", gotTranslations, want)
	}
}

func TestTranslateToolboxEntryTranslatesSubstitutions(t *testing.T) {
	id := ptrObjectID()
	var gotTranslations map[string]models.ToolboxSubstitutionTranslation
	store := &fakeStore{
		getToolboxSubstitutionByIdFn: func(string) (models.ToolboxSubstitution, error) {
			return models.ToolboxSubstitution{Id: id, Problem: "No buttermilk", Solution: "Milk + lemon juice", Tag: "dairy"}, nil
		},
		setToolboxSubstitutionTranslationsFn: func(_, _ string, translations map[string]models.ToolboxSubstitutionTranslation) error {
			gotTranslations = translations
			return nil
		},
	}
	s := &Service{
		db:             store,
		translator:     fakeTranslator{},
		targetLocales:  []string{"en", "fr"},
		translateSlots: make(chan struct{}, 1),
	}

	if err := s.translateToolboxEntry(models.ToolboxSuggestionSubstitution, *id); err != nil {
		t.Fatalf("translateToolboxEntry: %v", err)
	}
	want := map[string]models.ToolboxSubstitutionTranslation{
		"fr": {Problem: "No buttermilk-fr", Solution: "Milk + lemon juice-fr", Tag: "dairy-fr"},
	}
	if !reflect.DeepEqual(gotTranslations, want) {
		t.Fatalf("translations = %+v, want %+v", gotTranslations, want)
	}
}
