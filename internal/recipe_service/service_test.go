package recipe_service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

// fakeStore satisfies Store; only the methods a test needs are wired, the rest
// return zero values.
type fakeStore struct {
	getRecipeByIdLocaleFn         func(id, locale string) (models.Recipe, error)
	addLocaleRecipeFn             func(recipe models.Recipe, locale string) (models.Recipe, error)
	getRecipeByIdFn               func(id string) (models.Recipe, error)
	getRecipeByIdLocale           int
	getRecipeDocumentsFn          func(models.GetRecipesRequest) ([]models.Recipe, int64, error)
	getRecipeDocumentsBoostedFn   func(userID string, parameters models.GetRecipesRequest) ([]models.Recipe, []models.Recipe, int64, error)
	getRecipeDocumentsBoosted     int
	getFavoriteInfoFn             func(ids []string, userID string) (map[string]models.FavoriteInfo, error)
	getFamilyFavoriteInfoFn       func(rootIDs []string, userID string) (map[string]models.FavoriteInfo, error)
	deleteRecipeByIdFn            func(id string) (models.RecipeDB, error)
	deleteRecipeById              int
	createRecipeFn                func(authorID string, infos *models.CreateRecipe) (models.Recipe, error)
	replaceRecipeByIdFn           func(id string, recipe models.RecipeDB) (models.Recipe, error)
	repointRecipeReferencesFn     func(oldRootID, newRootID string) error
	hasIncomingReferencesFn       func(recipeID string) (bool, error)
	familyReferencesRecipeFn      func(familyID, targetID string) (bool, error)
	recipeReferencesFamilyFn      func(recipeID, familyID string) (bool, error)
	getRecipeTitlesFn             func(ids []string) (map[string]string, error)
	getUsersByIDsFn               func(ids []string) (map[string]models.UserView, error)
	listRecipeTranslationStatesFn func() ([]models.RecipeTranslationState, error)
	listTranslationOverridesFn    func(recipeID, locale string) ([]models.TranslationOverride, error)
	deleteTranslationOverrideFn   func(id string) (models.TranslationOverride, error)
	deleteOverridesByPrefixFn     func(recipeID, locale, prefix string) error
	// callOrder records, in order, the names of RepointRecipeReferences/
	// DeleteRecipeById calls.
	callOrder               []string
	repointRecipeReferences int

	// families maps recipe id -> family id for the family methods;
	// familyOrder lists those recipe ids oldest first (see addFamily).
	families        map[string]string
	familyOrder     []string
	familySizeCalls [][]string

	listToolboxUnitsFn                      func() ([]models.ToolboxUnit, error)
	getToolboxUnitByIdFn                    func(id string) (models.ToolboxUnit, error)
	setToolboxUnitTranslationsFn            func(id, sourceLocale string, translations map[string]models.ToolboxUnitTranslation) error
	getIngredientNutritionLinkByNameLowerFn func(nameLower string) (models.IngredientNutritionLink, error)
	getUnitAliasByAliasLowerFn              func(aliasLower string) (models.UnitAlias, error)
	listFoodsFn                             func() ([]models.FoodItem, error)
	getNutritionSuggestionByIdFn            func(id string) (models.NutritionSuggestion, error)
	createFoodFn                            func(food models.FoodItem) error
	updateFoodFn                            func(food models.FoodItem) (models.FoodItem, error)
	countLinksForFoodFn                     func(id string) (int64, error)
	countUnitAliasesForUnitFn               func(id primitive.ObjectID) (int64, error)
	createIngredientNutritionLinkFn         func(name, foodID string, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error)
	updateIngredientNutritionLinkFn         func(id, name, foodID string, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error)
	createUnitAliasFn                       func(alias string, unitID primitive.ObjectID) (models.UnitAlias, error)

	// feedback is the in-memory feedback collection (see feedback_test.go).
	feedback map[string]models.Feedback

	// tags, tagCounts and addedTags back the tag methods (see tags_test.go).
	tags      map[string]models.RecipeTag
	tagCounts map[string]int64
	addedTags map[string][]string
}

func (f *fakeStore) CreateRecipe(authorID string, infos *models.CreateRecipe) (models.Recipe, error) {
	if f.createRecipeFn != nil {
		return f.createRecipeFn(authorID, infos)
	}
	return models.Recipe{}, nil
}
func (f *fakeStore) ReplaceRecipeById(_ context.Context, id string, recipe models.RecipeDB) (models.Recipe, error) {
	if f.replaceRecipeByIdFn != nil {
		return f.replaceRecipeByIdFn(id, recipe)
	}
	return models.Recipe{}, nil
}
func (f *fakeStore) GetRecipeById(id string) (models.Recipe, error) {
	if f.getRecipeByIdFn != nil {
		return f.getRecipeByIdFn(id)
	}
	return models.Recipe{}, nil
}
func (f *fakeStore) GetRecipeByIdForAuthor(context.Context, string, string) (models.Recipe, error) {
	return models.Recipe{}, nil
}
func (f *fakeStore) GetRecipeByIdLocale(id string, locale string) (models.Recipe, error) {
	f.getRecipeByIdLocale++
	if f.getRecipeByIdLocaleFn != nil {
		return f.getRecipeByIdLocaleFn(id, locale)
	}
	return models.Recipe{}, errors.New("not found")
}
func (f *fakeStore) GetRecipeDocuments(parameters models.GetRecipesRequest) ([]models.Recipe, int64, error) {
	if f.getRecipeDocumentsFn != nil {
		return f.getRecipeDocumentsFn(parameters)
	}
	return nil, 0, nil
}
func (f *fakeStore) GetRecipeDocumentsBoosted(_ context.Context, userID string, parameters models.GetRecipesRequest) ([]models.Recipe, []models.Recipe, int64, error) {
	f.getRecipeDocumentsBoosted++
	if f.getRecipeDocumentsBoostedFn != nil {
		return f.getRecipeDocumentsBoostedFn(userID, parameters)
	}
	return nil, nil, 0, nil
}
func (f *fakeStore) GetRecipesByAuthor(context.Context, string, string, int) ([]models.Recipe, int64, error) {
	return nil, 0, nil
}
func (f *fakeStore) GetTranslationsByRecipeIDs(context.Context, []string, string) (map[string]models.Recipe, error) {
	return map[string]models.Recipe{}, nil
}
func (f *fakeStore) ListRecipeTranslationStates(context.Context) ([]models.RecipeTranslationState, error) {
	if f.listRecipeTranslationStatesFn != nil {
		return f.listRecipeTranslationStatesFn()
	}
	return nil, nil
}
func (f *fakeStore) AddLocaleRecipe(recipe models.Recipe, locale string) (models.Recipe, error) {
	if f.addLocaleRecipeFn != nil {
		return f.addLocaleRecipeFn(recipe, locale)
	}
	return recipe, nil
}
func (f *fakeStore) DeleteRecipeById(id string) (models.RecipeDB, error) {
	f.deleteRecipeById++
	f.callOrder = append(f.callOrder, "DeleteRecipeById")
	if f.deleteRecipeByIdFn != nil {
		return f.deleteRecipeByIdFn(id)
	}
	return models.RecipeDB{}, nil
}
func (f *fakeStore) DeleteLocalizedRecipesByID(context.Context, string) error { return nil }

// addFamily registers ids, oldest first, as members of familyID.
func (f *fakeStore) addFamily(familyID string, ids ...string) {
	if f.families == nil {
		f.families = map[string]string{}
	}
	for _, id := range ids {
		f.families[id] = familyID
		f.familyOrder = append(f.familyOrder, id)
	}
}
func (f *fakeStore) GetFamilySizes(_ context.Context, familyIDs []string) (map[string]int64, error) {
	f.familySizeCalls = append(f.familySizeCalls, familyIDs)
	sizes := map[string]int64{}
	for _, familyID := range familyIDs {
		for _, member := range f.familyOrder {
			if f.families[member] == familyID {
				sizes[familyID]++
			}
		}
	}
	return sizes, nil
}
func (f *fakeStore) GetNewestFamilyMemberID(_ context.Context, familyID, excludeID string) (*primitive.ObjectID, error) {
	for i := len(f.familyOrder) - 1; i >= 0; i-- {
		id := f.familyOrder[i]
		if id != excludeID && f.families[id] == familyID {
			objectID, err := primitive.ObjectIDFromHex(id)
			return &objectID, err
		}
	}
	return nil, nil
}
func (f *fakeStore) SetFamilyID(_ context.Context, recipeID, familyID string) error {
	f.addFamilyMember(recipeID)
	f.families[recipeID] = familyID
	return nil
}
func (f *fakeStore) RekeyFamily(_ context.Context, familyID, excludeID, newFamilyID string) error {
	for id, family := range f.families {
		if family == familyID && id != excludeID {
			f.families[id] = newFamilyID
		}
	}
	return nil
}
func (f *fakeStore) addFamilyMember(id string) {
	if f.families == nil {
		f.families = map[string]string{}
	}
	if _, ok := f.families[id]; !ok {
		f.familyOrder = append(f.familyOrder, id)
	}
}
func (f *fakeStore) RecipeReferencesFamily(_ context.Context, recipeID, familyID string) (bool, error) {
	if f.recipeReferencesFamilyFn != nil {
		return f.recipeReferencesFamilyFn(recipeID, familyID)
	}
	return false, nil
}
func (f *fakeStore) FamilyReferencesRecipe(_ context.Context, familyID, targetID string) (bool, error) {
	if f.familyReferencesRecipeFn != nil {
		return f.familyReferencesRecipeFn(familyID, targetID)
	}
	return false, nil
}
func (f *fakeStore) RepointRecipeReferences(_ context.Context, oldRootID, newRootID string) error {
	f.repointRecipeReferences++
	f.callOrder = append(f.callOrder, "RepointRecipeReferences")
	if f.repointRecipeReferencesFn != nil {
		return f.repointRecipeReferencesFn(oldRootID, newRootID)
	}
	return nil
}
func (f *fakeStore) HasIncomingReferences(_ context.Context, recipeID string) (bool, error) {
	if f.hasIncomingReferencesFn != nil {
		return f.hasIncomingReferencesFn(recipeID)
	}
	return false, nil
}
func (f *fakeStore) GetRecipeTitles(_ context.Context, ids []string) (map[string]string, error) {
	if f.getRecipeTitlesFn != nil {
		return f.getRecipeTitlesFn(ids)
	}
	return map[string]string{}, nil
}
func (f *fakeStore) GetUsersByIDs(_ context.Context, ids []string) (map[string]models.UserView, error) {
	if f.getUsersByIDsFn != nil {
		return f.getUsersByIDsFn(ids)
	}
	return map[string]models.UserView{}, nil
}

func (f *fakeStore) AddFavorite(context.Context, string, string) error       { return nil }
func (f *fakeStore) RemoveFavorite(context.Context, string, string) error    { return nil }
func (f *fakeStore) DeleteFavoritesByRecipeID(context.Context, string) error { return nil }
func (f *fakeStore) GetFavoriteInfo(_ context.Context, ids []string, userID string) (map[string]models.FavoriteInfo, error) {
	if f.getFavoriteInfoFn != nil {
		return f.getFavoriteInfoFn(ids, userID)
	}
	return map[string]models.FavoriteInfo{}, nil
}
func (f *fakeStore) GetFamilyFavoriteInfo(_ context.Context, rootIDs []string, userID string) (map[string]models.FavoriteInfo, error) {
	if f.getFamilyFavoriteInfoFn != nil {
		return f.getFamilyFavoriteInfoFn(rootIDs, userID)
	}
	return map[string]models.FavoriteInfo{}, nil
}

