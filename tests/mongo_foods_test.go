package tests

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/models"
	"recipes/internal/recipe_service"
)

// TestMigrateFoodsRemapsCiqualLinks covers the one-time move off Ciqual: a
// link to an entry folded into a food points at that food, a link to a
// dropped entry is deleted, the replaced collections are dropped, and a
// second run changes nothing.
func TestMigrateFoodsRemapsCiqualLinks(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	raw := db.RawDatabase()

	egg, dish := primitive.NewObjectID(), primitive.NewObjectID()
	_, err := raw.Collection("nutrition_ingredients").InsertMany(ctx, []interface{}{
		bson.M{"_id": egg, "name": "Oeuf, cru", "name_lower": "oeuf, cru"},
		bson.M{"_id": dish, "name": "Plat cuisiné X", "name_lower": "plat cuisiné x"},
	})
	require.NoError(t, err)
	_, err = raw.Collection("toolbox_ingredients").InsertOne(ctx, bson.M{"name": "Honey", "name_lower": "honey", "g_per_100ml": 141.0})
	require.NoError(t, err)
	density := 105.0
	_, err = raw.Collection("ingredient_nutrition_links").InsertMany(ctx, []interface{}{
		bson.M{"name": "Big eggs", "name_lower": "big eggs", "nutrition_id": egg, "g_per_100ml": density, "created_at": time.Now()},
		bson.M{"name": "Lasagna", "name_lower": "lasagna", "nutrition_id": dish, "created_at": time.Now()},
	})
	require.NoError(t, err)

	require.NoError(t, db.MigrateFoods(ctx))

	link, err := db.GetIngredientNutritionLinkByNameLower(ctx, "big eggs")
	require.NoError(t, err)
	assert.Equal(t, "egg", link.FoodID)
	require.NotNil(t, link.GPer100ml, "overrides survive the remap")
	assert.Equal(t, density, *link.GPer100ml)
	stale, err := raw.Collection("ingredient_nutrition_links").CountDocuments(ctx, bson.M{"nutrition_id": bson.M{"$exists": true}})
	require.NoError(t, err)
	assert.Zero(t, stale)
	_, err = db.GetIngredientNutritionLinkByNameLower(ctx, "lasagna")
	assert.Error(t, err, "a link to a dropped Ciqual entry is deleted")

	names, err := raw.ListCollectionNames(ctx, bson.M{})
	require.NoError(t, err)
	assert.NotContains(t, names, "nutrition_ingredients")
	assert.NotContains(t, names, "toolbox_ingredients")

	foods, err := db.ListFoods(ctx)
	require.NoError(t, err)
	require.NoError(t, db.MigrateFoods(ctx))
	again, err := db.ListFoods(ctx)
	require.NoError(t, err)
	assert.Len(t, again, len(foods), "a second run inserts nothing")
	links, err := db.ListIngredientNutritionLinks(ctx)
	require.NoError(t, err)
	assert.Len(t, links, 1)
}

// TestAdminFoodAndUnitCRUD covers the admin writes against the real
// collections: unique food ids and unit names, and deletes refused while
// something points at the entry.
func TestAdminFoodAndUnitCRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	svc := newTestRecipeService(t, db)
	require.NoError(t, db.SeedToolboxDefaults(ctx))
	require.NoError(t, db.MigrateFoods(ctx))

	req := models.AdminFoodRequest{Id: "test-quince", Names: map[string]string{"en": "Test quince"}, KcalPer100g: 57}
	_, err := svc.AdminCreateFood(ctx, req)
	require.NoError(t, err)
	_, err = svc.AdminCreateFood(ctx, req)
	assert.ErrorIs(t, err, recipe_service.ErrFoodExists)

	density := 80.0
	req.GPer100ml = &density
	updated, err := svc.AdminUpdateFood(ctx, "test-quince", req)
	require.NoError(t, err)
	require.NotNil(t, updated.GPer100ml)

	_, err = svc.AdminCreateNutritionLink(ctx, models.AdminNutritionLinkRequest{IngredientName: "Quince jelly", FoodID: "test-quince"})
	require.NoError(t, err)
	assert.ErrorIs(t, svc.AdminDeleteFood(ctx, "test-quince"), recipe_service.ErrFoodInUse)

	unit, err := svc.AdminCreateUnit(ctx, models.AdminUnitRequest{Name: "Test pinch", Symbol: "tpinch", Kind: "weight", ToBase: 0.3})
	require.NoError(t, err)
	_, err = svc.AdminCreateUnit(ctx, models.AdminUnitRequest{Name: "test pinch", Symbol: "tp", Kind: "weight", ToBase: 0.3})
	assert.ErrorIs(t, err, recipe_service.ErrUnitExists)
	_, err = svc.AdminCreateUnitAlias(ctx, models.AdminUnitAliasRequest{Alias: "tpinches", UnitID: unit.Id.Hex()})
	require.NoError(t, err)
	assert.ErrorIs(t, svc.AdminDeleteUnit(ctx, unit.Id.Hex()), recipe_service.ErrUnitInUse)
}

// TestMigrateFoodsMovesSubstitutions covers moving Toolbox substitutions
// onto foods: a matching one lands on the food in its source locale and
// translations, an unmatched one is dropped, and a re-run adds nothing.
func TestMigrateFoodsMovesSubstitutions(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.MigrateFoods(ctx))
	_, err := db.RawDatabase().Collection("toolbox_substitutions").InsertMany(ctx, []interface{}{
		bson.M{"problem": "No buttermilk", "solution": "Milk with a spoon of lemon juice", "source_locale": "en",
			"translations": bson.M{"fr": bson.M{"problem": "Pas de babeurre", "solution": "Du lait avec une cuillère de jus de citron"}}},
		bson.M{"problem": "Too salty soup", "solution": "Add a potato"},
	})
	require.NoError(t, err)

	require.NoError(t, db.MigrateFoods(ctx))
	require.NoError(t, db.MigrateFoods(ctx))

	food, err := db.GetFoodById(ctx, "buttermilk")
	require.NoError(t, err)
	assert.Equal(t, []string{"Milk with a spoon of lemon juice"}, food.Substitutes["en"])
	assert.Equal(t, []string{"Du lait avec une cuillère de jus de citron"}, food.Substitutes["fr"])
	names, err := db.RawDatabase().ListCollectionNames(ctx, bson.M{"name": "toolbox_substitutions"})
	require.NoError(t, err)
	assert.Empty(t, names)
}
