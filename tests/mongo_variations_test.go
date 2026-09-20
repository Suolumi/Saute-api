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

// insertRecipeDoc inserts an arbitrary recipe document, returning its id.
// Unlike insertTestRecipe, callers control every field (ingredients,
// variation_of, ...).
func insertRecipeDoc(t *testing.T, db database.Database, ctx context.Context, doc bson.M) primitive.ObjectID {
	t.Helper()
	id := primitive.NewObjectID()
	doc["_id"] = id
	_, err := db.RawDatabase().Collection("recipes").InsertOne(ctx, doc)
	require.NoError(t, err)
	return id
}

func TestGetRecipeDocumentsVariationFiltering(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "variation-author")
	root := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Cinnamon Rolls",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})
	variation := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Cinnamon Rolls V2", "variation_of": root,
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})

	// Default listing collapses to the root only.
	defaultList, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	require.Len(t, defaultList, 1)
	assert.Equal(t, root.Hex(), defaultList[0].Id.Hex())

	// VariationOf lists that family's variations only, never the root.
	variationsOnly, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, VariationOf: root.Hex()})
	require.NoError(t, err)
	require.Len(t, variationsOnly, 1)
	assert.Equal(t, variation.Hex(), variationsOnly[0].Id.Hex())

	// OwnRecipes lists every recipe by the author flatly, root and variation
	// alike, uncollapsed.
	ownRecipes, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true, Author: "variation-author"})
	require.NoError(t, err)
	require.Len(t, ownRecipes, 2)
	gotIDs := []string{ownRecipes[0].Id.Hex(), ownRecipes[1].Id.Hex()}
	assert.ElementsMatch(t, []string{root.Hex(), variation.Hex()}, gotIDs)
}

func TestGetRecipeDocumentsDefaultListingPromotesFamilyOnNewVariation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "promote-author")
	older := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Older Root",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})
	newer := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Newer Root",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})

	// Before any variation exists, plain newest-first _id ordering applies.
	list, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, newer.Hex(), list[0].Id.Hex())
	assert.Equal(t, older.Hex(), list[1].Id.Hex())

	// A variation on the older root should promote it back to the top of the
	// default listing - the root is still what's shown, but the family's
	// most recent activity, not the root's own creation date, decides its
	// position.
	insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Older Root V2", "variation_of": older,
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})

	list, _, err = db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, older.Hex(), list[0].Id.Hex())
	assert.Equal(t, newer.Hex(), list[1].Id.Hex())
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
	// A legacy document predating this field, with no category set at all -
	// must still show up on the default/food listing (see
	// GetRecipesRequest.Category's doc comment).
	legacy := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Legacy Stew",
		"ingredients": bson.A{bson.M{"name": "carrot"}}, "kind": "dish",
	})

	// Default listing (no Category set) excludes diy but includes both
	// explicit food docs and legacy untagged docs.
	defaultList, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	gotIDs := make([]string, len(defaultList))
	for i, r := range defaultList {
		gotIDs[i] = r.Id.Hex()
	}
	assert.ElementsMatch(t, []string{food.Hex(), legacy.Hex()}, gotIDs)

	// category=diy lists only the diy recipe.
	diyList, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Category: models.Diy})
	require.NoError(t, err)
	require.Len(t, diyList, 1)
	assert.Equal(t, diy.Hex(), diyList[0].Id.Hex())

	// OwnRecipes stays category-agnostic when Category is left unset - it
	// returns every recipe by the author regardless of category.
	ownRecipes, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true, Author: "category-author"})
	require.NoError(t, err)
	ownIDs := make([]string, len(ownRecipes))
	for i, r := range ownRecipes {
		ownIDs[i] = r.Id.Hex()
	}
	assert.ElementsMatch(t, []string{food.Hex(), diy.Hex(), legacy.Hex()}, ownIDs)

	// OwnRecipes with an explicit category=diy narrows to just DIY entries -
	// this is what the admin back-office's DIY moderation tab relies on.
	ownDiy, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true, Category: models.Diy})
	require.NoError(t, err)
	require.Len(t, ownDiy, 1)
	assert.Equal(t, diy.Hex(), ownDiy[0].Id.Hex())
}

func TestGetRecipeDocumentsSearchSurfacesRootViaVariation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "search-variation-author")
	root := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Cinnamon Rolls",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})
	insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Chocolate Swirl Rolls", "variation_of": root,
		"ingredients": bson.A{bson.M{"name": "chocolate chips"}}, "kind": "dish",
	})

	// A title that only exists on the variation still surfaces the root, not
	// the variation, and not a duplicate.
	byTitle, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Title: "chocolate"})
	require.NoError(t, err)
	require.Len(t, byTitle, 1)
	assert.Equal(t, root.Hex(), byTitle[0].Id.Hex())

	// Ingredient search is satisfied by the union across the family: the
	// root has flour, the variation has chocolate chips, neither has both.
	byIngredients, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Ingredients: []string{"flour", "chocolate chips"}})
	require.NoError(t, err)
	require.Len(t, byIngredients, 1)
	assert.Equal(t, root.Hex(), byIngredients[0].Id.Hex())

	// The variation_of listing (detail-page siblings) is unaffected by the
	// search cross-match - it's already scoped to one family.
	noMatch, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Title: "nonexistent"})
	require.NoError(t, err)
	assert.Empty(t, noMatch)
}

