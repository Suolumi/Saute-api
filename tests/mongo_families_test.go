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

func TestMigrateRecipeFamiliesSetsFamilyID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "family-author")

	base := func(title string) bson.M {
		return bson.M{"author": author, "title": title, "kind": "dish", "ingredients": bson.A{bson.M{"name": "flour"}}}
	}
	root := insertRecipeDoc(t, db, ctx, base("Root"))
	v1Doc := base("V1")
	v1Doc["variation_of"] = root
	v1 := insertRecipeDoc(t, db, ctx, v1Doc)
	v2Doc := base("V2")
	v2Doc["variation_of"] = root
	v2 := insertRecipeDoc(t, db, ctx, v2Doc)
	standalone := insertRecipeDoc(t, db, ctx, base("Standalone"))

	require.NoError(t, db.MigrateRecipeFamilies(ctx))

	load := func() map[primitive.ObjectID]bson.M {
		cur, err := db.RawDatabase().Collection("recipes").Find(ctx, bson.M{})
		require.NoError(t, err)
		var docs []bson.M
		require.NoError(t, cur.All(ctx, &docs))
		out := map[primitive.ObjectID]bson.M{}
		for _, d := range docs {
			out[d["_id"].(primitive.ObjectID)] = d
		}
		return out
	}
	first := load()
	for _, id := range []primitive.ObjectID{root, v1, v2} {
		assert.Equal(t, root, first[id]["family_id"])
	}
	assert.Equal(t, standalone, first[standalone]["family_id"])

	require.NoError(t, db.MigrateRecipeFamilies(ctx))
	assert.Equal(t, first, load())
}

func TestCreateRecipeAssignsOwnFamilyID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "create-family-author")

	created, err := db.CreateRecipe(author.Hex(), &models.CreateRecipe{
		Title: "Own family", Kind: "dish", Ingredients: []models.Ingredient{{Name: "flour"}},
	})
	require.NoError(t, err)
	require.NotNil(t, created.Id)
	require.NotNil(t, created.FamilyID)
	assert.Equal(t, *created.Id, *created.FamilyID)

	family := primitive.NewObjectID()
	joined, err := db.CreateRecipe(author.Hex(), &models.CreateRecipe{
		Title: "Joined", Kind: "dish", Ingredients: []models.Ingredient{{Name: "flour"}}, FamilyID: &family,
	})
	require.NoError(t, err)
	var doc bson.M
	require.NoError(t, db.RawDatabase().Collection("recipes").FindOne(ctx, bson.M{"_id": *joined.Id}).Decode(&doc))
	assert.Equal(t, family, doc["family_id"])
}
