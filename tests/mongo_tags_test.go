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
	meatFirst := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Meat First", "ingredients": ingredients, "kind": "dish",
	})
	veganVersion := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Vegan Twist", "family_id": meatFirst, "ingredients": ingredients, "kind": "dish",
		"tags": bson.A{"vegan", "vegetarian"},
	})
	// A newer, untagged version: the vegan one still represents the family.
	insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Meat Second", "family_id": meatFirst, "ingredients": ingredients, "kind": "dish",
	})
	// gluten-free on a sibling: no single recipe of this family is both.
	splitFirst := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Split First", "ingredients": ingredients, "kind": "dish", "tags": bson.A{"vegan"},
	})
	splitGF := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Split GF", "family_id": splitFirst, "ingredients": ingredients, "kind": "dish",
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
	assert.ElementsMatch(t, []string{veganRoot.Hex(), veganVersion.Hex(), splitFirst.Hex()}, ids(vegan),
		"each family shows its newest version carrying the tag")

	gf, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Tags: []string{"gluten-free"}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{veganRoot.Hex(), splitGF.Hex()}, ids(gf))

	both, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Tags: []string{"vegan", "gluten-free"}})
	require.NoError(t, err)
	assert.Equal(t, []string{veganRoot.Hex()}, ids(both), "one recipe must carry every tag")

	flat, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true, Author: "tag-author", Tags: []string{"vegetarian"}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{veganRoot.Hex(), veganVersion.Hex()}, ids(flat))

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

// TestSeedTagIconsFillsOnlyBlankIcons covers the startup step giving the
// original tags their icon: a blank icon is filled, an admin's pick stays.
func TestSeedTagIconsFillsOnlyBlankIcons(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	for _, tag := range []models.RecipeTag{
		{Slug: "vegan", Group: models.TagGroupDiet, Categories: []models.RecipeCategory{models.Food}, Implies: []string{}, Labels: map[string]string{"en": "Vegan"}, SourceLocale: "en"},
		{Slug: "one-pot", Group: models.TagGroupPractical, Categories: []models.RecipeCategory{models.Food}, Implies: []string{}, Labels: map[string]string{"en": "One-pot"}, SourceLocale: "en", Icon: "soup"},
		{Slug: "custom", Group: models.TagGroupPractical, Categories: []models.RecipeCategory{models.Food}, Implies: []string{}, Labels: map[string]string{"en": "Custom"}, SourceLocale: "en"},
	} {
		require.NoError(t, db.CreateRecipeTag(ctx, tag))
	}

	require.NoError(t, db.SeedTagIcons(ctx))
	require.NoError(t, db.SeedTagIcons(ctx))

	vegan, err := db.GetRecipeTag(ctx, "vegan")
	require.NoError(t, err)
	assert.Equal(t, "vegan", vegan.Icon)
	onePot, err := db.GetRecipeTag(ctx, "one-pot")
	require.NoError(t, err)
	assert.Equal(t, "soup", onePot.Icon, "an admin's icon is never overwritten")
	custom, err := db.GetRecipeTag(ctx, "custom")
	require.NoError(t, err)
	assert.Empty(t, custom.Icon, "a tag with no default icon is left alone")
}