func (f *fakeStore) CreateTranslationSuggestion(context.Context, models.TranslationSuggestion) (models.TranslationSuggestion, error) {
	return models.TranslationSuggestion{}, nil
}
func (f *fakeStore) GetTranslationSuggestionById(context.Context, string) (models.TranslationSuggestion, error) {
	return models.TranslationSuggestion{}, nil
}
func (f *fakeStore) ListTranslationSuggestions(context.Context, string, int64, int64) ([]models.TranslationSuggestion, int64, error) {
	return nil, 0, nil
}
func (f *fakeStore) UpdateTranslationSuggestionStatus(context.Context, string, string, string) error {
	return nil
}
func (f *fakeStore) HasPendingTranslationSuggestion(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (f *fakeStore) UpsertTranslationOverride(context.Context, models.TranslationOverride) error {
	return nil
}
func (f *fakeStore) ListTranslationOverrides(_ context.Context, recipeID, locale string) ([]models.TranslationOverride, error) {
	if f.listTranslationOverridesFn != nil {
		return f.listTranslationOverridesFn(recipeID, locale)
	}
	return nil, nil
}
func (f *fakeStore) DeleteTranslationOverride(_ context.Context, id string) (models.TranslationOverride, error) {
	if f.deleteTranslationOverrideFn != nil {
		return f.deleteTranslationOverrideFn(id)
	}
	return models.TranslationOverride{}, nil
}
func (f *fakeStore) DeleteTranslationOverridesByFieldPrefix(_ context.Context, recipeID, locale, prefix string) error {
	if f.deleteOverridesByPrefixFn != nil {
		return f.deleteOverridesByPrefixFn(recipeID, locale, prefix)
	}
	return nil
}

func (f *fakeStore) CreateToolboxUnit(context.Context, string, string, string, float64) (models.ToolboxUnit, error) {
	return models.ToolboxUnit{}, nil
}
func (f *fakeStore) UpdateToolboxUnit(context.Context, string, string, string, string, float64) (models.ToolboxUnit, error) {
	return models.ToolboxUnit{}, nil
}
func (f *fakeStore) ListToolboxUnits(context.Context) ([]models.ToolboxUnit, error) {
	if f.listToolboxUnitsFn != nil {
		return f.listToolboxUnitsFn()
	}
	return nil, nil
}
func (f *fakeStore) GetToolboxUnitById(_ context.Context, id string) (models.ToolboxUnit, error) {
	if f.getToolboxUnitByIdFn != nil {
		return f.getToolboxUnitByIdFn(id)
	}
	return models.ToolboxUnit{}, nil
}
func (f *fakeStore) GetToolboxUnitByNameLower(context.Context, string) (models.ToolboxUnit, error) {
	return models.ToolboxUnit{}, mongorepo.ToolboxEntryNotFoundError
}
func (f *fakeStore) SetToolboxUnitTranslations(_ context.Context, id, sourceLocale string, translations map[string]models.ToolboxUnitTranslation) error {
	if f.setToolboxUnitTranslationsFn != nil {
		return f.setToolboxUnitTranslationsFn(id, sourceLocale, translations)
	}
	return nil
}

func (f *fakeStore) ListFoods(context.Context) ([]models.FoodItem, error) {
	if f.listFoodsFn != nil {
		return f.listFoodsFn()
	}
	return nil, nil
}
func (f *fakeStore) GetFoodById(_ context.Context, id string) (models.FoodItem, error) {
	if f.listFoodsFn != nil {
		foods, _ := f.listFoodsFn()
		for _, food := range foods {
			if food.Id == id {
				return food, nil
			}
		}
	}
	return models.FoodItem{}, mongorepo.FoodNotFoundError
}

func (f *fakeStore) CreateFood(_ context.Context, food models.FoodItem) error {
	if f.createFoodFn != nil {
		return f.createFoodFn(food)
	}
	return nil
}
func (f *fakeStore) UpdateFood(_ context.Context, food models.FoodItem) (models.FoodItem, error) {
	if f.updateFoodFn != nil {
		return f.updateFoodFn(food)
	}
	return food, nil
}
func (f *fakeStore) DeleteFood(context.Context, string) error {
	return nil
}
func (f *fakeStore) CountLinksForFood(_ context.Context, id string) (int64, error) {
	if f.countLinksForFoodFn != nil {
		return f.countLinksForFoodFn(id)
	}
	return 0, nil
}
func (f *fakeStore) DeleteToolboxUnit(context.Context, string) error {
	return nil
}
func (f *fakeStore) CountUnitAliasesForUnit(_ context.Context, id primitive.ObjectID) (int64, error) {
	if f.countUnitAliasesForUnitFn != nil {
		return f.countUnitAliasesForUnitFn(id)
	}
	return 0, nil
}

func (f *fakeStore) CreateIngredientNutritionLink(_ context.Context, name, foodID string, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
	if f.createIngredientNutritionLinkFn != nil {
		return f.createIngredientNutritionLinkFn(name, foodID, gPer100ml, gramsPerUnit)
	}
	return models.IngredientNutritionLink{}, nil
}
func (f *fakeStore) UpdateIngredientNutritionLink(_ context.Context, id, name, foodID string, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
	if f.updateIngredientNutritionLinkFn != nil {
		return f.updateIngredientNutritionLinkFn(id, name, foodID, gPer100ml, gramsPerUnit)
	}
	return models.IngredientNutritionLink{}, nil
}
func (f *fakeStore) GetIngredientNutritionLinkById(context.Context, string) (models.IngredientNutritionLink, error) {
	return models.IngredientNutritionLink{}, nil
}
func (f *fakeStore) GetIngredientNutritionLinkByNameLower(_ context.Context, nameLower string) (models.IngredientNutritionLink, error) {
	if f.getIngredientNutritionLinkByNameLowerFn != nil {
		return f.getIngredientNutritionLinkByNameLowerFn(nameLower)
	}
	return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
}
func (f *fakeStore) ListIngredientNutritionLinks(context.Context) ([]models.IngredientNutritionLink, error) {
	return nil, nil
}
func (f *fakeStore) DeleteIngredientNutritionLink(context.Context, string) error {
	return nil
}

func (f *fakeStore) CreateUnitAlias(_ context.Context, alias string, unitID primitive.ObjectID) (models.UnitAlias, error) {
	if f.createUnitAliasFn != nil {
		return f.createUnitAliasFn(alias, unitID)
	}
	return models.UnitAlias{}, nil
}
func (f *fakeStore) UpdateUnitAlias(context.Context, string, string, primitive.ObjectID) (models.UnitAlias, error) {
	return models.UnitAlias{}, nil
}
func (f *fakeStore) GetUnitAliasByAliasLower(_ context.Context, aliasLower string) (models.UnitAlias, error) {
	if f.getUnitAliasByAliasLowerFn != nil {
		return f.getUnitAliasByAliasLowerFn(aliasLower)
	}
	return models.UnitAlias{}, mongorepo.NutritionEntryNotFoundError
}
func (f *fakeStore) ListUnitAliases(context.Context) ([]models.UnitAlias, error) {
	return nil, nil
}
func (f *fakeStore) DeleteUnitAlias(context.Context, string) error {
	return nil
}

func (f *fakeStore) CreateNutritionSuggestion(_ context.Context, suggestion models.NutritionSuggestion) (models.NutritionSuggestion, error) {
	id := primitive.NewObjectID()
	suggestion.Id = &id
	return suggestion, nil
}
func (f *fakeStore) GetNutritionSuggestionById(_ context.Context, id string) (models.NutritionSuggestion, error) {
	if f.getNutritionSuggestionByIdFn != nil {
		return f.getNutritionSuggestionByIdFn(id)
	}
	return models.NutritionSuggestion{}, nil
}
func (f *fakeStore) ListNutritionSuggestions(context.Context, string, int64, int64) ([]models.NutritionSuggestion, int64, error) {
	return nil, 0, nil
}
func (f *fakeStore) UpdateNutritionSuggestionStatus(context.Context, string, string, string) error {
	return nil
}

type fakeTranslator struct {
	translateFn      func(recipe models.Recipe, to string) (models.Recipe, error)
	detectFn         func(recipe models.Recipe) (string, error)
	translateTextsFn func(texts []string, to string) ([]string, error)
	detectLocaleFn   func(text string) (string, error)
}

func (f fakeTranslator) TranslateRecipe(recipe models.Recipe, to string) (models.Recipe, error) {
	return f.translateFn(recipe, to)
}
func (f fakeTranslator) GetRecipeLocale(recipe models.Recipe) (string, error) {
	return f.detectFn(recipe)
}
func (f fakeTranslator) TranslateTexts(texts []string, to string) ([]string, error) {
	if f.translateTextsFn != nil {
		return f.translateTextsFn(texts, to)
	}
	out := make([]string, len(texts))
	for i, text := range texts {
		out[i] = text + "-" + to
	}
	return out, nil
}
func (f fakeTranslator) DetectLocale(text string) (string, error) {
	if f.detectLocaleFn != nil {
		return f.detectLocaleFn(text)
	}
	return "en", nil
}

func canonicalRecipe() models.Recipe {
	id := primitive.NewObjectID()
	return models.Recipe{Id: &id, SourceLocale: "en", SourceHash: "hash-v1", Title: "Pancakes"}
}

func TestLocalizeServesMatchingTranslation(t *testing.T) {
	canonical := canonicalRecipe()
	store := &fakeStore{getRecipeByIdLocaleFn: func(id, locale string) (models.Recipe, error) {
		return models.Recipe{Id: canonical.Id, SourceHash: "hash-v1", Title: "Crêpes"}, nil
	}}
	s := &Service{db: store}

	got := s.localize(canonical, "fr")
	if got.Title != "Crêpes" || got.Locale != "fr" {
		t.Fatalf("got title=%q locale=%q, want Crêpes/fr", got.Title, got.Locale)
	}
}

func TestLocalizeFallsBackOnStaleOrMissing(t *testing.T) {
	canonical := canonicalRecipe()

	t.Run("hash mismatch", func(t *testing.T) {
		store := &fakeStore{getRecipeByIdLocaleFn: func(id, locale string) (models.Recipe, error) {
			return models.Recipe{Id: canonical.Id, SourceHash: "hash-old", Title: "Vieux"}, nil
		}}
		got := (&Service{db: store}).localize(canonical, "fr")
		if got.Title != "Pancakes" || got.Locale != "en" {
			t.Fatalf("got title=%q locale=%q, want Pancakes/en", got.Title, got.Locale)
		}
	})

	t.Run("no row", func(t *testing.T) {
		store := &fakeStore{} // GetRecipeByIdLocale returns an error
		got := (&Service{db: store}).localize(canonical, "fr")
		if got.Title != "Pancakes" || got.Locale != "en" {
			t.Fatalf("got title=%q locale=%q, want Pancakes/en", got.Title, got.Locale)
		}
	})
}

func TestLocalizeSkipsLookupWhenPointless(t *testing.T) {
	for _, tc := range []struct {
		name      string
		canonical models.Recipe
		requested string
	}{
		{"same base locale", canonicalRecipe(), "en-US"},
		{"empty request", canonicalRecipe(), ""},
		{"no source hash", models.Recipe{Id: canonicalRecipe().Id, SourceLocale: "en"}, "fr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			got := (&Service{db: store}).localize(tc.canonical, tc.requested)
			if store.getRecipeByIdLocale != 0 {
				t.Fatalf("GetRecipeByIdLocale called %d times, want 0", store.getRecipeByIdLocale)
			}
			if got.Locale != tc.canonical.SourceLocale {
				t.Fatalf("Locale = %q, want %q", got.Locale, tc.canonical.SourceLocale)
			}
		})
	}
}

