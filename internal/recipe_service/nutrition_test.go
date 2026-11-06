package recipe_service

import (
	"context"
	"errors"
	"testing"

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

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link.GPer100ml, link.GramsPerUnit, []models.ToolboxUnit{gram})
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

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link.GPer100ml, link.GramsPerUnit, []models.ToolboxUnit{cup})
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

	_, ok := s.resolveIngredientGrams(context.Background(), ingredient, link.GPer100ml, link.GramsPerUnit, []models.ToolboxUnit{cup})
	if ok {
		t.Fatal("got ok=true, want false: no density available for a volume unit")
	}
}

func TestResolveIngredientGramsBlankUnitUsesGramsPerUnit(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	gpu := 50.0
	ingredient := models.Ingredient{Name: "Egg", Quantity: 3, Unit: ""}
	link := models.IngredientNutritionLink{Name: "Egg", GramsPerUnit: &gpu}

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link.GPer100ml, link.GramsPerUnit, nil)
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

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link.GPer100ml, link.GramsPerUnit, []models.ToolboxUnit{gram})
	if !ok || grams != 100 {
		t.Fatalf("got grams=%v ok=%v, want 100/true", grams, ok)
	}
}

func TestResolveIngredientGramsCountWithoutGramsPerUnitIsUnresolved(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	ingredient := models.Ingredient{Name: "Egg", Quantity: 3, Unit: ""}
	link := models.IngredientNutritionLink{Name: "Egg"}

	_, ok := s.resolveIngredientGrams(context.Background(), ingredient, link.GPer100ml, link.GramsPerUnit, nil)
	if ok {
		t.Fatal("got ok=true, want false: no GramsPerUnit supplied for a count-based ingredient")
	}
}

func TestResolveIngredientGramsNoQuantityIsUnresolved(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	ingredient := models.Ingredient{Name: "Salt", Quantity: 0, Unit: "g"}
	link := models.IngredientNutritionLink{Name: "Salt"}

	_, ok := s.resolveIngredientGrams(context.Background(), ingredient, link.GPer100ml, link.GramsPerUnit, []models.ToolboxUnit{gramUnit()})
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

	grams, ok := s.resolveIngredientGrams(context.Background(), ingredient, link.GPer100ml, link.GramsPerUnit, []models.ToolboxUnit{gram})
	if !ok || grams != 2 {
		t.Fatalf("got grams=%v ok=%v, want 2/true", grams, ok)
	}
}

