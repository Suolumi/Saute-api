package tests

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