func TestGetFamilyFavoriteInfoCountsDistinctPeopleAcrossVariations(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "family-fav-author")
	userA := insertTestUser(t, db, ctx, "family-fav-a")
	userB := insertTestUser(t, db, ctx, "family-fav-b")
	userC := insertTestUser(t, db, ctx, "family-fav-c")

	root := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Root", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	variation := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Variation", "variation_of": root, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})

	require.NoError(t, db.AddFavorite(ctx, userA.Hex(), root.Hex()))
	require.NoError(t, db.AddFavorite(ctx, userA.Hex(), variation.Hex())) // same person, both versions
	require.NoError(t, db.AddFavorite(ctx, userB.Hex(), variation.Hex()))

	infoForA, err := db.GetFamilyFavoriteInfo(ctx, []string{root.Hex()}, userA.Hex())
	require.NoError(t, err)
	require.Contains(t, infoForA, root.Hex())
	assert.Equal(t, int64(2), infoForA[root.Hex()].Count, "userA favoriting both versions must still count once")
	assert.True(t, infoForA[root.Hex()].Favorited)

	infoForC, err := db.GetFamilyFavoriteInfo(ctx, []string{root.Hex()}, userC.Hex())
	require.NoError(t, err)
	assert.Equal(t, int64(2), infoForC[root.Hex()].Count)
	assert.False(t, infoForC[root.Hex()].Favorited)
}

func TestGetRecipeDocumentsBoostedTreatsVariationFavoriteAsFamilyFavorite(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "boost-variation-author")
	user := insertTestUser(t, db, ctx, "boost-variation-user")

	root := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Root", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	other := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Unrelated", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	variation := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Variation", "variation_of": root, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	_ = variation

	// The user favorited the variation directly, never the root itself.
	require.NoError(t, db.AddFavorite(ctx, user.Hex(), variation.Hex()))

	favorited, rest, total, err := db.GetRecipeDocumentsBoosted(ctx, user.Hex(), models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, favorited, 1)
	assert.Equal(t, root.Hex(), favorited[0].Id.Hex(), "the root must be boosted since its variation was favorited")
	require.Len(t, rest, 1)
	assert.Equal(t, other.Hex(), rest[0].Id.Hex())
	assert.Equal(t, int64(2), total)
}

func TestVariationCountsAndPromotion(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "promotion-author")
	root := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Root", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	oldest := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Oldest variation", "variation_of": root, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	newest := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Newest variation", "variation_of": root, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})

	counts, err := db.GetVariationCounts(ctx, []string{root.Hex()})
	require.NoError(t, err)
	assert.Equal(t, int64(2), counts[root.Hex()])

	oldestID, err := db.GetOldestVariationID(ctx, root.Hex())
	require.NoError(t, err)
	require.NotNil(t, oldestID)
	assert.Equal(t, oldest.Hex(), oldestID.Hex())

	require.NoError(t, db.RepointVariations(ctx, root.Hex(), oldest.Hex()))
	require.NoError(t, db.PromoteRecipeToRoot(ctx, oldest.Hex()))

	// newest is now a variation of oldest, not of root.
	reloadedNewest, err := db.GetRecipeById(newest.Hex())
	require.NoError(t, err)
	require.NotNil(t, reloadedNewest.VariationOf)
	assert.Equal(t, oldest.Hex(), reloadedNewest.VariationOf.Hex())

	// oldest itself is now a root (variation_of unset).
	reloadedOldest, err := db.GetRecipeById(oldest.Hex())
	require.NoError(t, err)
	assert.Nil(t, reloadedOldest.VariationOf)

	// root's family no longer has any variations - they were all re-pointed.
	countsAfter, err := db.GetVariationCounts(ctx, []string{root.Hex()})
	require.NoError(t, err)
	assert.Equal(t, int64(0), countsAfter[root.Hex()])

	countsForOldest, err := db.GetVariationCounts(ctx, []string{oldest.Hex()})
	require.NoError(t, err)
	assert.Equal(t, int64(1), countsForOldest[oldest.Hex()])
}

// TestGetRecipeDocumentsOwnRecipesFlatAcrossAllUsers backs the admin
// back-office's moderation browse view (handlers.AdminListRecipes): OwnRecipes
// with no Author filter must return every recipe and variation from every
// user, flat and ungrouped - never collapsed to family roots the way the
// default public listing is.
func TestGetRecipeDocumentsOwnRecipesFlatAcrossAllUsers(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	authorA := insertTestUser(t, db, ctx, "admin-browse-author-a")
	authorB := insertTestUser(t, db, ctx, "admin-browse-author-b")
	rootA := insertRecipeDoc(t, db, ctx, bson.M{"author": authorA, "title": "A Root", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	variationA := insertRecipeDoc(t, db, ctx, bson.M{"author": authorA, "title": "A Variation", "variation_of": rootA, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})
	rootB := insertRecipeDoc(t, db, ctx, bson.M{"author": authorB, "title": "B Root", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})

	all, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, OwnRecipes: true})
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
	require.Len(t, all, 3)
	gotIDs := []string{all[0].Id.Hex(), all[1].Id.Hex(), all[2].Id.Hex()}
	assert.ElementsMatch(t, []string{rootA.Hex(), variationA.Hex(), rootB.Hex()}, gotIDs)
}
