package recipe_service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

func gramUnit() models.ToolboxUnit {
	id := primitive.NewObjectID()
	return models.ToolboxUnit{Id: &id, Name: "Gram", NameLower: "gram", Symbol: "g", Kind: models.ToolboxUnitWeight, ToBase: 1}
}

func cupUnit() models.ToolboxUnit {
	id := primitive.NewObjectID()
	return models.ToolboxUnit{Id: &id, Name: "Cup", NameLower: "cup", Symbol: "cup", Kind: models.ToolboxUnitVolume, ToBase: 240}
}

func TestResolveIngredientGramsWeightUnitNeedsNoDensity(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	gram := gramUnit()
	ingredient := models.Ingredient{Name: "Flour", Quantity: 200, Unit: "g"}
	link := models.IngredientNutritionLink{Name: "Flour"}

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, []models.ToolboxUnit{gram}, map[string]float64{})
	if !ok || grams != 200 {
		t.Fatalf("got grams=%v ok=%v, want 200/true", grams, ok)
	}
}

func TestResolveIngredientGramsVolumeUnitUsesLinkDensity(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	cup := cupUnit()
	density := 103.0
	ingredient := models.Ingredient{Name: "Milk", Quantity: 1, Unit: "cup"}
	link := models.IngredientNutritionLink{Name: "Milk", GPer100ml: &density}

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, []models.ToolboxUnit{cup}, map[string]float64{})
	want := 1 * 240 * 103.0 / 100
	if !ok || grams != want {
		t.Fatalf("got grams=%v ok=%v, want %v/true", grams, ok, want)
	}
}

func TestResolveIngredientGramsVolumeUnitFallsBackToToolboxDensity(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	cup := cupUnit()
	ingredient := models.Ingredient{Name: "Milk", Quantity: 1, Unit: "cup"}
	link := models.IngredientNutritionLink{Name: "Milk"} // no density of its own

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, []models.ToolboxUnit{cup}, map[string]float64{"milk": 103})
	want := 1 * 240 * 103.0 / 100
	if !ok || grams != want {
		t.Fatalf("got grams=%v ok=%v, want %v/true", grams, ok, want)
	}
}

func TestResolveIngredientGramsVolumeUnitWithNoDensityAnywhereIsUnresolved(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	cup := cupUnit()
	ingredient := models.Ingredient{Name: "Milk", Quantity: 1, Unit: "cup"}
	link := models.IngredientNutritionLink{Name: "Milk"}

	_, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, []models.ToolboxUnit{cup}, map[string]float64{})
	if ok {
		t.Fatal("got ok=true, want false: no density available for a volume unit")
	}
}

func TestResolveIngredientGramsBlankUnitUsesGramsPerUnit(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	gpu := 50.0
	ingredient := models.Ingredient{Name: "Egg", Quantity: 3, Unit: ""}
	link := models.IngredientNutritionLink{Name: "Egg", GramsPerUnit: &gpu}

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, nil, map[string]float64{})
	if !ok || grams != 150 {
		t.Fatalf("got grams=%v ok=%v, want 150/true", grams, ok)
	}
}

func TestResolveIngredientGramsUnrecognizedUnitFallsBackToCount(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	gram := gramUnit()
	gpu := 50.0
	ingredient := models.Ingredient{Name: "Egg", Quantity: 2, Unit: "whole"} // "whole" matches no ToolboxUnit/alias
	link := models.IngredientNutritionLink{Name: "Egg", GramsPerUnit: &gpu}

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, []models.ToolboxUnit{gram}, map[string]float64{})
	if !ok || grams != 100 {
		t.Fatalf("got grams=%v ok=%v, want 100/true", grams, ok)
	}
}

func TestResolveIngredientGramsCountWithoutGramsPerUnitIsUnresolved(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	ingredient := models.Ingredient{Name: "Egg", Quantity: 3, Unit: ""}
	link := models.IngredientNutritionLink{Name: "Egg"}

	_, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, nil, map[string]float64{})
	if ok {
		t.Fatal("got ok=true, want false: no GramsPerUnit supplied for a count-based ingredient")
	}
}