func TestTargetLocalesFor(t *testing.T) {
	if got := targetLocalesFor("fr", []string{"en", "fr", "fi"}); !reflect.DeepEqual(got, []string{"en", "fi"}) {
		t.Fatalf("targetLocalesFor(fr) = %v, want [en fi]", got)
	}
	if got := targetLocalesFor("fr-FR", []string{"en", "fr", "fi"}); !reflect.DeepEqual(got, []string{"en", "fi"}) {
		t.Fatalf("targetLocalesFor(fr-FR) = %v, want [en fi]", got)
	}
	if got := targetLocalesFor("", []string{"en", "fr", "fi"}); !reflect.DeepEqual(got, []string{"en", "fr", "fi"}) {
		t.Fatalf("targetLocalesFor(empty) = %v, want all", got)
	}
}

func TestTranslateLocaleRetriesThenGivesUp(t *testing.T) {
	restore := translateBackoffs
	translateBackoffs = []time.Duration{0, 0, 0}
	defer func() { translateBackoffs = restore }()

	canonical := canonicalRecipe()
	boom := errors.New("google is down")
	calls := 0
	s := &Service{
		db:             &fakeStore{},
		translator:     fakeTranslator{translateFn: func(models.Recipe, string) (models.Recipe, error) { calls++; return models.Recipe{}, boom }},
		translateSlots: make(chan struct{}, translateConcurrency),
	}

	err := s.translateLocale(canonical, "fr", true)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if calls != len(translateBackoffs) {
		t.Fatalf("translate attempts = %d, want %d", calls, len(translateBackoffs))
	}
}

func TestTranslateLocaleSucceedsAfterRetry(t *testing.T) {
	restore := translateBackoffs
	translateBackoffs = []time.Duration{0, 0, 0}
	defer func() { translateBackoffs = restore }()

	canonical := canonicalRecipe()
	calls := 0
	var stored models.Recipe
	s := &Service{
		db: &fakeStore{addLocaleRecipeFn: func(recipe models.Recipe, locale string) (models.Recipe, error) {
			stored = recipe
			return recipe, nil
		}},
		translator: fakeTranslator{translateFn: func(r models.Recipe, to string) (models.Recipe, error) {
			calls++
			if calls < 2 {
				return models.Recipe{}, errors.New("transient")
			}
			return models.Recipe{Title: "Crêpes"}, nil
		}},
		translateSlots: make(chan struct{}, translateConcurrency),
	}

	if err := s.translateLocale(canonical, "fr", true); err != nil {
		t.Fatalf("translateLocale: %v", err)
	}
	if calls != 2 {
		t.Fatalf("translate attempts = %d, want 2", calls)
	}
	if stored.SourceHash != canonical.SourceHash || stored.SourceLocale != "en" || stored.Locale != "fr" {
		t.Fatalf("stored row = %+v, want hash/source/locale hash-v1/en/fr", stored)
	}
}

func TestTranslateLocaleSkipsFreshRowWhenNotForced(t *testing.T) {
	restore := translateBackoffs
	translateBackoffs = []time.Duration{0, 0, 0}
	defer func() { translateBackoffs = restore }()

	canonical := canonicalRecipe()
	store := &fakeStore{getRecipeByIdLocaleFn: func(id, locale string) (models.Recipe, error) {
		return models.Recipe{SourceHash: canonical.SourceHash}, nil
	}}
	translated := false
	s := &Service{
		db:             store,
		translator:     fakeTranslator{translateFn: func(models.Recipe, string) (models.Recipe, error) { translated = true; return models.Recipe{}, nil }},
		translateSlots: make(chan struct{}, translateConcurrency),
	}

	if err := s.translateLocale(canonical, "fr", false); err != nil {
		t.Fatalf("translateLocale: %v", err)
	}
	if translated {
		t.Fatal("translator was called for an already-fresh row")
	}
}

func TestApplyTranslationOverridesReappliesSurvivingOverride(t *testing.T) {
	canonical := canonicalRecipe() // Title: "Pancakes"
	overrideID := primitive.NewObjectID()
	var deleted []string
	store := &fakeStore{
		listTranslationOverridesFn: func(recipeID, locale string) ([]models.TranslationOverride, error) {
			return []models.TranslationOverride{{
				Id: &overrideID, RecipeID: *canonical.Id, Locale: locale,
				FieldPath: "title", Value: "Crêpes (corrigé)", SourceSnapshot: "Pancakes",
			}}, nil
		},
		deleteTranslationOverrideFn: func(id string) (models.TranslationOverride, error) {
			deleted = append(deleted, id)
			return models.TranslationOverride{}, nil
		},
	}
	s := &Service{db: store}

	got := s.applyTranslationOverrides(canonical, models.Recipe{Title: "Crêpes (frais)"}, "fr")
	if got.Title != "Crêpes (corrigé)" {
		t.Fatalf("Title = %q, want the reapplied override value", got.Title)
	}
	if len(deleted) != 0 {
		t.Fatalf("override was deleted, want it kept: %v", deleted)
	}
}

func TestApplyTranslationOverridesDropsDriftedOverride(t *testing.T) {
	canonical := canonicalRecipe() // Title now "Pancakes", but override was approved against "Crepes"
	overrideID := primitive.NewObjectID()
	var deleted []string
	store := &fakeStore{
		listTranslationOverridesFn: func(recipeID, locale string) ([]models.TranslationOverride, error) {
			return []models.TranslationOverride{{
				Id: &overrideID, RecipeID: *canonical.Id, Locale: locale,
				FieldPath: "title", Value: "Crêpes (corrigé)", SourceSnapshot: "Crepes",
			}}, nil
		},
		deleteTranslationOverrideFn: func(id string) (models.TranslationOverride, error) {
			deleted = append(deleted, id)
			return models.TranslationOverride{}, nil
		},
	}
	s := &Service{db: store}

	got := s.applyTranslationOverrides(canonical, models.Recipe{Title: "Crêpes (frais)"}, "fr")
	if got.Title != "Crêpes (frais)" {
		t.Fatalf("Title = %q, want the fresh machine translation left untouched", got.Title)
	}
	if len(deleted) != 1 || deleted[0] != overrideID.Hex() {
		t.Fatalf("deleted = %v, want exactly the drifted override %q", deleted, overrideID.Hex())
	}
}

func TestApplyTranslationOverridesDropsAllOnIngredientCountChange(t *testing.T) {
	canonical := canonicalRecipe()
	canonical.Ingredients = []models.Ingredient{{Name: "Flour"}, {Name: "Egg"}, {Name: "Milk"}} // now 3
	titleOverrideID := primitive.NewObjectID()
	ingredientOverrideID := primitive.NewObjectID()
	approvedLen := 2 // overrides were approved when there were only 2 ingredients
	var deletedPrefixes []string
	var deletedIDs []string
	store := &fakeStore{
		listTranslationOverridesFn: func(recipeID, locale string) ([]models.TranslationOverride, error) {
			return []models.TranslationOverride{
				{Id: &titleOverrideID, RecipeID: *canonical.Id, Locale: locale, FieldPath: "title", Value: "Crêpes (corrigé)", SourceSnapshot: "Pancakes"},
				{Id: &ingredientOverrideID, RecipeID: *canonical.Id, Locale: locale, FieldPath: "ingredients.1.name", Value: "Œuf", SourceSnapshot: "Egg", IngredientsLen: &approvedLen},
			}, nil
		},
		deleteOverridesByPrefixFn: func(recipeID, locale, prefix string) error {
			deletedPrefixes = append(deletedPrefixes, prefix)
			return nil
		},
		deleteTranslationOverrideFn: func(id string) (models.TranslationOverride, error) {
			deletedIDs = append(deletedIDs, id)
			return models.TranslationOverride{}, nil
		},
	}
	s := &Service{db: store}

	translated := models.Recipe{Title: "Crêpes (frais)", Ingredients: []models.Ingredient{{Name: "Farine"}, {Name: "Oeuf (frais)"}, {Name: "Lait"}}}
	got := s.applyTranslationOverrides(canonical, translated, "fr")

	if got.Title != "Crêpes (corrigé)" {
		t.Fatalf("Title = %q, want the surviving title override reapplied", got.Title)
	}
	if got.Ingredients[1].Name != "Oeuf (frais)" {
		t.Fatalf("Ingredients[1].Name = %q, want the fresh machine translation (ingredient override dropped)", got.Ingredients[1].Name)
	}
	if len(deletedPrefixes) != 1 || deletedPrefixes[0] != "ingredients." {
		t.Fatalf("deletedPrefixes = %v, want exactly [ingredients.]", deletedPrefixes)
	}
	if len(deletedIDs) != 0 {
		t.Fatalf("individual deletes = %v, want none (the prefix delete already covers it)", deletedIDs)
	}
}

func TestApplyTranslationOverridesStepOnlyChangeLeavesIngredientOverridesAlone(t *testing.T) {
	canonical := canonicalRecipe()
	canonical.Ingredients = []models.Ingredient{{Name: "Flour"}}
	canonical.Steps = []models.Step{{Description: "Mix"}, {Description: "Bake"}, {Description: "Serve"}} // now 3
	ingredientOverrideID := primitive.NewObjectID()
	stepOverrideID := primitive.NewObjectID()
	ingredientsApprovedLen := 1
	stepsApprovedLen := 2 // overrides were approved when there were only 2 steps
	var deletedPrefixes []string
	store := &fakeStore{
		listTranslationOverridesFn: func(recipeID, locale string) ([]models.TranslationOverride, error) {
			return []models.TranslationOverride{
				{Id: &ingredientOverrideID, RecipeID: *canonical.Id, Locale: locale, FieldPath: "ingredients.0.name", Value: "Farine (corrigé)", SourceSnapshot: "Flour", IngredientsLen: &ingredientsApprovedLen},
				{Id: &stepOverrideID, RecipeID: *canonical.Id, Locale: locale, FieldPath: "steps.0.description", Value: "Mélanger (corrigé)", SourceSnapshot: "Mix", StepsLen: &stepsApprovedLen},
			}, nil
		},
		deleteOverridesByPrefixFn: func(recipeID, locale, prefix string) error {
			deletedPrefixes = append(deletedPrefixes, prefix)
			return nil
		},
	}
	s := &Service{db: store}

	translated := models.Recipe{
		Ingredients: []models.Ingredient{{Name: "Farine (frais)"}},
		Steps:       []models.Step{{Description: "Mélanger (frais)"}, {Description: "Cuire"}, {Description: "Servir"}},
	}
	got := s.applyTranslationOverrides(canonical, translated, "fr")

	if got.Ingredients[0].Name != "Farine (corrigé)" {
		t.Fatalf("Ingredients[0].Name = %q, want the surviving ingredient override reapplied", got.Ingredients[0].Name)
	}
	if got.Steps[0].Description != "Mélanger (frais)" {
		t.Fatalf("Steps[0].Description = %q, want the fresh machine translation (step override dropped)", got.Steps[0].Description)
	}
	if len(deletedPrefixes) != 1 || deletedPrefixes[0] != "steps." {
		t.Fatalf("deletedPrefixes = %v, want exactly [steps.]", deletedPrefixes)
	}
}

