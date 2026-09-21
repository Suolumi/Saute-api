package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"recipes/internal/models"
)

// TestGetRecipeDocumentsQuickestSort guards the "Quickest" ready-in preset:
// unlike a target minutes value (which sorts by closeness, so a 10-minute
// recipe can tie with a 20-minute one against a 15-minute target), it must
// sort strictly ascending by the recipe's actual time - the fastest recipe
// always first, regardless of any preset value.
func TestGetRecipeDocumentsQuickestSort(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "quick-author")

	slow := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Slow Roast",
		"ingredients": bson.A{bson.M{"name": "beef"}}, "kind": "dish",
		"preparation_time": 20, "cooking_time": 40,
	})
	fast := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Fast Salad",
		"ingredients": bson.A{bson.M{"name": "lettuce"}}, "kind": "dish",
		"preparation_time": 10,
	})
	medium := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Medium Omelette",
		"ingredients": bson.A{bson.M{"name": "egg"}}, "kind": "dish",
		"preparation_time": 15,
	})

	// QuickestTotal: total time is preparation+cooking+resting, so fast (10)
	// < medium (15) < slow (20+40=60).
	documents, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, QuickestTotal: true})
	require.NoError(t, err)
	require.Len(t, documents, 3)
	assert.Equal(t, []string{fast.Hex(), medium.Hex(), slow.Hex()},
		[]string{documents[0].Id.Hex(), documents[1].Id.Hex(), documents[2].Id.Hex()})

	// QuickestPrep, on My Favorites: ties would break alphabetically, but
	// here prep times alone already order fast < medium < slow.
	require.NoError(t, db.AddFavorite(ctx, author.Hex(), slow.Hex()))
	require.NoError(t, db.AddFavorite(ctx, author.Hex(), fast.Hex()))
	require.NoError(t, db.AddFavorite(ctx, author.Hex(), medium.Hex()))

	favorites, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{
		Limit: 10, FavoritesOnly: true, FavoritedByUserID: author.Hex(), QuickestPrep: true,
	})
	require.NoError(t, err)
	require.Len(t, favorites, 3)
	assert.Equal(t, []string{fast.Hex(), medium.Hex(), slow.Hex()},
		[]string{favorites[0].Id.Hex(), favorites[1].Id.Hex(), favorites[2].Id.Hex()})
}

// TestGetRecipeDocumentsPopularSort guards the "Most Popular" sort: on the
// default family-collapsed listing it must rank by the family's *distinct
// favoriters* (root + every variation combined, matching what
// decorateFamilyFavorite later displays as favorite_count) rather than any
// single member's own count; on a flat listing (own_recipes here) it ranks by
// each recipe's own individual count instead.
func TestGetRecipeDocumentsPopularSort(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "popular-author")
	fan1 := insertTestUser(t, db, ctx, "popular-fan-1")
	fan2 := insertTestUser(t, db, ctx, "popular-fan-2")
	fan3 := insertTestUser(t, db, ctx, "popular-fan-3")

	// Family "Bread": root has 1 direct favorite, its variation has 2 more
	// distinct favoriters (fan2, fan3) - family total 3, more than any single
	// member's own count.
	breadRoot := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Bread",
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "baking",
	})
	breadVariation := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Rye Bread", "variation_of": breadRoot,
		"ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "baking",
	})
	require.NoError(t, db.AddFavorite(ctx, fan1.Hex(), breadRoot.Hex()))
	require.NoError(t, db.AddFavorite(ctx, fan2.Hex(), breadVariation.Hex()))
	require.NoError(t, db.AddFavorite(ctx, fan3.Hex(), breadVariation.Hex()))

	// "Soup": 2 direct favorites, no variations - family total 2.
	soup := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Soup",
		"ingredients": bson.A{bson.M{"name": "water"}}, "kind": "dish",
	})
	require.NoError(t, db.AddFavorite(ctx, fan1.Hex(), soup.Hex()))
	require.NoError(t, db.AddFavorite(ctx, fan2.Hex(), soup.Hex()))

	// "Salad": never favorited.
	salad := insertRecipeDoc(t, db, ctx, bson.M{
		"author": author, "title": "Salad",
		"ingredients": bson.A{bson.M{"name": "lettuce"}}, "kind": "dish",
	})

	documents, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{Limit: 10, Popular: true})
	require.NoError(t, err)
	require.Len(t, documents, 3)
	// The root, not the variation, represents the family on this
	// family-collapsed listing.
	assert.Equal(t, []string{breadRoot.Hex(), soup.Hex(), salad.Hex()},
		[]string{documents[0].Id.Hex(), documents[1].Id.Hex(), documents[2].Id.Hex()})

	// Flat listing (own_recipes): ranks by each recipe's own count, so the
	// variation - 2 direct favorites of its own, more than the root's 1 -
	// now outranks its own root (tied with soup's own 2, broken newest-first).
	flat, _, err := db.GetRecipeDocuments(models.GetRecipesRequest{
		Limit: 10, OwnRecipes: true, Author: "popular-author", Popular: true,
	})
	require.NoError(t, err)
	require.Len(t, flat, 4)
	assert.Equal(t, []string{soup.Hex(), breadVariation.Hex(), breadRoot.Hex(), salad.Hex()},
		[]string{flat[0].Id.Hex(), flat[1].Id.Hex(), flat[2].Id.Hex(), flat[3].Id.Hex()})
}