func TestResolveIngredientGramsNoQuantityIsUnresolved(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	ingredient := models.Ingredient{Name: "Salt", Quantity: 0, Unit: "g"}
	link := models.IngredientNutritionLink{Name: "Salt"}

	_, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, []models.ToolboxUnit{gramUnit()}, map[string]float64{})
	if ok {
		t.Fatal("got ok=true, want false: no quantity at all ('salt to taste')")
	}
}

func TestResolveIngredientGramsUnitResolvesViaAlias(t *testing.T) {
	gram := gramUnit()
	store := &fakeStore{
		getUnitAliasByAliasLowerFn: func(aliasLower string) (models.UnitAlias, error) {
			if aliasLower == "cuillère" {
				return models.UnitAlias{UnitID: *gram.Id}, nil
			}
			return models.UnitAlias{}, mongorepo.NutritionEntryNotFoundError
		},
	}
	s := &Service{db: store}
	ingredient := models.Ingredient{Name: "Sugar", Quantity: 2, Unit: "cuillère"}
	link := models.IngredientNutritionLink{Name: "Sugar"}

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link, []models.ToolboxUnit{gram}, map[string]float64{})
	if !ok || grams != 2 {
		t.Fatalf("got grams=%v ok=%v, want 2/true", grams, ok)
	}
}