func TestGetDecoratesFavorite(t *testing.T) {
	canonical := canonicalRecipe()
	store := &fakeStore{
		getRecipeByIdFn: func(string) (models.Recipe, error) { return canonical, nil },
		getFavoriteInfoFn: func(ids []string, userID string) (map[string]models.FavoriteInfo, error) {
			if len(ids) != 1 || ids[0] != canonical.Id.Hex() || userID != "user-1" {
				t.Fatalf("GetFavoriteInfo called with ids=%v userID=%q", ids, userID)
			}
			return map[string]models.FavoriteInfo{canonical.Id.Hex(): {Count: 3, Favorited: true}}, nil
		},
		getFamilyFavoriteInfoFn: func([]string, string) (map[string]models.FavoriteInfo, error) {
			t.Fatal("GetFamilyFavoriteInfo should not be called by Get - a detail page shows the exact recipe's own favorite status, not the family's")
			return nil, nil
		},
	}
	s := &Service{db: store}

	got, err := s.Get(context.Background(), canonical.Id.Hex(), "", "user-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Favorite || got.FavoriteCount != 3 {
		t.Fatalf("got Favorite=%v FavoriteCount=%d, want true/3", got.Favorite, got.FavoriteCount)
	}
}

func TestGetLeavesFavoriteZeroValueOnLookupError(t *testing.T) {
	canonical := canonicalRecipe()
	store := &fakeStore{
		getRecipeByIdFn:   func(string) (models.Recipe, error) { return canonical, nil },
		getFavoriteInfoFn: func([]string, string) (map[string]models.FavoriteInfo, error) { return nil, errors.New("boom") },
	}
	s := &Service{db: store}

	got, err := s.Get(context.Background(), canonical.Id.Hex(), "", "user-1")
	if err != nil {
		t.Fatalf("Get: %v, want success despite favorite lookup failure", err)
	}
	if got.Favorite || got.FavoriteCount != 0 {
		t.Fatalf("got Favorite=%v FavoriteCount=%d, want false/0", got.Favorite, got.FavoriteCount)
	}
}

func TestListBoostedMergesFavoritedFirstAndDecorates(t *testing.T) {
	favoritedID := primitive.NewObjectID()
	restID := primitive.NewObjectID()
	store := &fakeStore{
		getRecipeDocumentsBoostedFn: func(userID string, parameters models.GetRecipesRequest) ([]models.Recipe, []models.Recipe, int64, error) {
			if userID != "user-1" {
				t.Fatalf("GetRecipeDocumentsBoosted userID = %q, want user-1", userID)
			}
			return []models.Recipe{{Id: &favoritedID, FamilyID: &favoritedID, Title: "Favorited"}},
				[]models.Recipe{{Id: &restID, FamilyID: &restID, Title: "Rest"}}, 5, nil
		},
		getFamilyFavoriteInfoFn: func(rootIDs []string, userID string) (map[string]models.FavoriteInfo, error) {
			return map[string]models.FavoriteInfo{
				favoritedID.Hex(): {Count: 2, Favorited: true},
				restID.Hex():      {Count: 0, Favorited: false},
			}, nil
		},
	}
	s := &Service{db: store}

	previews, total, err := s.List(context.Background(), models.GetRecipesRequest{Favorite: true}, "user-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(previews) != 2 || previews[0].Id.Hex() != favoritedID.Hex() || previews[1].Id.Hex() != restID.Hex() {
		t.Fatalf("previews = %+v, want [favorited, rest] in that order", previews)
	}
	if !previews[0].Favorite || previews[0].FavoriteCount != 2 {
		t.Fatalf("favorited preview = %+v, want Favorite=true FavoriteCount=2", previews[0])
	}
	if previews[1].Favorite || previews[1].FavoriteCount != 0 {
		t.Fatalf("rest preview = %+v, want Favorite=false FavoriteCount=0", previews[1])
	}
}

func TestListIgnoresFavoriteFlagWhenAnonymous(t *testing.T) {
	store := &fakeStore{
		getRecipeDocumentsFn: func(models.GetRecipesRequest) ([]models.Recipe, int64, error) {
			return nil, 0, nil
		},
	}
	s := &Service{db: store}

	if _, _, err := s.List(context.Background(), models.GetRecipesRequest{Favorite: true}, ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.getRecipeDocumentsBoosted != 0 {
		t.Fatalf("GetRecipeDocumentsBoosted called %d times for an anonymous request, want 0", store.getRecipeDocumentsBoosted)
	}
}

func TestDeleteRejectsRecipeWithFavorites(t *testing.T) {
	id := primitive.NewObjectID().Hex()
	store := &fakeStore{
		getFavoriteInfoFn: func(ids []string, userID string) (map[string]models.FavoriteInfo, error) {
			return map[string]models.FavoriteInfo{id: {Count: 1}}, nil
		},
	}
	s := &Service{db: store}

	_, err := s.Delete(context.Background(), id)
	if !errors.Is(err, ErrHasFavorites) {
		t.Fatalf("Delete err = %v, want ErrHasFavorites", err)
	}
	if store.deleteRecipeById != 0 {
		t.Fatalf("DeleteRecipeById called %d times, want 0", store.deleteRecipeById)
	}
}

func TestDeleteAllowsRecipeWithoutFavorites(t *testing.T) {
	id := primitive.NewObjectID().Hex()
	store := &fakeStore{
		getFavoriteInfoFn: func(ids []string, userID string) (map[string]models.FavoriteInfo, error) {
			return map[string]models.FavoriteInfo{}, nil
		},
	}
	s := &Service{db: store}

	if _, err := s.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if store.deleteRecipeById != 1 {
		t.Fatalf("DeleteRecipeById called %d times, want 1", store.deleteRecipeById)
	}
}

func validRecipeDB(ingredients []models.Ingredient) models.RecipeDB {
	return models.RecipeDB{
		Title:       "Title",
		Description: "Description",
		Quantity:    1,
		Kind:        models.RecipeKinds[0],
		Ingredients: ingredients,
		Steps:       []models.Step{{Description: "Do it"}},
	}
}

func TestValidateRecipeTrimsIngredientLabel(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{{Name: "Flour", Label: "  For the dough  "}})

	if err := validateRecipe(&recipe); err != nil {
		t.Fatalf("validateRecipe: %v", err)
	}
	if got := recipe.Ingredients[0].Label; got != "For the dough" {
		t.Fatalf("Label = %q, want %q", got, "For the dough")
	}
}

func TestValidateRecipeClearsDiyCookAndRestTime(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{{Name: "Lye"}})
	recipe.Category = models.Diy
	recipe.PreparationTime, recipe.CookingTime, recipe.RestingTime = 30, 10, 20

	if err := validateRecipe(&recipe); err != nil {
		t.Fatalf("validateRecipe: %v", err)
	}
	if recipe.PreparationTime != 30 || recipe.CookingTime != 0 || recipe.RestingTime != 0 {
		t.Fatalf("times = %d/%d/%d, want 30/0/0", recipe.PreparationTime, recipe.CookingTime, recipe.RestingTime)
	}
}

func TestValidateRecipeCanonicalizesLabelCasing(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{
		{Name: "Flour", Label: "For the Dough"},
		{Name: "Egg", Label: "for the dough"},
		{Name: "Apple", Label: "For The DOUGH"},
	})

	if err := validateRecipe(&recipe); err != nil {
		t.Fatalf("validateRecipe: %v", err)
	}
	for i, ingredient := range recipe.Ingredients {
		if ingredient.Label != "For the Dough" {
			t.Fatalf("Ingredients[%d].Label = %q, want canonical %q", i, ingredient.Label, "For the Dough")
		}
	}
}

func TestValidateRecipeRejectsOverlongLabel(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{{Name: "Flour", Label: strings.Repeat("a", maxIngredientFieldLength+1)}})

	if err := validateRecipe(&recipe); !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateRecipe err = %v, want ErrInvalid", err)
	}
}

func TestValidateRecipeAllowsEmptyLabel(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{{Name: "Flour"}})

	if err := validateRecipe(&recipe); err != nil {
		t.Fatalf("validateRecipe: %v", err)
	}
	if recipe.Ingredients[0].Label != "" {
		t.Fatalf("Label = %q, want empty", recipe.Ingredients[0].Label)
	}
}

func TestValidateRecipeNormalizesEmptyCategoryToFood(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{{Name: "Flour"}})
	recipe.Category = ""

	if err := validateRecipe(&recipe); err != nil {
		t.Fatalf("validateRecipe: %v", err)
	}
	if recipe.Category != models.Food {
		t.Fatalf("Category = %q, want %q", recipe.Category, models.Food)
	}
}

func TestValidateRecipeRejectsUnknownCategory(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{{Name: "Flour"}})
	recipe.Category = models.RecipeCategory("bogus")

	if err := validateRecipe(&recipe); !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateRecipe err = %v, want ErrInvalid", err)
	}
}

func TestValidateRecipeRequiresKindForFoodCategory(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{{Name: "Flour"}})
	recipe.Category = models.Food
	recipe.Kind = models.RecipeKind("bogus")

	if err := validateRecipe(&recipe); !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateRecipe err = %v, want ErrInvalid", err)
	}
}

func TestValidateRecipeSkipsKindForDiyCategory(t *testing.T) {
	recipe := validRecipeDB([]models.Ingredient{{Name: "Baking soda"}})
	recipe.Category = models.Diy
	recipe.Kind = ""

	if err := validateRecipe(&recipe); err != nil {
		t.Fatalf("validateRecipe: %v", err)
	}
}

// pictureUpload builds a small valid PNG upload for tests that exercise
// normalization/writing.
func pictureUpload(t *testing.T) PictureUpload {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return PictureUpload{Filename: "step.png", MediaType: "image/png", Data: buf.Bytes()}
}

