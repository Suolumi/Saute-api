package recipe_service

import (
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

func TestTranslateToolboxEntryDetectsSourceAndTranslatesIntoEveryOtherTarget(t *testing.T) {
	id := ptrObjectID()
	var gotID, gotSourceLocale string
	var gotTranslations map[string]models.ToolboxUnitTranslation
	store := &fakeStore{
		getToolboxUnitByIdFn: func(string) (models.ToolboxUnit, error) {
			return models.ToolboxUnit{Id: id, Name: "Cup"}, nil
		},
		setToolboxUnitTranslationsFn: func(entryID, sourceLocale string, translations map[string]models.ToolboxUnitTranslation) error {
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

	if err := s.translateToolboxEntry(toolboxKindUnit, *id); err != nil {
		t.Fatalf("translateToolboxEntry: %v", err)
	}
	if gotID != id.Hex() || gotSourceLocale != "en" {
		t.Fatalf("got id=%q sourceLocale=%q, want %q/en", gotID, gotSourceLocale, id.Hex())
	}
	want := map[string]models.ToolboxUnitTranslation{
		"fr": {Name: "Cup-fr"},
		"fi": {Name: "Cup-fi"},
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

	if err := s.translateToolboxEntry(toolboxKindSubstitution, *id); err != nil {
		t.Fatalf("translateToolboxEntry: %v", err)
	}
	want := map[string]models.ToolboxSubstitutionTranslation{
		"fr": {Problem: "No buttermilk-fr", Solution: "Milk + lemon juice-fr", Tag: "dairy-fr"},
	}
	if !reflect.DeepEqual(gotTranslations, want) {
		t.Fatalf("translations = %+v, want %+v", gotTranslations, want)
	}
}
