package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/database"
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

// familyFixture creates family F = {A, B, C} (in that order) and a standalone S.
func familyFixture(t *testing.T, db database.Database, author primitive.ObjectID) (f primitive.ObjectID, a, b, c, s models.Recipe) {
	t.Helper()
	f = primitive.NewObjectID()
	mk := func(title string, family *primitive.ObjectID, ings ...models.Ingredient) models.Recipe {
		if len(ings) == 0 {
			ings = []models.Ingredient{{Name: "flour"}}
		}
		r, err := db.CreateRecipe(author.Hex(), &models.CreateRecipe{Title: title, Kind: "dish", Ingredients: ings, FamilyID: family})
		require.NoError(t, err)
		return r
	}
	a, b, c = mk("A", &f), mk("B", &f), mk("C", &f)
	s = mk("S", nil)
	return
}

func familyOf(t *testing.T, db database.Database, id primitive.ObjectID) primitive.ObjectID {
	t.Helper()
	var doc bson.M
	require.NoError(t, db.RawDatabase().Collection("recipes").FindOne(context.Background(), bson.M{"_id": id}).Decode(&doc))
	return doc["family_id"].(primitive.ObjectID)
}

func TestGetFamilySizes(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	f, _, _, _, s := familyFixture(t, db, insertTestUser(t, db, ctx, "sizes"))

	sizes, err := db.GetFamilySizes(ctx, []string{f.Hex(), s.Id.Hex()})
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{f.Hex(): 3, s.Id.Hex(): 1}, sizes)
}

func TestGetNewestFamilyMemberID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	f, _, b, c, s := familyFixture(t, db, insertTestUser(t, db, ctx, "newest"))

	got, err := db.GetNewestFamilyMemberID(ctx, f.Hex(), "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, *c.Id, *got)

	got, err = db.GetNewestFamilyMemberID(ctx, f.Hex(), c.Id.Hex())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, *b.Id, *got)

	got, err = db.GetNewestFamilyMemberID(ctx, s.Id.Hex(), s.Id.Hex())
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSetFamilyID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	f, _, _, _, s := familyFixture(t, db, insertTestUser(t, db, ctx, "setfamily"))

	require.NoError(t, db.SetFamilyID(ctx, s.Id.Hex(), f.Hex()))
	assert.Equal(t, f, familyOf(t, db, *s.Id))
}

func TestRekeyFamily(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	f, a, b, c, _ := familyFixture(t, db, insertTestUser(t, db, ctx, "rekey"))
	n := primitive.NewObjectID()

	require.NoError(t, db.RekeyFamily(ctx, f.Hex(), a.Id.Hex(), n.Hex()))
	assert.Equal(t, f, familyOf(t, db, *a.Id))
	assert.Equal(t, n, familyOf(t, db, *b.Id))
	assert.Equal(t, n, familyOf(t, db, *c.Id))
}

func TestRecipeReferencesFamily(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "refsfamily")
	f, _, b, _, s := familyFixture(t, db, author)

	got, err := db.RecipeReferencesFamily(ctx, s.Id.Hex(), f.Hex())
	require.NoError(t, err)
	assert.False(t, got)

	_, err = db.RawDatabase().Collection("recipes").UpdateOne(ctx, bson.M{"_id": *s.Id},
		bson.M{"$set": bson.M{"ingredients": bson.A{bson.M{"name": "B", "recipe_ref": *b.Id}}}})
	require.NoError(t, err)
	got, err = db.RecipeReferencesFamily(ctx, s.Id.Hex(), f.Hex())
	require.NoError(t, err)
	assert.True(t, got)
}

func TestFamilyReferencesRecipeByFamilyID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	f, a, _, _, s := familyFixture(t, db, insertTestUser(t, db, ctx, "famrefs"))

	got, err := db.FamilyReferencesRecipe(ctx, f.Hex(), s.Id.Hex())
	require.NoError(t, err)
	assert.False(t, got)

	_, err = db.RawDatabase().Collection("recipes").UpdateOne(ctx, bson.M{"_id": *a.Id},
		bson.M{"$set": bson.M{"ingredients": bson.A{bson.M{"name": "S", "recipe_ref": *s.Id}}}})
	require.NoError(t, err)
	got, err = db.FamilyReferencesRecipe(ctx, f.Hex(), s.Id.Hex())
	require.NoError(t, err)
	assert.True(t, got)
}
