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
	"recipes/internal/models"
)

func newTestDB(t *testing.T) database.Database {
	t.Helper()
	databaseName := "Recipes_test_" + primitive.NewObjectID().Hex()
	db, err := mongorepo.New(&config.DatabaseConfig{
		DefaultAddr: "mongodb://localhost:27017",
		Name:        databaseName,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Skipf("local MongoDB is not available: %v", err)
	}
	t.Cleanup(func() {
		_ = db.RawDatabase().Drop(context.Background())
		_ = db.Close(context.Background())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, db.EnsureIndexes(ctx))
	return db
}

func insertTestUser(t *testing.T, db database.Database, ctx context.Context, username string) primitive.ObjectID {
	t.Helper()
	id := primitive.NewObjectID()
	_, err := db.RawDatabase().Collection("users").InsertOne(ctx, bson.M{
		"_id": id, "username": username, "email": username + "@example.test",
	})
	require.NoError(t, err)
	return id
}

func insertTestRecipe(t *testing.T, db database.Database, ctx context.Context, authorID primitive.ObjectID, title string) primitive.ObjectID {
	t.Helper()
	id := primitive.NewObjectID()
	_, err := db.RawDatabase().Collection("recipes").InsertOne(ctx, bson.M{
		"_id":         id,
		"author":      authorID,
		"title":       title,
		"ingredients": bson.A{bson.M{"name": "flour"}},
		"kind":        "dish",
	})
	require.NoError(t, err)
	return id
}

func TestAddFavoriteIsIdempotentAndDecorates(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "fav-author")
	otherUser := insertTestUser(t, db, ctx, "fav-other")
	recipeID := insertTestRecipe(t, db, ctx, author, "Pancakes")

	require.NoError(t, db.AddFavorite(ctx, author.Hex(), recipeID.Hex()))
	require.NoError(t, db.AddFavorite(ctx, author.Hex(), recipeID.Hex())) // idempotent

	info, err := db.GetFavoriteInfo(ctx, []string{recipeID.Hex()}, author.Hex())
	require.NoError(t, err)
	require.Contains(t, info, recipeID.Hex())
	assert.Equal(t, int64(1), info[recipeID.Hex()].Count)
	assert.True(t, info[recipeID.Hex()].Favorited)

	// A different user sees the same public count but Favorited: false.
	otherInfo, err := db.GetFavoriteInfo(ctx, []string{recipeID.Hex()}, otherUser.Hex())
	require.NoError(t, err)
	assert.Equal(t, int64(1), otherInfo[recipeID.Hex()].Count)
	assert.False(t, otherInfo[recipeID.Hex()].Favorited)

	// Anonymous (empty userID) sees the count but never Favorited: true.
	anonInfo, err := db.GetFavoriteInfo(ctx, []string{recipeID.Hex()}, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), anonInfo[recipeID.Hex()].Count)
	assert.False(t, anonInfo[recipeID.Hex()].Favorited)

	require.NoError(t, db.RemoveFavorite(ctx, author.Hex(), recipeID.Hex()))
	require.NoError(t, db.RemoveFavorite(ctx, author.Hex(), recipeID.Hex())) // idempotent

	afterRemove, err := db.GetFavoriteInfo(ctx, []string{recipeID.Hex()}, author.Hex())
	require.NoError(t, err)
	assert.Equal(t, int64(0), afterRemove[recipeID.Hex()].Count)
	assert.False(t, afterRemove[recipeID.Hex()].Favorited)
}

func TestDeleteFavoritesByRecipeIDRemovesTheRecord(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "fav-delete-author")
	recipeID := insertTestRecipe(t, db, ctx, author, "Waffles")

	require.NoError(t, db.AddFavorite(ctx, author.Hex(), recipeID.Hex()))
	require.NoError(t, db.DeleteFavoritesByRecipeID(ctx, recipeID.Hex()))

	info, err := db.GetFavoriteInfo(ctx, []string{recipeID.Hex()}, author.Hex())
	require.NoError(t, err)
	assert.NotContains(t, info, recipeID.Hex())
}

