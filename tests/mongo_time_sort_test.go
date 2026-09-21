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
