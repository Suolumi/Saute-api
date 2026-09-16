package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/models"
)

// TestGetRecipeByIdDecodesLegacyStringPictures verifies a recipe document
// written before per-picture attribution existed (a plain array of filename
// strings, the only shape that ever existed in production) still decodes
// correctly, with every entry treated as an author-owned picture - the
// backward-compatibility path RecipePicture.UnmarshalBSONValue exists for,
// so no data migration is required.
func TestGetRecipeByIdDecodesLegacyStringPictures(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "legacy-pictures-author")
	id := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Legacy Recipe",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
		"pictures": bson.A{"legacy-one.jpg", "legacy-two.png"},
	})

	recipe, err := db.GetRecipeById(id.Hex())
	require.NoError(t, err)
	require.Len(t, recipe.Pictures, 2)
	assert.Equal(t, "legacy-one.jpg", recipe.Pictures[0].Filename)
	assert.Nil(t, recipe.Pictures[0].AddedBy)
	assert.Equal(t, "legacy-two.png", recipe.Pictures[1].Filename)
	assert.Nil(t, recipe.Pictures[1].AddedBy)
}

// TestReplaceRecipeByIdRoundTripsPictureAttribution verifies the current
// {filename, added_by} document shape round-trips through a write, keeping
// author-owned (AddedBy nil) and contributor pictures distinguishable.
func TestReplaceRecipeByIdRoundTripsPictureAttribution(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "attribution-author")
	contributor := insertTestUser(t, db, ctx, "attribution-contributor")
	id := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Attributed Recipe",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})

	recipe, err := db.GetRecipeById(id.Hex())
	require.NoError(t, err)
	recipeDB := recipe.ToRecipeDB()
	recipeDB.Pictures = []models.RecipePicture{
		{Filename: "author-pic.jpg"},
		{Filename: "contributor-pic.jpg", AddedBy: &contributor},
	}

	_, err = db.ReplaceRecipeById(ctx, id.Hex(), recipeDB)
	require.NoError(t, err)

	updated, err := db.GetRecipeById(id.Hex())
	require.NoError(t, err)
	require.Len(t, updated.Pictures, 2)
	assert.Equal(t, "author-pic.jpg", updated.Pictures[0].Filename)
	assert.Nil(t, updated.Pictures[0].AddedBy)
	assert.Equal(t, "contributor-pic.jpg", updated.Pictures[1].Filename)
	require.NotNil(t, updated.Pictures[1].AddedBy)
	assert.Equal(t, contributor.Hex(), updated.Pictures[1].AddedBy.Hex())
}

func TestGetUsersByIDsBatchFetches(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	first := insertTestUser(t, db, ctx, "batch-user-one")
	second := insertTestUser(t, db, ctx, "batch-user-two")

	users, err := db.GetUsersByIDs(ctx, []string{first.Hex(), second.Hex(), primitive.NewObjectID().Hex()})
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "batch-user-one", users[first.Hex()].Username)
	assert.Equal(t, "batch-user-two", users[second.Hex()].Username)
}