// TestGetRecipeDocumentsBoostedPartitionsFavoritesFirst guards the pagination
// contract the favorites-first Home feed relies on: every recipe the user
// favorited (matching the filters) comes back in the unpaginated `favorited`
// segment, and the normal offset/limit pagination applies only to the
// remaining non-favorited matches.
func TestGetRecipeDocumentsBoostedPartitionsFavoritesFirst(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "boost-author")
	user := insertTestUser(t, db, ctx, "boost-user")

	r1 := insertTestRecipe(t, db, ctx, author, "Recipe One")
	r2 := insertTestRecipe(t, db, ctx, author, "Recipe Two")
	r3 := insertTestRecipe(t, db, ctx, author, "Recipe Three")

	require.NoError(t, db.AddFavorite(ctx, user.Hex(), r2.Hex()))

	favorited, rest, total, err := db.GetRecipeDocumentsBoosted(ctx, user.Hex(), models.GetRecipesRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, favorited, 1)
	assert.Equal(t, r2.Hex(), favorited[0].Id.Hex())
	require.Len(t, rest, 2)
	assert.ElementsMatch(t, []string{r1.Hex(), r3.Hex()}, []string{rest[0].Id.Hex(), rest[1].Id.Hex()})
	assert.Equal(t, int64(3), total)

	// The favorited block is only included on the Offset == 0 call; Offset
	// pages through the non-favorited remainder exclusively, so it must not
	// count the favorited items already shown.
	page1Fav, page1Rest, _, err := db.GetRecipeDocumentsBoosted(ctx, user.Hex(), models.GetRecipesRequest{Limit: 1, Offset: 0})
	require.NoError(t, err)
	require.Len(t, page1Fav, 1)
	require.Len(t, page1Rest, 1)

	page2Fav, page2Rest, _, err := db.GetRecipeDocumentsBoosted(ctx, user.Hex(), models.GetRecipesRequest{Limit: 1, Offset: 1})
	require.NoError(t, err)
	assert.Empty(t, page2Fav, "favorited block must not repeat on later pages")
	require.Len(t, page2Rest, 1)
	assert.NotEqual(t, page1Rest[0].Id.Hex(), page2Rest[0].Id.Hex(), "offset must page through the non-favorited remainder, not repeat it")
}

// TestGetRecipeDocumentsFavoritesOnly guards the "My Favorites" listing mode:
// only the exact recipes the caller favorited come back (never another
// user's un-favorited recipes, never the un-favorited sibling in a family),
// narrowed by category the same way the default listing is, and sorted
// alphabetically by title rather than newest-first.
func TestGetRecipeDocumentsFavoritesOnly(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "myfav-author")
	user := insertTestUser(t, db, ctx, "myfav-user")

	root := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Zebra Cake",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})
	variation := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Apple Cake V2", "variation_of": root,
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})
	unfavorited := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Mango Cake",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish",
	})
	diyItem := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Bar Soap", "category": "diy",
		"ingredients": bson.A{bson.M{"name": "lye"}},
	})

	require.NoError(t, db.AddFavorite(ctx, user.Hex(), root.Hex()))
	require.NoError(t, db.AddFavorite(ctx, user.Hex(), variation.Hex()))
	require.NoError(t, db.AddFavorite(ctx, user.Hex(), diyItem.Hex()))
	// unfavorited stays un-favorited; another user's favorite must not leak in.
	otherUser := insertTestUser(t, db, ctx, "myfav-other")
	require.NoError(t, db.AddFavorite(ctx, otherUser.Hex(), unfavorited.Hex()))

	foodFavorites, count, err := db.GetRecipeDocuments(models.GetRecipesRequest{
		Limit: 10, FavoritesOnly: true, FavoritedByUserID: user.Hex(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
	require.Len(t, foodFavorites, 2)
	// Flat, not family-collapsed: both the root and the variation the user
	// favorited show up as distinct entries. Alphabetical by title: "Apple"
	// before "Zebra".
	assert.Equal(t, variation.Hex(), foodFavorites[0].Id.Hex())
	assert.Equal(t, root.Hex(), foodFavorites[1].Id.Hex())

	diyFavorites, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{
		Limit: 10, FavoritesOnly: true, FavoritedByUserID: user.Hex(), Category: models.Diy,
	})
	require.NoError(t, err)
	require.Len(t, diyFavorites, 1)
	assert.Equal(t, diyItem.Hex(), diyFavorites[0].Id.Hex())

	// Anonymous/empty caller id: fail closed, never leak every recipe.
	anonFavorites, anonCount, err := db.GetRecipeDocuments(models.GetRecipesRequest{
		Limit: 10, FavoritesOnly: true,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), anonCount)
	assert.Empty(t, anonFavorites)
}
