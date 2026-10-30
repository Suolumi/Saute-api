package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"recipes/internal/models"
)

func TestGetRecipeDocumentsTagFilter(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "tag-author")
	ingredients := bson.A{bson.M{"name": "flour"}}

	veganRoot := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Vegan Root", "ingredients": ingredients, "kind": "dish",
		"tags": bson.A{"vegan", "vegetarian", "gluten-free"},
	})
	meatRoot := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Meat Root", "ingredients": ingredients, "kind": "dish",
	})
	veganVariation := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Vegan Twist", "variation_of": meatRoot, "ingredients": ingredients, "kind": "dish",
		"tags": bson.A{"vegan", "vegetarian"},
	})
	// gluten-free on a sibling: no single recipe of this family is both.
	splitRoot := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Split Root", "ingredients": ingredients, "kind": "dish", "tags": bson.A{"vegan"},
	})
	insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Split GF", "variation_of": splitRoot, "ingredients": ingredients, "kind": "dish",
		"tags": bson.A{"gluten-free"},
	})

	ids := func(recipes []models.Recipe) []string {
		out := make([]string, len(recipes))
		for i, recipe := range recipes {
			out[i] = recipe.Id.Hex()
		}
		return out
	}

	vegan, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Tags: []string{"vegan"}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{veganRoot.Hex(), meatRoot.Hex(), splitRoot.Hex()}, ids(vegan))
	for _, recipe := range vegan {
		switch recipe.Id.Hex() {
		case meatRoot.Hex():
			require.Len(t, recipe.TagMatches, 1, "only a variation matches, so the card gets a hint")
			assert.Equal(t, veganVariation.Hex(), recipe.TagMatches[0].Id.Hex())
			assert.Equal(t, "Vegan Twist", recipe.TagMatches[0].Title)
		default:
			assert.Empty(t, recipe.TagMatches, "the root itself matches")
		}
	}

	both, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Tags: []string{"vegan", "gluten-free"}})
	require.NoError(t, err)
	assert.Equal(t, []string{veganRoot.Hex()}, ids(both), "one recipe must carry every tag")

	flat, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true, Author: "tag-author", Tags: []string{"vegetarian"}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{veganRoot.Hex(), veganVariation.Hex()}, ids(flat))

	injected, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Tags: []string{"$title"}})
	require.NoError(t, err)
	assert.Empty(t, injected, "a tag value is a literal, never a field path")
}

func TestRecipeTagStore(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "tag-store-author")
	ingredients := bson.A{bson.M{"name": "flour"}}

	tag := models.RecipeTag{
		Slug: "vegan", Group: models.TagGroupDiet, Categories: []models.RecipeCategory{models.Food},
		Implies: []string{}, Labels: map[string]string{"en": "Vegan"}, SourceLocale: "en",
	}
	require.NoError(t, db.CreateRecipeTag(ctx, tag))
	require.Error(t, db.CreateRecipeTag(ctx, tag))

	insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Legacy", "ingredients": ingredients, "tags": bson.A{"vegan"}})
	insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "DIY", "category": "diy", "ingredients": ingredients, "tags": bson.A{"vegan"}})

	all, err := db.CountRecipesWithTag(ctx, "vegan", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2), all)
	food, err := db.CountRecipesWithTag(ctx, "vegan", []models.RecipeCategory{models.Food})
	require.NoError(t, err)
	assert.Equal(t, int64(1), food, "a recipe with no category counts as food")

	modified, err := db.AddTagsToRecipesWithTag(ctx, "vegan", []string{"vegetarian"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), modified)
	count, err := db.CountRecipesWithTag(ctx, "vegetarian", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	require.NoError(t, db.FillRecipeTagLabel(ctx, "vegan", "en", "Vegan", "fr", "Végan"))
	require.NoError(t, db.FillRecipeTagLabel(ctx, "vegan", "en", "Vegan", "fr", "Overwritten"))
	require.NoError(t, db.FillRecipeTagLabel(ctx, "vegan", "en", "Old source", "fi", "Vegaani"))
	stored, err := db.GetRecipeTag(ctx, "vegan")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"en": "Vegan", "fr": "Végan"}, stored.Labels)

	stored.Icon = "vegan"
	updated, err := db.UpdateRecipeTag(ctx, stored)
	require.NoError(t, err)
	assert.Equal(t, "vegan", updated.Icon)
}
