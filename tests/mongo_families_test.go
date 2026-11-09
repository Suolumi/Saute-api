package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/database"
	"recipes/internal/models"
)

func TestMigrateRecipeFamiliesSetsFamilyID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "family-author")

	// Pre-migration documents: no family_id, so not insertRecipeDoc.
	insert := func(title string, variationOf *primitive.ObjectID) primitive.ObjectID {
		doc := bson.M{"_id": primitive.NewObjectID(), "author": author, "title": title, "kind": "dish", "ingredients": bson.A{bson.M{"name": "flour"}}}
		if variationOf != nil {
			doc["variation_of"] = *variationOf
		}
		_, err := db.RawDatabase().Collection("recipes").InsertOne(ctx, doc)
		require.NoError(t, err)
		return doc["_id"].(primitive.ObjectID)
	}
	root := insert("Root", nil)
	v1 := insert("V1", &root)
	v2 := insert("V2", &root)
	standalone := insert("Standalone", nil)

	_, err := db.RawDatabase().Collection("recipes").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "variation_of", Value: 1}}, Options: options.Index().SetSparse(true).SetName("recipes_variation_of"),
	})
	require.NoError(t, err)

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
	for id, doc := range first {
		assert.NotContains(t, doc, "variation_of", id.Hex())
	}
	idx, err := db.RawDatabase().Collection("recipes").Indexes().List(ctx)
	require.NoError(t, err)
	var indexes []bson.M
	require.NoError(t, idx.All(ctx, &indexes))
	for _, i := range indexes {
		assert.NotEqual(t, "recipes_variation_of", i["name"])
	}

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

// insertRecipeDoc inserts an arbitrary recipe document, returning its id.
// Unlike insertTestRecipe, callers control every field. family_id defaults to
// the recipe's own id, like a standalone recipe.
func insertRecipeDoc(t *testing.T, db database.Database, ctx context.Context, doc bson.M) primitive.ObjectID {
	t.Helper()
	id := primitive.NewObjectID()
	doc["_id"] = id
	if _, ok := doc["family_id"]; !ok {
		doc["family_id"] = id
	}
	_, err := db.RawDatabase().Collection("recipes").InsertOne(ctx, doc)
	require.NoError(t, err)
	return id
}

func recipeIDs(recipes []models.Recipe) []primitive.ObjectID {
	out := make([]primitive.ObjectID, len(recipes))
	for i, recipe := range recipes {
		out[i] = *recipe.Id
	}
	return out
}

// lasagnaFixture creates family F = {A "Lasagna" (flour), B "Vegan lasagna"
// (tagged vegan), C "Lasagna" (cinnamon)} in that order, then a standalone S.
func lasagnaFixture(t *testing.T, db database.Database, author primitive.ObjectID) (f, a, b, c, s primitive.ObjectID) {
	t.Helper()
	ctx := context.Background()
	f = primitive.NewObjectID()
	a = insertRecipeDoc(t, db, ctx, bson.M{"author": author, "family_id": f, "title": "Lasagna", "kind": "dish",
		"ingredients": bson.A{bson.M{"name": "flour"}, bson.M{"name": "pasta"}}})
	b = insertRecipeDoc(t, db, ctx, bson.M{"author": author, "family_id": f, "title": "Vegan lasagna", "kind": "dish",
		"ingredients": bson.A{bson.M{"name": "pasta"}}, "tags": bson.A{"vegan"}})
	c = insertRecipeDoc(t, db, ctx, bson.M{"author": author, "family_id": f, "title": "Lasagna", "kind": "dish",
		"ingredients": bson.A{bson.M{"name": "cinnamon"}, bson.M{"name": "pasta"}}})
	s = insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Soup", "kind": "dish",
		"ingredients": bson.A{bson.M{"name": "water"}}})
	return
}

func TestCollapsedListingShowsNewestVersion(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "collapsed-newest")
	f, _, _, c, s := lasagnaFixture(t, db, author)

	list, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(4), count, "length counts individual recipes, not families")
	// S was created after C, so it comes first.
	assert.Equal(t, []primitive.ObjectID{s, c}, recipeIDs(list))

	// A version created later than S moves its family back to the top.
	d := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "family_id": f, "title": "Lasagna 2", "kind": "dish",
		"ingredients": bson.A{bson.M{"name": "pasta"}}})
	list, _, err = db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{d, s}, recipeIDs(list))
}