func TestGetRecipeNutritionScalesByServingsAndReportsCoverage(t *testing.T) {
	gram := gramUnit()
	recipeID := primitive.NewObjectID()
	nutritionID := primitive.NewObjectID()
	recipe := models.Recipe{
		Id: &recipeID, Quantity: 4,
		Ingredients: []models.Ingredient{
			{Name: "Flour", Quantity: 200, Unit: "g"},
			{Name: "Salt", Quantity: 1, Unit: "pinch"}, // unlinked
		},
	}
	store := &fakeStore{
		getRecipeByIdFn:    func(string) (models.Recipe, error) { return recipe, nil },
		listToolboxUnitsFn: func() ([]models.ToolboxUnit, error) { return []models.ToolboxUnit{gram}, nil },
		getIngredientNutritionLinkByNameLowerFn: func(nameLower string) (models.IngredientNutritionLink, error) {
			if nameLower == "flour" {
				return models.IngredientNutritionLink{Name: "Flour", NutritionID: nutritionID}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
		getNutritionIngredientByIdFn: func(id string) (models.NutritionIngredient, error) {
			if id == nutritionID.Hex() {
				return models.NutritionIngredient{Id: &nutritionID, Name: "Wheat flour", KcalPer100g: 350, ProteinG: 10, CarbsG: 70, FatG: 1, SaltG: 0, SugarG: 0}, nil
			}
			return models.NutritionIngredient{}, errors.New("not found")
		},
	}
	s := &Service{db: store}

	got, err := s.GetRecipeNutrition(context.Background(), recipeID.Hex(), 8)
	if err != nil {
		t.Fatalf("GetRecipeNutrition: %v", err)
	}
	// 200g flour -> 700 kcal at base servings (4); doubled for 8 servings -> 1400.
	if got.Servings != 8 || got.Kcal != 1400 || got.MatchedCount != 1 || got.TotalCount != 2 {
		t.Fatalf("got %+v, want servings=8 kcal=1400 matched=1 total=2", got)
	}
	if len(got.Ingredients) != 2 {
		t.Fatalf("got %d ingredient lines, want 2", len(got.Ingredients))
	}
	if !got.Ingredients[0].Matched || got.Ingredients[0].Kcal != 1400 {
		t.Fatalf("got flour line %+v, want matched/kcal=1400", got.Ingredients[0])
	}
	if got.Ingredients[1].Matched || got.Ingredients[1].Kcal != 0 {
		t.Fatalf("got salt line %+v, want unmatched/kcal=0", got.Ingredients[1])
	}
}

func TestGetRecipeNutritionDefaultsServingsToRecipeQuantity(t *testing.T) {
	recipeID := primitive.NewObjectID()
	recipe := models.Recipe{Id: &recipeID, Quantity: 4, Ingredients: []models.Ingredient{{Name: "Salt", Quantity: 1}}}
	store := &fakeStore{
		getRecipeByIdFn:    func(string) (models.Recipe, error) { return recipe, nil },
		listToolboxUnitsFn: func() ([]models.ToolboxUnit, error) { return nil, nil },
	}
	s := &Service{db: store}

	got, err := s.GetRecipeNutrition(context.Background(), recipeID.Hex(), 0)
	if err != nil {
		t.Fatalf("GetRecipeNutrition: %v", err)
	}
	if got.Servings != 4 {
		t.Fatalf("got servings=%d, want 4 (the recipe's own base servings)", got.Servings)
	}
}

func TestSubmitNutritionLinkSuggestionAppliesDirectlyForAFreshName(t *testing.T) {
	nutritionID := primitive.NewObjectID()
	created := false
	store := &fakeStore{
		getNutritionIngredientByIdFn: func(string) (models.NutritionIngredient, error) { return models.NutritionIngredient{}, nil },
		// default getIngredientNutritionLinkByNameLowerFn (unset) returns NotFound - a fresh name.
	}
	store.createIngredientNutritionLinkFn = func(name string, nid primitive.ObjectID, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
		created = true
		id := primitive.NewObjectID()
		return models.IngredientNutritionLink{Id: &id, Name: name, NutritionID: nid}, nil
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	gramsPerUnit := 50.0
	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Flour", NutritionID: nutritionID.Hex(), GramsPerUnit: &gramsPerUnit,
	})
	if err != nil {
		t.Fatalf("SubmitNutritionLinkSuggestion: %v", err)
	}
	if !created {
		t.Fatal("CreateIngredientNutritionLink was not called - a fresh name should apply directly, no suggestion")
	}
	if !result.Applied || result.Link == nil || result.Suggestion != nil {
		t.Fatalf("got %+v, want Applied=true with Link set and no Suggestion", result)
	}
}

func TestSubmitNutritionLinkSuggestionBecomesCorrectionOnNameMatch(t *testing.T) {
	nutritionID := primitive.NewObjectID()
	existingLinkID := primitive.NewObjectID()
	store := &fakeStore{
		getNutritionIngredientByIdFn: func(string) (models.NutritionIngredient, error) { return models.NutritionIngredient{}, nil },
		getIngredientNutritionLinkByNameLowerFn: func(nameLower string) (models.IngredientNutritionLink, error) {
			if nameLower == "flour" {
				return models.IngredientNutritionLink{Id: &existingLinkID, Name: "Flour"}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	gramsPerUnit := 50.0
	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Flour", NutritionID: nutritionID.Hex(), GramsPerUnit: &gramsPerUnit,
	})
	if err != nil {
		t.Fatalf("SubmitNutritionLinkSuggestion: %v", err)
	}
	if result.Applied || result.Suggestion == nil {
		t.Fatalf("got %+v, want Applied=false with Suggestion set (an already-linked name is a correction)", result)
	}
	if result.Suggestion.TargetID == nil || *result.Suggestion.TargetID != existingLinkID {
		t.Fatalf("got TargetID=%v, want %v (existing link matched by name)", result.Suggestion.TargetID, existingLinkID)
	}
}

func TestSubmitNutritionLinkSuggestionRejectsVolumeUnitWithoutDensity(t *testing.T) {
	nutritionID := primitive.NewObjectID()
	cup := cupUnit()
	store := &fakeStore{
		getNutritionIngredientByIdFn: func(string) (models.NutritionIngredient, error) { return models.NutritionIngredient{}, nil },
		listToolboxUnitsFn:           func() ([]models.ToolboxUnit, error) { return []models.ToolboxUnit{cup}, nil },
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	_, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Milk", IngredientUnit: "cup", NutritionID: nutritionID.Hex(),
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("SubmitNutritionLinkSuggestion err = %v, want ErrInvalid (a volume unit needs a density)", err)
	}
}

func TestSubmitNutritionLinkSuggestionWeightUnitNeedsNeitherField(t *testing.T) {
	nutritionID := primitive.NewObjectID()
	gram := gramUnit()
	store := &fakeStore{
		getNutritionIngredientByIdFn: func(string) (models.NutritionIngredient, error) { return models.NutritionIngredient{}, nil },
		listToolboxUnitsFn:           func() ([]models.ToolboxUnit, error) { return []models.ToolboxUnit{gram}, nil },
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Flour", IngredientUnit: "g", NutritionID: nutritionID.Hex(),
	})
	if err != nil {
		t.Fatalf("SubmitNutritionLinkSuggestion: %v", err)
	}
	if !result.Applied {
		t.Fatal("got Applied=false, want true (a weight unit needs neither density nor grams-per-unit)")
	}
}

func TestSubmitNutritionLinkSuggestionFillingAnEmptyFieldAppliesDirectly(t *testing.T) {
	nutritionID := primitive.NewObjectID()
	existingLinkID := primitive.NewObjectID()
	existingGramsPerUnit := 90.0
	var gotGPer100ml, gotGramsPerUnit *float64
	updateCalled := false
	store := &fakeStore{
		getNutritionIngredientByIdFn: func(string) (models.NutritionIngredient, error) { return models.NutritionIngredient{}, nil },
		getIngredientNutritionLinkByNameLowerFn: func(nameLower string) (models.IngredientNutritionLink, error) {
			if nameLower == "crème fraîche" {
				return models.IngredientNutritionLink{Id: &existingLinkID, Name: "Crème fraîche", NutritionID: nutritionID, GramsPerUnit: &existingGramsPerUnit}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
		updateIngredientNutritionLinkFn: func(id, name string, nid primitive.ObjectID, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
			updateCalled = true
			gotGPer100ml, gotGramsPerUnit = gPer100ml, gramsPerUnit
			return models.IngredientNutritionLink{Id: &existingLinkID, Name: name, NutritionID: nid, GPer100ml: gPer100ml, GramsPerUnit: gramsPerUnit}, nil
		},
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	newDensity := 100.0
	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Crème fraîche", NutritionID: nutritionID.Hex(), GPer100ml: &newDensity,
	})
	if err != nil {
		t.Fatalf("SubmitNutritionLinkSuggestion: %v", err)
	}
	if !result.Applied || result.Suggestion != nil {
		t.Fatalf("got %+v, want Applied=true with no Suggestion (filling a previously-empty density needs no review)", result)
	}
	if !updateCalled {
		t.Fatal("UpdateIngredientNutritionLink was not called")
	}
	if gotGPer100ml == nil || *gotGPer100ml != newDensity {
		t.Fatalf("got GPer100ml=%v, want %v", gotGPer100ml, newDensity)
	}
	if gotGramsPerUnit == nil || *gotGramsPerUnit != existingGramsPerUnit {
		t.Fatalf("got GramsPerUnit=%v, want %v (the existing value should be preserved, not wiped)", gotGramsPerUnit, existingGramsPerUnit)
	}
}

func TestSubmitNutritionLinkSuggestionOverwritingAnExistingFieldRequiresReview(t *testing.T) {
	nutritionID := primitive.NewObjectID()
	existingLinkID := primitive.NewObjectID()
	existingDensity := 92.0
	existingGramsPerUnit := 15.0
	store := &fakeStore{
		getNutritionIngredientByIdFn: func(string) (models.NutritionIngredient, error) { return models.NutritionIngredient{}, nil },
		getIngredientNutritionLinkByNameLowerFn: func(nameLower string) (models.IngredientNutritionLink, error) {
			if nameLower == "olive oil" {
				return models.IngredientNutritionLink{Id: &existingLinkID, Name: "Olive oil", NutritionID: nutritionID, GPer100ml: &existingDensity, GramsPerUnit: &existingGramsPerUnit}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
		createIngredientNutritionLinkFn: func(string, primitive.ObjectID, *float64, *float64) (models.IngredientNutritionLink, error) {
			t.Fatal("CreateIngredientNutritionLink should not be called when the submission needs review")
			return models.IngredientNutritionLink{}, nil
		},
		updateIngredientNutritionLinkFn: func(string, string, primitive.ObjectID, *float64, *float64) (models.IngredientNutritionLink, error) {
			t.Fatal("UpdateIngredientNutritionLink should not be called when the submission needs review")
			return models.IngredientNutritionLink{}, nil
		},
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	newDensity := 105.0
	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Olive oil", NutritionID: nutritionID.Hex(), GPer100ml: &newDensity,
	})
	if err != nil {
		t.Fatalf("SubmitNutritionLinkSuggestion: %v", err)
	}
	if result.Applied || result.Suggestion == nil {
		t.Fatalf("got %+v, want Applied=false with Suggestion set (overwriting a set density needs review)", result)
	}
	if result.Suggestion.GPer100ml == nil || *result.Suggestion.GPer100ml != newDensity {
		t.Fatalf("got Suggestion.GPer100ml=%v, want %v (the submitted value, not the old one)", result.Suggestion.GPer100ml, newDensity)
	}
	if result.Suggestion.TargetID == nil || *result.Suggestion.TargetID != existingLinkID {
		t.Fatalf("got TargetID=%v, want %v", result.Suggestion.TargetID, existingLinkID)
	}
}

// sugarLinkedStore returns a fakeStore where "Sugar" resolves to a linked
// NutritionIngredient at 400 kcal/100g - shared setup for the recipe_ref
// recursion tests below, which vary only in the recipe graph.
func sugarLinkedStore(recipesByID map[string]models.Recipe) *fakeStore {
	gram := gramUnit()
	sugarNutritionID := primitive.NewObjectID()
	return &fakeStore{
		getRecipeByIdFn: func(id string) (models.Recipe, error) {
			if r, ok := recipesByID[id]; ok {
				return r, nil
			}
			return models.Recipe{}, mongorepo.NotFoundError
		},
		listToolboxUnitsFn: func() ([]models.ToolboxUnit, error) { return []models.ToolboxUnit{gram}, nil },
		getIngredientNutritionLinkByNameLowerFn: func(nameLower string) (models.IngredientNutritionLink, error) {
			if nameLower == "sugar" {
				return models.IngredientNutritionLink{Name: "Sugar", NutritionID: sugarNutritionID}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
		getNutritionIngredientByIdFn: func(id string) (models.NutritionIngredient, error) {
			if id == sugarNutritionID.Hex() {
				return models.NutritionIngredient{Id: &sugarNutritionID, Name: "Sugar", KcalPer100g: 400}, nil
			}
			return models.NutritionIngredient{}, errors.New("not found")
		},
	}
}

func TestGetRecipeNutritionRecursesIntoRecipeRefScaledByBatches(t *testing.T) {
	subID := primitive.NewObjectID()
	parentID := primitive.NewObjectID()
	sub := models.Recipe{Id: &subID, Quantity: 1, Ingredients: []models.Ingredient{{Name: "Sugar", Quantity: 100, Unit: "g"}}}
	parent := models.Recipe{
		Id: &parentID, Quantity: 4,
		Ingredients: []models.Ingredient{{RecipeRef: &subID, Quantity: 2}}, // 2 batches
	}
	store := sugarLinkedStore(map[string]models.Recipe{subID.Hex(): sub, parentID.Hex(): parent})
	s := &Service{db: store}

	got, err := s.GetRecipeNutrition(context.Background(), parentID.Hex(), 4)
	if err != nil {
		t.Fatalf("GetRecipeNutrition: %v", err)
	}
	// sub-recipe: 100g sugar -> 400 kcal. 2 batches -> 800 kcal.
	if got.Kcal != 800 || got.MatchedCount != 1 || got.TotalCount != 1 {
		t.Fatalf("got %+v, want kcal=800 matched=1 total=1", got)
	}
	if len(got.Ingredients) != 1 || !got.Ingredients[0].Matched || got.Ingredients[0].Kcal != 800 {
		t.Fatalf("got ingredient line %+v, want matched/kcal=800", got.Ingredients)
	}
}

func TestGetRecipeNutritionRecipeRefBlankQuantityMeansOneBatch(t *testing.T) {
	subID := primitive.NewObjectID()
	parentID := primitive.NewObjectID()
	sub := models.Recipe{Id: &subID, Quantity: 1, Ingredients: []models.Ingredient{{Name: "Sugar", Quantity: 100, Unit: "g"}}}
	parent := models.Recipe{Id: &parentID, Quantity: 4, Ingredients: []models.Ingredient{{RecipeRef: &subID}}} // no quantity set
	store := sugarLinkedStore(map[string]models.Recipe{subID.Hex(): sub, parentID.Hex(): parent})
	s := &Service{db: store}

	got, err := s.GetRecipeNutrition(context.Background(), parentID.Hex(), 4)
	if err != nil {
		t.Fatalf("GetRecipeNutrition: %v", err)
	}
	if got.Kcal != 400 {
		t.Fatalf("got kcal=%v, want 400 (a blank quantity means one batch, the whole referenced recipe)", got.Kcal)
	}
}

func TestGetRecipeNutritionRecipeRefUnmatchedWhenSubRecipeHasNoMatches(t *testing.T) {
	subID := primitive.NewObjectID()
	parentID := primitive.NewObjectID()
	sub := models.Recipe{Id: &subID, Quantity: 1, Ingredients: []models.Ingredient{{Name: "Mystery Powder", Quantity: 1}}} // unlinkable
	parent := models.Recipe{Id: &parentID, Quantity: 4, Ingredients: []models.Ingredient{{RecipeRef: &subID, Quantity: 1}}}
	store := sugarLinkedStore(map[string]models.Recipe{subID.Hex(): sub, parentID.Hex(): parent})
	s := &Service{db: store}

	got, err := s.GetRecipeNutrition(context.Background(), parentID.Hex(), 4)
	if err != nil {
		t.Fatalf("GetRecipeNutrition: %v", err)
	}
	if got.MatchedCount != 0 || got.Kcal != 0 {
		t.Fatalf("got %+v, want matched=0 kcal=0: a reference to a recipe with nothing matched contributes nothing", got)
	}
}

func TestGetRecipeNutritionRecipeRefDirectSelfReferenceDoesNotRecurseForever(t *testing.T) {
	selfID := primitive.NewObjectID()
	self := models.Recipe{Id: &selfID, Quantity: 1, Ingredients: []models.Ingredient{{RecipeRef: &selfID, Quantity: 1}}}
	store := sugarLinkedStore(map[string]models.Recipe{selfID.Hex(): self})
	s := &Service{db: store}

	done := make(chan struct{})
	var got models.RecipeNutrition
	var err error
	go func() {
		got, err = s.GetRecipeNutrition(context.Background(), selfID.Hex(), 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("GetRecipeNutrition did not return - a self-reference recursed without terminating")
	}
	if err != nil {
		t.Fatalf("GetRecipeNutrition: %v", err)
	}
	if got.MatchedCount != 0 || got.Kcal != 0 {
		t.Fatalf("got %+v, want matched=0 kcal=0: a reference cycle is treated as unmatched", got)
	}
}

func TestGetRecipeNutritionRecipeRefIndirectCycleDoesNotRecurseForever(t *testing.T) {
	aID := primitive.NewObjectID()
	bID := primitive.NewObjectID()
	a := models.Recipe{Id: &aID, Quantity: 1, Ingredients: []models.Ingredient{{RecipeRef: &bID, Quantity: 1}}}
	b := models.Recipe{Id: &bID, Quantity: 1, Ingredients: []models.Ingredient{{RecipeRef: &aID, Quantity: 1}}}
	store := sugarLinkedStore(map[string]models.Recipe{aID.Hex(): a, bID.Hex(): b})
	s := &Service{db: store}

	done := make(chan struct{})
	var got models.RecipeNutrition
	var err error
	go func() {
		got, err = s.GetRecipeNutrition(context.Background(), aID.Hex(), 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("GetRecipeNutrition did not return - an A->B->A cycle recursed without terminating")
	}
	if err != nil {
		t.Fatalf("GetRecipeNutrition: %v", err)
	}
	if got.MatchedCount != 0 || got.Kcal != 0 {
		t.Fatalf("got %+v, want matched=0 kcal=0: an indirect reference cycle is treated as unmatched", got)
	}
}