func TestGetRecipeNutritionScalesByServingsAndReportsCoverage(t *testing.T) {
	gram := gramUnit()
	recipeID := primitive.NewObjectID()
	foodID := "flour"
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
				return models.IngredientNutritionLink{Name: "Flour", FoodID: foodID}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
		listFoodsFn: func() ([]models.FoodItem, error) {
			return []models.FoodItem{{Id: foodID, KcalPer100g: 350, ProteinG: 10, CarbsG: 70, FatG: 1}}, nil
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
	foodID := "flour"
	created := false
	store := &fakeStore{
		listFoodsFn: func() ([]models.FoodItem, error) { return []models.FoodItem{{Id: foodID}}, nil },
		// default getIngredientNutritionLinkByNameLowerFn (unset) returns NotFound - a fresh name.
	}
	store.createIngredientNutritionLinkFn = func(name string, nid string, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
		created = true
		id := primitive.NewObjectID()
		return models.IngredientNutritionLink{Id: &id, Name: name, FoodID: nid}, nil
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	gramsPerUnit := 50.0
	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Flour", FoodID: foodID, GramsPerUnit: &gramsPerUnit,
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
	foodID := "flour"
	existingLinkID := primitive.NewObjectID()
	store := &fakeStore{
		listFoodsFn: func() ([]models.FoodItem, error) { return []models.FoodItem{{Id: foodID}}, nil },
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
		IngredientName: "Flour", FoodID: foodID, GramsPerUnit: &gramsPerUnit,
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
	foodID := "flour"
	cup := cupUnit()
	store := &fakeStore{
		listFoodsFn:        func() ([]models.FoodItem, error) { return []models.FoodItem{{Id: foodID}}, nil },
		listToolboxUnitsFn: func() ([]models.ToolboxUnit, error) { return []models.ToolboxUnit{cup}, nil },
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	_, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Milk", IngredientUnit: "cup", FoodID: foodID,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("SubmitNutritionLinkSuggestion err = %v, want ErrInvalid (a volume unit needs a density)", err)
	}
}

func TestSubmitNutritionLinkSuggestionWeightUnitNeedsNeitherField(t *testing.T) {
	foodID := "flour"
	gram := gramUnit()
	store := &fakeStore{
		listFoodsFn:        func() ([]models.FoodItem, error) { return []models.FoodItem{{Id: foodID}}, nil },
		listToolboxUnitsFn: func() ([]models.ToolboxUnit, error) { return []models.ToolboxUnit{gram}, nil },
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Flour", IngredientUnit: "g", FoodID: foodID,
	})
	if err != nil {
		t.Fatalf("SubmitNutritionLinkSuggestion: %v", err)
	}
	if !result.Applied {
		t.Fatal("got Applied=false, want true (a weight unit needs neither density nor grams-per-unit)")
	}
}

func TestSubmitNutritionLinkSuggestionFillingAnEmptyFieldAppliesDirectly(t *testing.T) {
	foodID := "flour"
	existingLinkID := primitive.NewObjectID()
	existingGramsPerUnit := 90.0
	var gotGPer100ml, gotGramsPerUnit *float64
	updateCalled := false
	store := &fakeStore{
		listFoodsFn: func() ([]models.FoodItem, error) { return []models.FoodItem{{Id: foodID}}, nil },
		getIngredientNutritionLinkByNameLowerFn: func(nameLower string) (models.IngredientNutritionLink, error) {
			if nameLower == "crème fraîche" {
				return models.IngredientNutritionLink{Id: &existingLinkID, Name: "Crème fraîche", FoodID: foodID, GramsPerUnit: &existingGramsPerUnit}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
		updateIngredientNutritionLinkFn: func(id, name string, nid string, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
			updateCalled = true
			gotGPer100ml, gotGramsPerUnit = gPer100ml, gramsPerUnit
			return models.IngredientNutritionLink{Id: &existingLinkID, Name: name, FoodID: nid, GPer100ml: gPer100ml, GramsPerUnit: gramsPerUnit}, nil
		},
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	newDensity := 100.0
	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Crème fraîche", FoodID: foodID, GPer100ml: &newDensity,
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
	foodID := "flour"
	existingLinkID := primitive.NewObjectID()
	existingDensity := 92.0
	existingGramsPerUnit := 15.0
	store := &fakeStore{
		listFoodsFn: func() ([]models.FoodItem, error) { return []models.FoodItem{{Id: foodID}}, nil },
		getIngredientNutritionLinkByNameLowerFn: func(nameLower string) (models.IngredientNutritionLink, error) {
			if nameLower == "olive oil" {
				return models.IngredientNutritionLink{Id: &existingLinkID, Name: "Olive oil", FoodID: foodID, GPer100ml: &existingDensity, GramsPerUnit: &existingGramsPerUnit}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
		createIngredientNutritionLinkFn: func(string, string, *float64, *float64) (models.IngredientNutritionLink, error) {
			t.Fatal("CreateIngredientNutritionLink should not be called when the submission needs review")
			return models.IngredientNutritionLink{}, nil
		},
		updateIngredientNutritionLinkFn: func(string, string, string, *float64, *float64) (models.IngredientNutritionLink, error) {
			t.Fatal("UpdateIngredientNutritionLink should not be called when the submission needs review")
			return models.IngredientNutritionLink{}, nil
		},
	}
	s := &Service{db: store}
	userID := primitive.NewObjectID().Hex()

	newDensity := 105.0
	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), userID, models.SubmitNutritionLinkRequest{
		IngredientName: "Olive oil", FoodID: foodID, GPer100ml: &newDensity,
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
// food at 400 kcal/100g.
func sugarLinkedStore(recipesByID map[string]models.Recipe) *fakeStore {
	gram := gramUnit()

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
				return models.IngredientNutritionLink{Name: "Sugar", FoodID: "sugar"}, nil
			}
			return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
		},
		listFoodsFn: func() ([]models.FoodItem, error) {
			return []models.FoodItem{{Id: "sugar", KcalPer100g: 400}}, nil
		},
	}
}

func TestGetRecipeNutritionIgnoresRecipeRef(t *testing.T) {
	subID := primitive.NewObjectID()
	parentID := primitive.NewObjectID()
	sub := models.Recipe{Id: &subID, Quantity: 1, Ingredients: []models.Ingredient{{Name: "Sugar", Quantity: 100, Unit: "g"}}}
	parent := models.Recipe{
		Id: &parentID, Quantity: 4,
		Ingredients: []models.Ingredient{
			{Name: "Sugar", Quantity: 50, Unit: "g"},
			{RecipeRef: &subID, Quantity: 2},
		},
	}
	store := sugarLinkedStore(map[string]models.Recipe{subID.Hex(): sub, parentID.Hex(): parent})
	s := &Service{db: store}

	got, err := s.GetRecipeNutrition(context.Background(), parentID.Hex(), 4)
	if err != nil {
		t.Fatalf("GetRecipeNutrition: %v", err)
	}
	// Only the 50g of sugar counts: 200 kcal. The sub-recipe contributes nothing.
	if got.Kcal != 200 || got.MatchedCount != 1 || got.TotalCount != 2 {
		t.Fatalf("got %+v, want kcal=200 matched=1 total=2", got)
	}
	if len(got.Ingredients) != 2 || got.Ingredients[1].Matched || got.Ingredients[1].Kcal != 0 {
		t.Fatalf("got ingredient lines %+v, want the recipe_ref line unmatched with kcal=0", got.Ingredients)
	}
}

func ptr(v float64) *float64 { return &v }

// foodStore returns a fakeStore with gram and cup units, recipe, and two
// foods: egg (50 g a piece, aliases "eggs"/"oeufs") and honey (141 g/100ml).
func foodStore(recipe models.Recipe) *fakeStore {
	gram, cup := gramUnit(), cupUnit()
	return &fakeStore{
		getRecipeByIdFn:    func(string) (models.Recipe, error) { return recipe, nil },
		listToolboxUnitsFn: func() ([]models.ToolboxUnit, error) { return []models.ToolboxUnit{gram, cup}, nil },
		listFoodsFn: func() ([]models.FoodItem, error) {
			return []models.FoodItem{
				{Id: "egg", Names: map[string]string{"en": "Egg", "fr": "Œuf"}, Aliases: map[string][]string{"en": {"eggs"}, "fr": {"oeufs"}},
					GramsPerPiece: ptr(50), KcalPer100g: 140},
				{Id: "honey", Names: map[string]string{"en": "Honey", "fr": "Miel"}, GPer100ml: ptr(141), KcalPer100g: 300},
			}, nil
		},
	}
}

func recipeWith(locale string, ingredients ...models.Ingredient) models.Recipe {
	id := primitive.NewObjectID()
	return models.Recipe{Id: &id, Quantity: 1, SourceLocale: locale, Ingredients: ingredients}
}

func TestRecipeNutritionAutoMatchesFoodByAliasWithItsDefaultGramsPerPiece(t *testing.T) {
	recipe := recipeWith("en", models.Ingredient{Name: "Eggs ", Quantity: 2})
	s := &Service{db: foodStore(recipe)}

	got, err := s.GetRecipeNutrition(context.Background(), recipe.Id.Hex(), 0)
	if err != nil {
		t.Fatal(err)
	}
	// 2 x 50 g at 140 kcal/100g.
	if got.MatchedCount != 1 || got.Kcal != 140 {
		t.Fatalf("got %+v, want matched=1 kcal=140", got)
	}
}

func TestRecipeNutritionVolumeUsesFoodDensity(t *testing.T) {
	recipe := recipeWith("fr", models.Ingredient{Name: "miel", Quantity: 1, Unit: "cup"})
	s := &Service{db: foodStore(recipe)}

	got, err := s.GetRecipeNutrition(context.Background(), recipe.Id.Hex(), 0)
	if err != nil {
		t.Fatal(err)
	}
	// 240 ml x 1.41 g/ml = 338.4 g at 300 kcal/100g.
	if got.Kcal != 1015.2 {
		t.Fatalf("got kcal=%v, want 1015.2", got.Kcal)
	}
}

func TestRecipeNutritionLinkOverrideBeatsFoodDefault(t *testing.T) {
	recipe := recipeWith("en", models.Ingredient{Name: "Big egg", Quantity: 1})
	store := foodStore(recipe)
	store.getIngredientNutritionLinkByNameLowerFn = func(nameLower string) (models.IngredientNutritionLink, error) {
		if nameLower == "big egg" {
			return models.IngredientNutritionLink{Name: "Big egg", FoodID: "egg", GramsPerUnit: ptr(100)}, nil
		}
		return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
	}
	s := &Service{db: store}

	got, err := s.GetRecipeNutrition(context.Background(), recipe.Id.Hex(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kcal != 140 {
		t.Fatalf("got kcal=%v, want 140 (the link's 100 g a piece)", got.Kcal)
	}
}

func TestRecipeNutritionIgnoresNamesFromOtherLocales(t *testing.T) {
	recipe := recipeWith("fr", models.Ingredient{Name: "eggs", Quantity: 2})
	s := &Service{db: foodStore(recipe)}

	got, err := s.GetRecipeNutrition(context.Background(), recipe.Id.Hex(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchedCount != 0 {
		t.Fatalf("got matched=%d, want 0: \"eggs\" is English, the recipe French", got.MatchedCount)
	}
}

func TestSubmitNutritionLinkNeedsNoNumberWhenFoodHasDefault(t *testing.T) {
	store := foodStore(models.Recipe{})
	s := &Service{db: store}

	result, err := s.SubmitNutritionLinkSuggestion(context.Background(), primitive.NewObjectID().Hex(), models.SubmitNutritionLinkRequest{
		IngredientName: "Gros oeuf", FoodID: "egg",
	})
	if err != nil {
		t.Fatalf("SubmitNutritionLinkSuggestion: %v", err)
	}
	if !result.Applied {
		t.Fatalf("got %+v, want applied", result)
	}
}

func TestSubmitNutritionLinkRejectsUnknownFood(t *testing.T) {
	s := &Service{db: foodStore(models.Recipe{})}

	_, err := s.SubmitNutritionLinkSuggestion(context.Background(), primitive.NewObjectID().Hex(), models.SubmitNutritionLinkRequest{
		IngredientName: "Thing", FoodID: "nope", GramsPerUnit: ptr(1),
	})
	if !errors.Is(err, ErrFoodNotFound) {
		t.Fatalf("err = %v, want ErrFoodNotFound", err)
	}
}