func TestCollapsedListingShowsNewestMatchingVersion(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, _, b, _, _ := lasagnaFixture(t, db, insertTestUser(t, db, ctx, "collapsed-matching"))

	vegan, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Tags: []string{"vegan"}})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{b}, recipeIDs(vegan))
	assert.Equal(t, int64(1), count)

	byTitle, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Title: "vegan"})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{b}, recipeIDs(byTitle))
}

func TestCollapsedIngredientSearchIsPerRecipe(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, a, _, _, _ := lasagnaFixture(t, db, insertTestUser(t, db, ctx, "collapsed-ingredients"))

	both, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Ingredients: []string{"flour", "cinnamon"}})
	require.NoError(t, err)
	assert.Empty(t, both, "no single version has both ingredients")
	assert.Equal(t, int64(0), count)

	flour, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Ingredients: []string{"flour"}})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{a}, recipeIDs(flour))
}

func TestCollapsedListingPaginatesFamilies(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "collapsed-pages")
	for i := 0; i < 3; i++ {
		f := primitive.NewObjectID()
		for j := 0; j < 2; j++ {
			insertRecipeDoc(t, db, ctx, bson.M{"author": author, "family_id": f, "title": "Recipe", "kind": "dish",
				"ingredients": bson.A{bson.M{"name": "flour"}}})
		}
	}

	page1, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(6), count)
	require.Len(t, page1, 2)
	page2, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 2, Offset: 2})
	require.NoError(t, err)
	require.Len(t, page2, 1)

	families := map[primitive.ObjectID]bool{}
	for _, recipe := range append(page1, page2...) {
		require.NotNil(t, recipe.FamilyID)
		assert.False(t, families[*recipe.FamilyID], "a family must appear once")
		families[*recipe.FamilyID] = true
	}
	assert.Len(t, families, 3)
}

func TestCollapsedPopularityAggregatesFamily(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "collapsed-popular")
	user1 := insertTestUser(t, db, ctx, "collapsed-popular-1")
	user2 := insertTestUser(t, db, ctx, "collapsed-popular-2")
	_, a, _, c, s := lasagnaFixture(t, db, author)

	require.NoError(t, db.AddFavorite(ctx, user1.Hex(), a.Hex()))
	require.NoError(t, db.AddFavorite(ctx, user2.Hex(), c.Hex()))
	require.NoError(t, db.AddFavorite(ctx, user1.Hex(), s.Hex()))

	list, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Popular: true})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{c, s}, recipeIDs(list), "the family's 2 people outrank the standalone's 1")
}

func TestFamilyListingListsAllMembersNewestFirst(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	f, a, b, c, _ := lasagnaFixture(t, db, insertTestUser(t, db, ctx, "family-listing"))

	list, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Family: f.Hex()})
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
	assert.Equal(t, []primitive.ObjectID{c, b, a}, recipeIDs(list))
}

func TestExcludeFamilyHidesWholeFamily(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	f, _, _, _, s := lasagnaFixture(t, db, insertTestUser(t, db, ctx, "exclude-family"))

	collapsed, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, ExcludeFamily: f.Hex()})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{s}, recipeIDs(collapsed))

	flat, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Flat: true, ExcludeFamily: f.Hex()})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{s}, recipeIDs(flat))
}

func TestGetRecipeDocumentsCategoryFiltering(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "category-author")
	food := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Cinnamon Rolls",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish", "category": "food",
	})
	diy := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Lavender Soap",
		"ingredients": bson.A{bson.M{"name": "lye"}}, "category": "diy",
	})
	// A legacy document with no category at all still shows on the food listing.
	legacy := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Legacy Stew",
		"ingredients": bson.A{bson.M{"name": "carrot"}}, "kind": "dish",
	})

	defaultList, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	assert.ElementsMatch(t, []primitive.ObjectID{food, legacy}, recipeIDs(defaultList))

	diyList, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Category: models.Diy})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{diy}, recipeIDs(diyList))

	// OwnRecipes stays category-agnostic when Category is unset.
	ownRecipes, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true, Author: "category-author"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []primitive.ObjectID{food, diy, legacy}, recipeIDs(ownRecipes))

	// The admin DIY tab: OwnRecipes with category=diy narrows to DIY.
	ownDiy, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true, Category: models.Diy})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{diy}, recipeIDs(ownDiy))
}

