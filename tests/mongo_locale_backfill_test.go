package tests

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/config"
	"recipes/internal/database"
	mongorepo "recipes/internal/database/mongo"
)

func newLocaleTestDB(t *testing.T) (database.Database, context.Context) {
	t.Helper()
	db, err := mongorepo.New(&config.DatabaseConfig{
		DefaultAddr: "mongodb://localhost:27017",
		Name:        "Recipes_test_" + primitive.NewObjectID().Hex(),
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Skipf("local MongoDB is not available: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = db.RawDatabase().Drop(context.Background())
		_ = db.Close(context.Background())
	})
	require.NoError(t, db.EnsureIndexes(ctx))
	return db, ctx
}

func TestSeedNutritionDefaultsAddsUnitAliasesOnce(t *testing.T) {
	db, ctx := newLocaleTestDB(t)
	require.NoError(t, db.SeedToolboxDefaults(ctx))
	require.NoError(t, db.SeedNutritionDefaults(ctx))
	require.NoError(t, db.SeedNutritionDefaults(ctx))

	aliases, err := db.ListUnitAliases(ctx)
	require.NoError(t, err)
	byAlias := map[string]primitive.ObjectID{}
	for _, alias := range aliases {
		byAlias[alias.Alias] = alias.UnitID
	}
	gram, err := db.GetToolboxUnitByNameLower(ctx, "gram")
	require.NoError(t, err)
	assert.Equal(t, *gram.Id, byAlias["克"])
	assert.Len(t, aliases, len(byAlias), "re-seeding must not duplicate aliases")
}

func TestListRecipeTranslationStates(t *testing.T) {
	db, ctx := newLocaleTestDB(t)
	recipeID := primitive.NewObjectID()
	_, err := db.RawDatabase().Collection("recipes").InsertMany(ctx, []interface{}{
		bson.M{"_id": recipeID, "title": "Pancakes", "source_locale": "en", "source_hash": "v2"},
		bson.M{"_id": primitive.NewObjectID(), "title": "Unhashed"},
	})
	require.NoError(t, err)
	_, err = db.RawDatabase().Collection("recipe_translations").InsertMany(ctx, []interface{}{
		bson.M{"recipe_id": recipeID, "locale": "fr", "source_hash": "v2"},
		bson.M{"recipe_id": recipeID, "locale": "fi", "source_hash": "v1"},
	})
	require.NoError(t, err)

	states, err := db.ListRecipeTranslationStates(ctx)
	require.NoError(t, err)
	require.Len(t, states, 1)
	assert.Equal(t, recipeID, states[0].ID)
	assert.Equal(t, "en", states[0].SourceLocale)
	assert.Equal(t, map[string]string{"fr": "v2", "fi": "v1"}, states[0].Translations)
}