func TestStepPictureUploadsValidatesRange(t *testing.T) {
	s := &Service{}

	t.Run("empty map is a no-op", func(t *testing.T) {
		indices, uploads, err := s.stepPictureUploads(nil, 3)
		if err != nil || indices != nil || uploads != nil {
			t.Fatalf("got (%v, %v, %v), want (nil, nil, nil)", indices, uploads, err)
		}
	})

	t.Run("rejects out-of-range index", func(t *testing.T) {
		_, _, err := s.stepPictureUploads(map[int]PictureUpload{2: {}}, 2)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("orders by ascending index", func(t *testing.T) {
		upload0, upload2 := PictureUpload{Filename: "a"}, PictureUpload{Filename: "b"}
		indices, uploads, err := s.stepPictureUploads(map[int]PictureUpload{2: upload2, 0: upload0}, 3)
		if err != nil {
			t.Fatalf("stepPictureUploads: %v", err)
		}
		if !reflect.DeepEqual(indices, []int{0, 2}) {
			t.Fatalf("indices = %v, want [0 2]", indices)
		}
		if uploads[0].Filename != "a" || uploads[1].Filename != "b" {
			t.Fatalf("uploads = %+v, want [a b] in order", uploads)
		}
	})
}

func TestCreateIgnoresClientSuppliedStepPictureAndUsesUpload(t *testing.T) {
	dir := t.TempDir()
	var stored models.CreateRecipe
	store := &fakeStore{createRecipeFn: func(_ string, infos *models.CreateRecipe) (models.Recipe, error) {
		stored = *infos
		return models.Recipe{Id: ptrObjectID(), SourceHash: infos.SourceHash, Steps: infos.Steps}, nil
	}}
	s := &Service{db: store, imageDir: dir, maxPictureBytes: 10 << 20}

	input := models.CreateRecipe{
		Title: "Pancakes", Description: "d", Quantity: 1, Kind: models.RecipeKinds[0],
		Ingredients: []models.Ingredient{{Name: "Flour"}},
		Steps:       []models.Step{{Description: "Mix", Picture: "attacker-supplied.jpg"}},
	}
	created, err := s.Create(context.Background(), "author-1", input, nil, map[int]PictureUpload{0: pictureUpload(t)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if stored.Steps[0].Picture == "attacker-supplied.jpg" || stored.Steps[0].Picture == "" {
		t.Fatalf("Steps[0].Picture = %q, want a freshly generated filename, not the client-supplied one", stored.Steps[0].Picture)
	}
	if _, err := os.Stat(filepath.Join(dir, stored.Steps[0].Picture)); err != nil {
		t.Fatalf("uploaded step picture was not written to disk: %v", err)
	}
	if created.Steps[0].Picture != stored.Steps[0].Picture {
		t.Fatalf("created.Steps[0].Picture = %q, want %q", created.Steps[0].Picture, stored.Steps[0].Picture)
	}
}

func TestCreateRejectsOutOfRangeStepPictureIndex(t *testing.T) {
	s := &Service{db: &fakeStore{}, imageDir: t.TempDir(), maxPictureBytes: 10 << 20}
	input := models.CreateRecipe{
		Title: "T", Description: "d", Quantity: 1, Kind: models.RecipeKinds[0],
		Ingredients: []models.Ingredient{{Name: "Flour"}},
		Steps:       []models.Step{{Description: "Mix"}},
	}
	_, err := s.Create(context.Background(), "author-1", input, nil, map[int]PictureUpload{5: pictureUpload(t)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func ptrObjectID() *primitive.ObjectID {
	id := primitive.NewObjectID()
	return &id
}

func TestMergePatchRejectsUnknownStepPicture(t *testing.T) {
	recipe := canonicalRecipe()
	recipe.Author = &models.UserView{Id: ptrObjectID()}
	recipe.Steps = []models.Step{{Description: "Mix", Picture: "known.jpg"}}
	patch := models.UpdateRecipeRequest{Steps: &[]models.Step{{Description: "Mix", Picture: "unknown.jpg"}}}

	_, err := mergePatch(recipe, patch, nil, false)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestMergePatchAllowsKeepingOrDroppingKnownStepPicture(t *testing.T) {
	recipe := canonicalRecipe()
	recipe.Author = &models.UserView{Id: ptrObjectID()}
	recipe.Steps = []models.Step{{Description: "Mix", Picture: "known.jpg"}}
	patch := models.UpdateRecipeRequest{Steps: &[]models.Step{
		{Description: "Mix", Picture: "known.jpg"},
		{Description: "Bake"},
	}}

	merged, err := mergePatch(recipe, patch, nil, false)
	if err != nil {
		t.Fatalf("mergePatch: %v", err)
	}
	if merged.Steps[0].Picture != "known.jpg" {
		t.Fatalf("Steps[0].Picture = %q, want kept known.jpg", merged.Steps[0].Picture)
	}
	if merged.Steps[1].Picture != "" {
		t.Fatalf("Steps[1].Picture = %q, want empty", merged.Steps[1].Picture)
	}
}

func TestMergePatchSkipsValidationForFreshUploadIndex(t *testing.T) {
	recipe := canonicalRecipe()
	recipe.Author = &models.UserView{Id: ptrObjectID()}
	recipe.Steps = []models.Step{{Description: "Mix"}}
	patch := models.UpdateRecipeRequest{Steps: &[]models.Step{{Description: "Mix", Picture: "whatever-will-be-overwritten.jpg"}}}

	merged, err := mergePatch(recipe, patch, map[int]bool{0: true}, false)
	if err != nil {
		t.Fatalf("mergePatch: %v", err)
	}
	if merged.Steps[0].Picture != "" {
		t.Fatalf("Steps[0].Picture = %q, want cleared pending the fresh upload", merged.Steps[0].Picture)
	}
}

func TestUpdateRemovesDroppedStepPictureFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old-step.jpg"), []byte("data"), 0o600); err != nil {
		t.Fatalf("seed old picture: %v", err)
	}
	recipe := canonicalRecipe()
	recipe.Author = &models.UserView{Id: ptrObjectID()}
	recipe.Quantity = 1
	recipe.Kind = models.RecipeKinds[0]
	recipe.Ingredients = []models.Ingredient{{Name: "Flour"}}
	recipe.Steps = []models.Step{{Description: "Mix", Picture: "old-step.jpg"}}
	store := &fakeStore{replaceRecipeByIdFn: func(_ string, r models.RecipeDB) (models.Recipe, error) {
		return models.Recipe{Id: recipe.Id, SourceHash: r.SourceHash, Steps: r.Steps}, nil
	}}
	s := &Service{db: store, imageDir: dir, maxPictureBytes: 10 << 20}

	patch := models.UpdateRecipeRequest{Steps: &[]models.Step{{Description: "Mix"}}}
	if _, err := s.Update(context.Background(), recipe, patch, nil, nil, false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old-step.jpg")); !os.IsNotExist(err) {
		t.Fatalf("old-step.jpg still exists after being dropped, err = %v", err)
	}
}

func TestUpdateWritesNewStepPictureAtGivenIndex(t *testing.T) {
	dir := t.TempDir()
	recipe := canonicalRecipe()
	recipe.Author = &models.UserView{Id: ptrObjectID()}
	recipe.Quantity = 1
	recipe.Kind = models.RecipeKinds[0]
	recipe.Ingredients = []models.Ingredient{{Name: "Flour"}}
	recipe.Steps = []models.Step{{Description: "Mix"}}
	var storedSteps []models.Step
	store := &fakeStore{replaceRecipeByIdFn: func(_ string, r models.RecipeDB) (models.Recipe, error) {
		storedSteps = r.Steps
		return models.Recipe{Id: recipe.Id, SourceHash: r.SourceHash, Steps: r.Steps}, nil
	}}
	s := &Service{db: store, imageDir: dir, maxPictureBytes: 10 << 20}

	patch := models.UpdateRecipeRequest{Steps: &[]models.Step{{Description: "Mix"}}}
	if _, err := s.Update(context.Background(), recipe, patch, nil, map[int]PictureUpload{0: pictureUpload(t)}, false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if storedSteps[0].Picture == "" {
		t.Fatal("Steps[0].Picture is empty, want the freshly uploaded filename")
	}
	if _, err := os.Stat(filepath.Join(dir, storedSteps[0].Picture)); err != nil {
		t.Fatalf("new step picture was not written to disk: %v", err)
	}
}

func TestDeleteRemovesStepPicturesFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "step-pic.jpg"), []byte("data"), 0o600); err != nil {
		t.Fatalf("seed picture: %v", err)
	}
	id := primitive.NewObjectID().Hex()
	store := &fakeStore{
		getFavoriteInfoFn: func(ids []string, userID string) (map[string]models.FavoriteInfo, error) {
			return map[string]models.FavoriteInfo{}, nil
		},
		deleteRecipeByIdFn: func(string) (models.RecipeDB, error) {
			return models.RecipeDB{Steps: []models.Step{{Description: "Mix", Picture: "step-pic.jpg"}}}, nil
		},
	}
	s := &Service{db: store, imageDir: dir}

	if _, err := s.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "step-pic.jpg")); !os.IsNotExist(err) {
		t.Fatalf("step-pic.jpg still exists after recipe deletion, err = %v", err)
	}
}

func validCreateRecipe() models.CreateRecipe {
	return models.CreateRecipe{
		Title: "Cinnamon Rolls", Description: "d", Quantity: 1, Kind: models.RecipeKinds[0],
		Ingredients: []models.Ingredient{{Name: "Flour"}},
		Steps:       []models.Step{{Description: "Mix"}},
	}
}

// recipeLookup serves recipes by id, with FamilyID read from store.families
// at fetch time so it reflects any family change the service made.
func recipeLookup(store *fakeStore, recipes ...models.Recipe) func(string) (models.Recipe, error) {
	return func(id string) (models.Recipe, error) {
		for _, recipe := range recipes {
			if recipe.Id.Hex() != id {
				continue
			}
			if family, ok := store.families[id]; ok {
				familyID, _ := primitive.ObjectIDFromHex(family)
				recipe.FamilyID = &familyID
			}
			return recipe, nil
		}
		return models.Recipe{}, mongorepo.NotFoundError
	}
}

// familyOfThree registers A, B, C (oldest first) as one family keyed by A.
func familyOfThree(store *fakeStore) (a, b, c models.Recipe) {
	a = models.Recipe{Id: ptrObjectID(), Category: models.Food, Author: &models.UserView{Id: ptrObjectID()}}
	b = models.Recipe{Id: ptrObjectID(), Category: models.Food, Author: &models.UserView{Id: ptrObjectID()}}
	c = models.Recipe{Id: ptrObjectID(), Category: models.Food, Author: &models.UserView{Id: ptrObjectID()}}
	store.addFamily(a.Id.Hex(), a.Id.Hex(), b.Id.Hex(), c.Id.Hex())
	return a, b, c
}

func assertFamilies(t *testing.T, store *fakeStore, want map[string]string) {
	t.Helper()
	for id, family := range want {
		if store.families[id] != family {
			t.Fatalf("family of %s = %s, want %s (families = %v)", id, store.families[id], family, store.families)
		}
	}
}

func noFavorites(store *fakeStore) {
	store.getFavoriteInfoFn = func([]string, string) (map[string]models.FavoriteInfo, error) {
		return map[string]models.FavoriteInfo{}, nil
	}
}

func TestCreateJoinsTargetFamily(t *testing.T) {
	store := &fakeStore{}
	_, b, _ := familyOfThree(store)
	var stored models.CreateRecipe
	store.getRecipeByIdFn = recipeLookup(store, b)
	store.createRecipeFn = func(_ string, infos *models.CreateRecipe) (models.Recipe, error) {
		stored = *infos
		return models.Recipe{Id: ptrObjectID(), SourceHash: infos.SourceHash}, nil
	}
	s := &Service{db: store, imageDir: t.TempDir(), maxPictureBytes: 10 << 20}

	input := validCreateRecipe()
	input.VariationOf = b.Id
	if _, err := s.Create(context.Background(), "author-1", input, nil, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if stored.FamilyID == nil || stored.FamilyID.Hex() != store.families[b.Id.Hex()] {
		t.Fatalf("stored.FamilyID = %v, want the target's family %s", stored.FamilyID, store.families[b.Id.Hex()])
	}
}

func TestCreateVersionOfMissingTargetReturnsNotFound(t *testing.T) {
	store := &fakeStore{
		getRecipeByIdFn: func(string) (models.Recipe, error) { return models.Recipe{}, mongorepo.NotFoundError },
	}
	s := &Service{db: store, imageDir: t.TempDir(), maxPictureBytes: 10 << 20}

	input := validCreateRecipe()
	input.VariationOf = ptrObjectID()
	_, err := s.Create(context.Background(), "author-1", input, nil, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Create err = %v, want ErrNotFound", err)
	}
}

func TestCreateRejectsVersionCategoryMismatch(t *testing.T) {
	targetID := ptrObjectID()
	target := models.Recipe{Id: targetID, FamilyID: targetID, Category: models.Food, Author: &models.UserView{Id: ptrObjectID()}}
	store := &fakeStore{
		getRecipeByIdFn: func(string) (models.Recipe, error) { return target, nil },
	}
	s := &Service{db: store, imageDir: t.TempDir(), maxPictureBytes: 10 << 20}

	input := validCreateRecipe()
	input.VariationOf = targetID
	input.Category = models.Diy
	_, err := s.Create(context.Background(), "author-1", input, nil, nil)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create err = %v, want ErrInvalid", err)
	}
}

func TestCreateAllowsVersionCategoryMatch(t *testing.T) {
	targetID := ptrObjectID()
	target := models.Recipe{Id: targetID, FamilyID: targetID, Category: models.Diy, Author: &models.UserView{Id: ptrObjectID()}}
	var stored models.CreateRecipe
	store := &fakeStore{
		getRecipeByIdFn: func(string) (models.Recipe, error) { return target, nil },
		createRecipeFn: func(_ string, infos *models.CreateRecipe) (models.Recipe, error) {
			stored = *infos
			return models.Recipe{Id: ptrObjectID(), SourceHash: infos.SourceHash}, nil
		},
	}
	s := &Service{db: store, imageDir: t.TempDir(), maxPictureBytes: 10 << 20}

	input := validCreateRecipe()
	input.VariationOf = targetID
	input.Category = models.Diy
	if _, err := s.Create(context.Background(), "author-1", input, nil, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if stored.Category != models.Diy {
		t.Fatalf("stored.Category = %q, want %q", stored.Category, models.Diy)
	}
}

func TestUpdateRejectsVersionCategoryMismatch(t *testing.T) {
	store := &fakeStore{}
	a := models.Recipe{Id: ptrObjectID(), Category: models.Food}
	recipe := canonicalRecipe()
	recipe.Author = &models.UserView{Id: ptrObjectID()}
	recipe.Quantity = 1
	recipe.Kind = models.RecipeKinds[0]
	recipe.Category = models.Food
	recipe.Ingredients = []models.Ingredient{{Name: "Flour"}}
	recipe.Steps = []models.Step{{Description: "Mix"}}
	recipe.FamilyID = a.Id
	store.addFamily(a.Id.Hex(), a.Id.Hex(), recipe.Id.Hex())
	store.getRecipeByIdFn = recipeLookup(store, a)
	s := &Service{db: store, imageDir: t.TempDir(), maxPictureBytes: 10 << 20}

	diy := models.Diy
	patch := models.UpdateRecipeRequest{Category: &diy}
	_, err := s.Update(context.Background(), recipe, patch, nil, nil, false)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Update err = %v, want ErrInvalid", err)
	}
}

func TestSourceHashIgnoresFamilyID(t *testing.T) {
	base := validRecipeDB(nil)
	base.Ingredients = []models.Ingredient{{Name: "Flour"}}
	withFamily := base
	withFamily.FamilyID = ptrObjectID()

	if sourceHash(base) != sourceHash(withFamily) {
		t.Fatal("sourceHash differs based on FamilyID alone, want it to depend only on content")
	}
}

func TestDeleteRepointsRefsToNewestRemaining(t *testing.T) {
	store := &fakeStore{}
	noFavorites(store)
	a, b, c := familyOfThree(store)
	store.getRecipeByIdFn = recipeLookup(store, a, b, c)
	store.hasIncomingReferencesFn = func(id string) (bool, error) { return id == b.Id.Hex(), nil }
	store.repointRecipeReferencesFn = func(oldID, newID string) error {
		if oldID != b.Id.Hex() || newID != c.Id.Hex() {
			t.Fatalf("RepointRecipeReferences(%s, %s), want (%s, %s)", oldID, newID, b.Id.Hex(), c.Id.Hex())
		}
		return nil
	}
	s := &Service{db: store}

	if _, err := s.Delete(context.Background(), b.Id.Hex()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	want := []string{"RepointRecipeReferences", "DeleteRecipeById"}
	if !reflect.DeepEqual(store.callOrder, want) {
		t.Fatalf("callOrder = %v, want %v", store.callOrder, want)
	}
}

func TestDeleteBlockedWhenReferencedAndAlone(t *testing.T) {
	store := &fakeStore{}
	noFavorites(store)
	a := models.Recipe{Id: ptrObjectID()}
	store.addFamily(a.Id.Hex(), a.Id.Hex())
	store.getRecipeByIdFn = recipeLookup(store, a)
	store.hasIncomingReferencesFn = func(string) (bool, error) { return true, nil }
	s := &Service{db: store}

	if _, err := s.Delete(context.Background(), a.Id.Hex()); !errors.Is(err, ErrRecipeReferenced) {
		t.Fatalf("Delete err = %v, want ErrRecipeReferenced", err)
	}
	if store.deleteRecipeById != 0 || store.repointRecipeReferences != 0 {
		t.Fatalf("delete:%d repoint:%d, want 0/0", store.deleteRecipeById, store.repointRecipeReferences)
	}
}

func TestDeleteStillBlockedByFavorites(t *testing.T) {
	store := &fakeStore{}
	_, b, _ := familyOfThree(store)
	store.getFavoriteInfoFn = func([]string, string) (map[string]models.FavoriteInfo, error) {
		return map[string]models.FavoriteInfo{b.Id.Hex(): {Count: 1}}, nil
	}
	store.hasIncomingReferencesFn = func(string) (bool, error) {
		t.Fatal("HasIncomingReferences should not be called when the favorites guard already blocks the delete")
		return false, nil
	}
	s := &Service{db: store}

	if _, err := s.Delete(context.Background(), b.Id.Hex()); !errors.Is(err, ErrHasFavorites) {
		t.Fatalf("Delete err = %v, want ErrHasFavorites", err)
	}
	if store.deleteRecipeById != 0 || store.repointRecipeReferences != 0 {
		t.Fatalf("delete:%d repoint:%d, want 0/0", store.deleteRecipeById, store.repointRecipeReferences)
	}
}

func TestDeleteAllowedWhenNotReferenced(t *testing.T) {
	store := &fakeStore{}
	noFavorites(store)
	_, b, _ := familyOfThree(store)
	store.hasIncomingReferencesFn = func(string) (bool, error) { return false, nil }
	s := &Service{db: store}

	if _, err := s.Delete(context.Background(), b.Id.Hex()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if store.deleteRecipeById != 1 || store.repointRecipeReferences != 0 {
		t.Fatalf("delete:%d repoint:%d, want 1/0", store.deleteRecipeById, store.repointRecipeReferences)
	}
}

// standaloneSource registers a standalone recipe (its own family) in store.
func standaloneSource(store *fakeStore, category models.RecipeCategory) models.Recipe {
	source := models.Recipe{Id: ptrObjectID(), Category: category, Author: &models.UserView{Id: ptrObjectID()}}
	store.addFamily(source.Id.Hex(), source.Id.Hex())
	return source
}

func TestLinkVersionSuccess(t *testing.T) {
	store := &fakeStore{}
	a, b, c := familyOfThree(store)
	source := standaloneSource(store, models.Food)
	store.getRecipeByIdFn = recipeLookup(store, a, b, c, source)
	s := &Service{db: store}

	if _, err := s.LinkVariation(context.Background(), source.Id.Hex(), b.Id.Hex()); err != nil {
		t.Fatalf("LinkVariation: %v", err)
	}
	assertFamilies(t, store, map[string]string{source.Id.Hex(): a.Id.Hex(), b.Id.Hex(): a.Id.Hex()})
}

func TestLinkVersionRejectsSameRecipe(t *testing.T) {
	id := primitive.NewObjectID().Hex()
	s := &Service{db: &fakeStore{}}
	if _, err := s.LinkVariation(context.Background(), id, id); !errors.Is(err, ErrInvalid) {
		t.Fatalf("LinkVariation err = %v, want ErrInvalid", err)
	}
}

func TestLinkVersionRejectsSourceInFamily(t *testing.T) {
	store := &fakeStore{}
	a, b, c := familyOfThree(store)
	target := standaloneSource(store, models.Food)
	store.getRecipeByIdFn = recipeLookup(store, a, b, c, target)
	s := &Service{db: store}

	if _, err := s.LinkVariation(context.Background(), a.Id.Hex(), target.Id.Hex()); !errors.Is(err, ErrAlreadyInFamily) {
		t.Fatalf("LinkVariation err = %v, want ErrAlreadyInFamily", err)
	}
	assertFamilies(t, store, map[string]string{a.Id.Hex(): a.Id.Hex()})
}

func TestLinkVersionMissingTargetReturnsNotFound(t *testing.T) {
	store := &fakeStore{}
	source := standaloneSource(store, models.Food)
	store.getRecipeByIdFn = recipeLookup(store, source)
	s := &Service{db: store}

	if _, err := s.LinkVariation(context.Background(), source.Id.Hex(), primitive.NewObjectID().Hex()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LinkVariation err = %v, want ErrNotFound", err)
	}
}

func TestLinkVersionRejectsCategoryMismatch(t *testing.T) {
	store := &fakeStore{}
	a, b, c := familyOfThree(store)
	source := standaloneSource(store, models.Diy)
	store.getRecipeByIdFn = recipeLookup(store, a, b, c, source)
	s := &Service{db: store}

	if _, err := s.LinkVariation(context.Background(), source.Id.Hex(), b.Id.Hex()); !errors.Is(err, ErrCategoryMismatch) {
		t.Fatalf("LinkVariation err = %v, want ErrCategoryMismatch", err)
	}
	assertFamilies(t, store, map[string]string{source.Id.Hex(): source.Id.Hex()})
}

func TestLinkVersionRejectsWhenSourceReferencesFamily(t *testing.T) {
	store := &fakeStore{}
	a, b, c := familyOfThree(store)
	source := standaloneSource(store, models.Food)
	store.getRecipeByIdFn = recipeLookup(store, a, b, c, source)
	store.recipeReferencesFamilyFn = func(recipeID, familyID string) (bool, error) {
		return recipeID == source.Id.Hex() && familyID == a.Id.Hex(), nil
	}
	s := &Service{db: store}

	if _, err := s.LinkVariation(context.Background(), source.Id.Hex(), b.Id.Hex()); !errors.Is(err, ErrReferenceLoop) {
		t.Fatalf("LinkVariation err = %v, want ErrReferenceLoop", err)
	}
	assertFamilies(t, store, map[string]string{source.Id.Hex(): source.Id.Hex()})
}

func TestLinkVersionRejectsWhenTargetFamilyReferencesSource(t *testing.T) {
	store := &fakeStore{}
	a, b, c := familyOfThree(store)
	source := standaloneSource(store, models.Food)
	store.getRecipeByIdFn = recipeLookup(store, a, b, c, source)
	store.familyReferencesRecipeFn = func(familyID, targetID string) (bool, error) {
		return familyID == a.Id.Hex() && targetID == source.Id.Hex(), nil
	}
	s := &Service{db: store}

	if _, err := s.LinkVariation(context.Background(), source.Id.Hex(), b.Id.Hex()); !errors.Is(err, ErrReferenceLoop) {
		t.Fatalf("LinkVariation err = %v, want ErrReferenceLoop", err)
	}
	assertFamilies(t, store, map[string]string{source.Id.Hex(): source.Id.Hex()})
}

func TestDetachVersionSetsOwnFamilyID(t *testing.T) {
	store := &fakeStore{}
	a, b, c := familyOfThree(store)
	store.getRecipeByIdFn = recipeLookup(store, a, b, c)
	s := &Service{db: store}

	got, err := s.DetachVariation(context.Background(), b.Id.Hex())
	if err != nil {
		t.Fatalf("DetachVariation: %v", err)
	}
	assertFamilies(t, store, map[string]string{a.Id.Hex(): a.Id.Hex(), b.Id.Hex(): b.Id.Hex(), c.Id.Hex(): a.Id.Hex()})
	if got.FamilySize != 1 {
		t.Fatalf("detached FamilySize = %d, want 1", got.FamilySize)
	}
}

func TestDetachVersionRekeysFamilyWhenKeyIsOwnID(t *testing.T) {
	store := &fakeStore{}
	a, b, c := familyOfThree(store)
	store.getRecipeByIdFn = recipeLookup(store, a, b, c)
	s := &Service{db: store}

	if _, err := s.DetachVariation(context.Background(), a.Id.Hex()); err != nil {
		t.Fatalf("DetachVariation: %v", err)
	}
	assertFamilies(t, store, map[string]string{a.Id.Hex(): a.Id.Hex(), b.Id.Hex(): c.Id.Hex(), c.Id.Hex(): c.Id.Hex()})
}

func TestDetachVersionRejectsStandalone(t *testing.T) {
	store := &fakeStore{}
	source := standaloneSource(store, models.Food)
	store.getRecipeByIdFn = recipeLookup(store, source)
	s := &Service{db: store}

	if _, err := s.DetachVariation(context.Background(), source.Id.Hex()); !errors.Is(err, ErrNotInFamily) {
		t.Fatalf("DetachVariation err = %v, want ErrNotInFamily", err)
	}
}

func TestGetLocalizedCarriesFamilyID(t *testing.T) {
	store := &fakeStore{}
	other := models.Recipe{Id: ptrObjectID()}
	canonical := canonicalRecipe()
	store.addFamily(other.Id.Hex(), other.Id.Hex(), canonical.Id.Hex())
	store.getRecipeByIdFn = recipeLookup(store, canonical)
	store.getRecipeByIdLocaleFn = func(id, locale string) (models.Recipe, error) {
		return models.Recipe{Id: canonical.Id, SourceHash: canonical.SourceHash, Title: "Crêpes"}, nil
	}
	s := &Service{db: store}

	got, err := s.Get(context.Background(), canonical.Id.Hex(), "fr", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Crêpes" {
		t.Fatalf("Title = %q, want the stored translation", got.Title)
	}
	if got.FamilyID == nil || got.FamilyID.Hex() != other.Id.Hex() {
		t.Fatalf("FamilyID = %v, want %s", got.FamilyID, other.Id.Hex())
	}
	if got.FamilySize != 2 {
		t.Fatalf("FamilySize = %d, want 2", got.FamilySize)
	}
}

func TestGetResolvesRefIngredientTitleWhenNoLabel(t *testing.T) {
	canonical := canonicalRecipe()
	refID := primitive.NewObjectID()
	canonical.Ingredients = []models.Ingredient{{RecipeRef: &refID}}
	store := &fakeStore{
		getRecipeByIdFn: func(string) (models.Recipe, error) { return canonical, nil },
		getRecipeTitlesFn: func(ids []string) (map[string]string, error) {
			if len(ids) != 1 || ids[0] != refID.Hex() {
				t.Fatalf("GetRecipeTitles called with %v, want [%s]", ids, refID.Hex())
			}
			return map[string]string{refID.Hex(): "Tarte Tatin"}, nil
		},
	}
	s := &Service{db: store}

	got, err := s.Get(context.Background(), canonical.Id.Hex(), "", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Ingredients[0].ResolvedRefTitle != "Tarte Tatin" {
		t.Fatalf("ResolvedRefTitle = %q, want %q", got.Ingredients[0].ResolvedRefTitle, "Tarte Tatin")
	}
}

func TestGetResolvesLiveTitleEvenWithRefLabel(t *testing.T) {
	canonical := canonicalRecipe()
	refID := primitive.NewObjectID()
	canonical.Ingredients = []models.Ingredient{{RecipeRef: &refID, RefLabel: "the crust recipe"}}
	store := &fakeStore{
		getRecipeByIdFn: func(string) (models.Recipe, error) { return canonical, nil },
		getRecipeTitlesFn: func(ids []string) (map[string]string, error) {
			return map[string]string{refID.Hex(): "Shortcrust Pastry"}, nil
		},
	}
	s := &Service{db: store}

	got, err := s.Get(context.Background(), canonical.Id.Hex(), "", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Ingredients[0].ResolvedRefTitle != "Shortcrust Pastry" || got.Ingredients[0].RefLabel != "the crust recipe" {
		t.Fatalf("got ResolvedRefTitle=%q RefLabel=%q, want the live title and the label kept apart", got.Ingredients[0].ResolvedRefTitle, got.Ingredients[0].RefLabel)
	}
}

func TestListDetailedForUserResolvesRefIngredientTitles(t *testing.T) {
	id := ptrObjectID()
	refID := primitive.NewObjectID()
	store := &fakeStore{
		getRecipeByIdFn: func(string) (models.Recipe, error) { return models.Recipe{}, nil },
	}
	store.getRecipeTitlesFn = func(ids []string) (map[string]string, error) {
		return map[string]string{refID.Hex(): "Base Dough"}, nil
	}
	// GetRecipesByAuthor is not mockable via a func field on fakeStore; use the
	// Store interface directly via an embedded override.
	s := &Service{db: recipesByAuthorStore{
		fakeStore: store,
		recipes:   []models.Recipe{{Id: id, Ingredients: []models.Ingredient{{RecipeRef: &refID}}}},
	}}

	got, _, _, err := s.ListDetailedForUser(context.Background(), "user-1", "", 20, "")
	if err != nil {
		t.Fatalf("ListDetailedForUser: %v", err)
	}
	if len(got) != 1 || got[0].Ingredients[0].ResolvedRefTitle != "Base Dough" {
		t.Fatalf("got %+v, want ResolvedRefTitle = Base Dough", got)
	}
}

func TestSearchReportsNextOffsetWhenMorePagesRemain(t *testing.T) {
	var gotParameters models.GetRecipesRequest
	store := &fakeStore{
		getRecipeDocumentsFn: func(parameters models.GetRecipesRequest) ([]models.Recipe, int64, error) {
			gotParameters = parameters
			documents := make([]models.Recipe, parameters.Limit)
			for i := range documents {
				documents[i] = models.Recipe{Id: ptrObjectID()}
			}
			return documents, 99, nil
		},
	}
	s := &Service{db: store}

	got, total, nextOffset, hasNext, err := s.Search(context.Background(), models.GetRecipesRequest{Title: "curry"}, 10, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotParameters.Offset != 10 || gotParameters.Limit != 3 || gotParameters.Title != "curry" {
		t.Fatalf("GetRecipeDocuments called with %+v, want Offset=10 Limit=3 Title=curry", gotParameters)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (trimmed to the requested limit)", len(got))
	}
	if total != 99 {
		t.Fatalf("total = %d, want 99", total)
	}
	if !hasNext || nextOffset != 12 {
		t.Fatalf("hasNext=%v nextOffset=%d, want true/12", hasNext, nextOffset)
	}
}

func TestSearchReportsNoNextOffsetOnLastPage(t *testing.T) {
	store := &fakeStore{
		getRecipeDocumentsFn: func(parameters models.GetRecipesRequest) ([]models.Recipe, int64, error) {
			return []models.Recipe{{Id: ptrObjectID()}}, 1, nil
		},
	}
	s := &Service{db: store}

	got, _, _, hasNext, err := s.Search(context.Background(), models.GetRecipesRequest{}, 0, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || hasNext {
		t.Fatalf("len(got)=%d hasNext=%v, want 1/false", len(got), hasNext)
	}
}

// recipesByAuthorStore wraps fakeStore to stub GetRecipesByAuthor, which
// fakeStore itself always returns empty for.
type recipesByAuthorStore struct {
	*fakeStore
	recipes []models.Recipe
}

func (r recipesByAuthorStore) GetRecipesByAuthor(context.Context, string, string, int) ([]models.Recipe, int64, error) {
	return r.recipes, int64(len(r.recipes)), nil
}

func TestSourceHashIgnoresPictures(t *testing.T) {
	base := validRecipeDB(nil)
	base.Ingredients = []models.Ingredient{{Name: "Flour"}}
	withPictures := base
	withPictures.Pictures = []models.RecipePicture{{Filename: "a.jpg"}, {Filename: "b.jpg", AddedBy: ptrObjectID()}}

	if sourceHash(base) != sourceHash(withPictures) {
		t.Fatal("sourceHash differs based on Pictures alone, want it to depend only on translatable content")
	}
}

func TestRegroupPicturesKeepsAuthorFirstAndPreservesRelativeOrder(t *testing.T) {
	contributorA, contributorB := ptrObjectID(), ptrObjectID()
	pictures := []models.RecipePicture{
		{Filename: "contrib-a-1.jpg", AddedBy: contributorA},
		{Filename: "author-1.jpg"},
		{Filename: "contrib-b-1.jpg", AddedBy: contributorB},
		{Filename: "author-2.jpg"},
	}

	got := regroupPictures(pictures)
	want := []string{"author-1.jpg", "author-2.jpg", "contrib-a-1.jpg", "contrib-b-1.jpg"}
	if len(got) != len(want) {
		t.Fatalf("regroupPictures = %+v, want %d entries", got, len(want))
	}
	for i, filename := range want {
		if got[i].Filename != filename {
			t.Fatalf("regroupPictures[%d] = %q, want %q (full: %+v)", i, got[i].Filename, filename, got)
		}
	}
}

func recipeWithAuthor(authorID *primitive.ObjectID) models.Recipe {
	recipe := canonicalRecipe()
	recipe.Author = &models.UserView{Id: authorID}
	return recipe
}

func TestAddPictureAuthorHasNoCap(t *testing.T) {
	dir := t.TempDir()
	authorID := ptrObjectID()
	recipe := recipeWithAuthor(authorID)
	for i := 0; i < PictureCapPerContributor+2; i++ {
		recipe.Pictures = append(recipe.Pictures, models.RecipePicture{Filename: "author.jpg"})
	}
	var stored models.RecipeDB
	store := &fakeStore{replaceRecipeByIdFn: func(_ string, r models.RecipeDB) (models.Recipe, error) {
		stored = r
		return models.Recipe{Id: recipe.Id, Pictures: r.Pictures}, nil
	}}
	s := &Service{db: store, imageDir: dir, maxPictureBytes: 10 << 20}

	if _, err := s.AddPicture(context.Background(), recipe, authorID.Hex(), pictureUpload(t)); err != nil {
		t.Fatalf("AddPicture: %v", err)
	}
	last := stored.Pictures[len(stored.Pictures)-1]
	if last.AddedBy != nil {
		t.Fatalf("new author picture AddedBy = %v, want nil", last.AddedBy)
	}
}

func TestAddPictureEnforcesContributorCap(t *testing.T) {
	authorID := ptrObjectID()
	contributorID := ptrObjectID()
	recipe := recipeWithAuthor(authorID)
	for i := 0; i < PictureCapPerContributor; i++ {
		recipe.Pictures = append(recipe.Pictures, models.RecipePicture{Filename: "contrib.jpg", AddedBy: contributorID})
	}
	s := &Service{db: &fakeStore{}, imageDir: t.TempDir(), maxPictureBytes: 10 << 20}

	_, err := s.AddPicture(context.Background(), recipe, contributorID.Hex(), pictureUpload(t))
	if !errors.Is(err, ErrPictureCapReached) {
		t.Fatalf("AddPicture err = %v, want ErrPictureCapReached", err)
	}
}

func TestAddPictureAttributesContributorAndKeepsAuthorGroupFirst(t *testing.T) {
	dir := t.TempDir()
	authorID := ptrObjectID()
	contributorID := ptrObjectID()
	recipe := recipeWithAuthor(authorID)
	recipe.Pictures = []models.RecipePicture{{Filename: "author-1.jpg"}}
	var stored models.RecipeDB
	store := &fakeStore{replaceRecipeByIdFn: func(_ string, r models.RecipeDB) (models.Recipe, error) {
		stored = r
		return models.Recipe{Id: recipe.Id, Pictures: r.Pictures}, nil
	}}
	s := &Service{db: store, imageDir: dir, maxPictureBytes: 10 << 20}

	if _, err := s.AddPicture(context.Background(), recipe, contributorID.Hex(), pictureUpload(t)); err != nil {
		t.Fatalf("AddPicture: %v", err)
	}
	if len(stored.Pictures) != 2 {
		t.Fatalf("stored.Pictures = %+v, want 2 entries", stored.Pictures)
	}
	if stored.Pictures[0].Filename != "author-1.jpg" || stored.Pictures[0].AddedBy != nil {
		t.Fatalf("stored.Pictures[0] = %+v, want the untouched author picture first", stored.Pictures[0])
	}
	if stored.Pictures[1].AddedBy == nil || stored.Pictures[1].AddedBy.Hex() != contributorID.Hex() {
		t.Fatalf("stored.Pictures[1].AddedBy = %v, want %v", stored.Pictures[1].AddedBy, contributorID)
	}
}

func TestRemovePictureNotFound(t *testing.T) {
	recipe := recipeWithAuthor(ptrObjectID())
	s := &Service{db: &fakeStore{}}

	_, err := s.RemovePicture(context.Background(), recipe, "missing.jpg", "someone", false)
	if !errors.Is(err, ErrPictureNotFound) {
		t.Fatalf("RemovePicture err = %v, want ErrPictureNotFound", err)
	}
}

func TestRemovePicturePermissions(t *testing.T) {
	authorID := ptrObjectID()
	contributorID := ptrObjectID()
	otherID := ptrObjectID()

	for _, tc := range []struct {
		name        string
		callerID    string
		callerAdmin bool
		wantErr     error
	}{
		{"contributor removes their own picture", contributorID.Hex(), false, nil},
		{"author removes the contributor's picture", authorID.Hex(), false, nil},
		{"admin removes the contributor's picture", otherID.Hex(), true, nil},
		{"unrelated user is forbidden", otherID.Hex(), false, ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recipe := recipeWithAuthor(authorID)
			recipe.Pictures = []models.RecipePicture{
				{Filename: "author.jpg"},
				{Filename: "contrib.jpg", AddedBy: contributorID},
			}
			store := &fakeStore{replaceRecipeByIdFn: func(_ string, r models.RecipeDB) (models.Recipe, error) {
				return models.Recipe{Id: recipe.Id, Pictures: r.Pictures}, nil
			}}
			s := &Service{db: store, imageDir: t.TempDir()}

			_, err := s.RemovePicture(context.Background(), recipe, "contrib.jpg", tc.callerID, tc.callerAdmin)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("RemovePicture err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestMergePatchPreservesContributorPictureWithoutFullAccess(t *testing.T) {
	contributorID := ptrObjectID()
	recipe := recipeWithAuthor(ptrObjectID())
	recipe.Pictures = []models.RecipePicture{
		{Filename: "author.jpg"},
		{Filename: "contrib.jpg", AddedBy: contributorID},
	}
	// The author's own wizard never lists contributor filenames.
	patch := models.UpdateRecipeRequest{KeepPictureIDs: &[]string{"author.jpg"}}

	merged, err := mergePatch(recipe, patch, nil, false)
	if err != nil {
		t.Fatalf("mergePatch: %v", err)
	}
	if len(merged.Pictures) != 2 || merged.Pictures[1].Filename != "contrib.jpg" {
		t.Fatalf("merged.Pictures = %+v, want the contributor picture preserved", merged.Pictures)
	}
}

func TestMergePatchRejectsAuthorRemovingContributorPictureWithoutFullAccess(t *testing.T) {
	contributorID := ptrObjectID()
	recipe := recipeWithAuthor(ptrObjectID())
	recipe.Pictures = []models.RecipePicture{
		{Filename: "author.jpg"},
		{Filename: "contrib.jpg", AddedBy: contributorID},
	}
	patch := models.UpdateRecipeRequest{KeepPictureIDs: &[]string{"author.jpg", "contrib.jpg"}}

	_, err := mergePatch(recipe, patch, nil, false)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("mergePatch err = %v, want ErrInvalid", err)
	}
}

func TestMergePatchFullAccessAllowsRemovingContributorPicture(t *testing.T) {
	contributorID := ptrObjectID()
	recipe := recipeWithAuthor(ptrObjectID())
	recipe.Pictures = []models.RecipePicture{
		{Filename: "author.jpg"},
		{Filename: "contrib.jpg", AddedBy: contributorID},
	}
	patch := models.UpdateRecipeRequest{KeepPictureIDs: &[]string{"author.jpg"}}

	merged, err := mergePatch(recipe, patch, nil, true)
	if err != nil {
		t.Fatalf("mergePatch: %v", err)
	}
	if len(merged.Pictures) != 1 || merged.Pictures[0].Filename != "author.jpg" {
		t.Fatalf("merged.Pictures = %+v, want only author.jpg (admin dropped the contributor picture)", merged.Pictures)
	}
}

func TestListUsesPerRecipeFavoritesForOwnRecipes(t *testing.T) {
	id := ptrObjectID()
	store := &fakeStore{
		getRecipeDocumentsFn: func(models.GetRecipesRequest) ([]models.Recipe, int64, error) {
			return []models.Recipe{{Id: id}}, 1, nil
		},
		getFavoriteInfoFn: func(ids []string, userID string) (map[string]models.FavoriteInfo, error) {
			return map[string]models.FavoriteInfo{id.Hex(): {Count: 1, Favorited: true}}, nil
		},
		getFamilyFavoriteInfoFn: func([]string, string) (map[string]models.FavoriteInfo, error) {
			t.Fatal("GetFamilyFavoriteInfo should not be called in OwnRecipes mode")
			return nil, nil
		},
	}
	s := &Service{db: store}

	previews, _, err := s.List(context.Background(), models.GetRecipesRequest{OwnRecipes: true}, "user-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(previews) != 1 || !previews[0].Favorite || previews[0].FavoriteCount != 1 {
		t.Fatalf("previews = %+v, want one favorited preview with count 1", previews)
	}
}

func TestListUsesPerRecipeFavoritesForVariationOf(t *testing.T) {
	id := ptrObjectID()
	store := &fakeStore{
		getRecipeDocumentsFn: func(models.GetRecipesRequest) ([]models.Recipe, int64, error) {
			return []models.Recipe{{Id: id}}, 1, nil
		},
		getFavoriteInfoFn: func(ids []string, userID string) (map[string]models.FavoriteInfo, error) {
			return map[string]models.FavoriteInfo{id.Hex(): {Count: 1, Favorited: true}}, nil
		},
		getFamilyFavoriteInfoFn: func([]string, string) (map[string]models.FavoriteInfo, error) {
			t.Fatal("GetFamilyFavoriteInfo should not be called when listing a family's variations - each one shows its own exact favorite status")
			return nil, nil
		},
	}
	s := &Service{db: store}

	previews, _, err := s.List(context.Background(), models.GetRecipesRequest{VariationOf: id.Hex()}, "user-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(previews) != 1 || !previews[0].Favorite || previews[0].FavoriteCount != 1 {
		t.Fatalf("previews = %+v, want one favorited preview with count 1", previews)
	}
}

func TestListUsesPerRecipeFavoritesForFlat(t *testing.T) {
	id := ptrObjectID()
	store := &fakeStore{
		getRecipeDocumentsFn: func(models.GetRecipesRequest) ([]models.Recipe, int64, error) {
			return []models.Recipe{{Id: id}}, 1, nil
		},
		getFavoriteInfoFn: func(ids []string, userID string) (map[string]models.FavoriteInfo, error) {
			return map[string]models.FavoriteInfo{id.Hex(): {Count: 1, Favorited: true}}, nil
		},
		getFamilyFavoriteInfoFn: func([]string, string) (map[string]models.FavoriteInfo, error) {
			t.Fatal("GetFamilyFavoriteInfo should not be called on the flat listing")
			return nil, nil
		},
	}
	s := &Service{db: store}

	previews, _, err := s.List(context.Background(), models.GetRecipesRequest{Flat: true}, "user-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(previews) != 1 || !previews[0].Favorite || previews[0].FavoriteCount != 1 {
		t.Fatalf("previews = %+v, want one favorited preview with count 1", previews)
	}
}

func TestListStampsFamilySize(t *testing.T) {
	store := &fakeStore{}
	a, b, standalone := ptrObjectID(), ptrObjectID(), ptrObjectID()
	store.addFamily(a.Hex(), a.Hex(), b.Hex(), ptrObjectID().Hex())
	store.addFamily(standalone.Hex(), standalone.Hex())
	store.getRecipeDocumentsFn = func(models.GetRecipesRequest) ([]models.Recipe, int64, error) {
		return []models.Recipe{{Id: a, FamilyID: a}, {Id: b, FamilyID: a}, {Id: standalone, FamilyID: standalone}}, 3, nil
	}
	s := &Service{db: store}

	previews, _, err := s.List(context.Background(), models.GetRecipesRequest{Flat: true}, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := []int64{previews[0].FamilySize, previews[1].FamilySize, previews[2].FamilySize}
	if !reflect.DeepEqual(got, []int64{3, 3, 1}) {
		t.Fatalf("family sizes = %v, want [3 3 1]", got)
	}
	want := [][]string{{a.Hex(), standalone.Hex()}}
	if !reflect.DeepEqual(store.familySizeCalls, want) {
		t.Fatalf("GetFamilySizes calls = %v, want %v (deduplicated family ids)", store.familySizeCalls, want)
	}
}