func TestGetFamilyFavoriteInfoCountsDistinctPeopleAcrossVersions(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "family-fav-author")
	userA := insertTestUser(t, db, ctx, "family-fav-a")
	userB := insertTestUser(t, db, ctx, "family-fav-b")
	userC := insertTestUser(t, db, ctx, "family-fav-c")

	family := primitive.NewObjectID()
	first := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "First", "family_id": family, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	second := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Second", "family_id": family, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})

	require.NoError(t, db.AddFavorite(ctx, userA.Hex(), first.Hex()))
	require.NoError(t, db.AddFavorite(ctx, userA.Hex(), second.Hex())) // same person, both versions
	require.NoError(t, db.AddFavorite(ctx, userB.Hex(), second.Hex()))

	infoForA, err := db.GetFamilyFavoriteInfo(ctx, []string{family.Hex()}, userA.Hex())
	require.NoError(t, err)
	require.Contains(t, infoForA, family.Hex())
	assert.Equal(t, int64(2), infoForA[family.Hex()].Count, "userA favoriting both versions must still count once")
	assert.True(t, infoForA[family.Hex()].Favorited)

	infoForC, err := db.GetFamilyFavoriteInfo(ctx, []string{family.Hex()}, userC.Hex())
	require.NoError(t, err)
	assert.Equal(t, int64(2), infoForC[family.Hex()].Count)
	assert.False(t, infoForC[family.Hex()].Favorited)
}

func TestGetRecipeDocumentsBoostedTreatsVersionFavoriteAsFamilyFavorite(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "boost-family-author")
	user := insertTestUser(t, db, ctx, "boost-family-user")

	family := primitive.NewObjectID()
	older := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Older", "family_id": family, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	other := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Unrelated", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	newer := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Newer", "family_id": family, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})

	// The user favorited the older version only; the family's newest
	// version represents it and must still be boosted.
	require.NoError(t, db.AddFavorite(ctx, user.Hex(), older.Hex()))

	favorited, rest, total, err := db.GetRecipeDocumentsBoosted(ctx, user.Hex(), models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{newer}, recipeIDs(favorited))
	assert.Equal(t, []primitive.ObjectID{other}, recipeIDs(rest))
	assert.Equal(t, int64(3), total, "individual recipes, not families")
}

// TestGetRecipeDocumentsOwnRecipesFlatAcrossAllUsers backs the admin
// moderation browse view: OwnRecipes with no Author returns every recipe from
// every user, never collapsed.
func TestGetRecipeDocumentsOwnRecipesFlatAcrossAllUsers(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	authorA := insertTestUser(t, db, ctx, "admin-browse-author-a")
	authorB := insertTestUser(t, db, ctx, "admin-browse-author-b")
	family := primitive.NewObjectID()
	a1 := insertRecipeDoc(t, db, ctx, bson.M{"author": authorA, "title": "A One", "family_id": family, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	a2 := insertRecipeDoc(t, db, ctx, bson.M{"author": authorA, "title": "A Two", "family_id": family, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	b := insertRecipeDoc(t, db, ctx, bson.M{"author": authorB, "title": "B", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})

	all, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true})
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
	assert.ElementsMatch(t, []primitive.ObjectID{a1, a2, b}, recipeIDs(all))
}

func TestGetRecipeDocumentsFlatListing(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "flat-listing-author")
	family := primitive.NewObjectID()
	plain := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Lasagna", "family_id": family, "ingredients": bson.A{bson.M{"name": "pasta"}}, "kind": "dish"})
	veggie := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Veggie Lasagna", "family_id": family, "tags": bson.A{"vegan"}, "ingredients": bson.A{bson.M{"name": "pasta"}}, "kind": "dish"})
	diy := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Soap", "category": "diy"})

	// Every version, newest first, never diy on the food listing.
	food, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Flat: true})
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
	assert.Equal(t, []primitive.ObjectID{veggie, plain}, recipeIDs(food))

	diyOnly, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Flat: true, Category: models.Diy})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{diy}, recipeIDs(diyOnly))

	vegan, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Flat: true, Tags: []string{"vegan"}})
	require.NoError(t, err)
	assert.Equal(t, []primitive.ObjectID{veggie}, recipeIDs(vegan))
}
