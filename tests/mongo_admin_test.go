package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"recipes/internal/models"
)

// TestAdminStatsCounts covers the small counting methods behind the
// back-office's GET /admin/system/stats (handlers.AdminStats).
func TestAdminStatsCounts(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	author := insertTestUser(t, db, ctx, "stats-author")
	_, err := db.RawDatabase().Collection("users").UpdateByID(ctx, author, bson.M{"$set": bson.M{"admin": true}})
	require.NoError(t, err)
	insertTestUser(t, db, ctx, "stats-non-admin")

	foodRoot := insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Food Root", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish", "category": "food"})
	insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Food Variation", "family_id": foodRoot, "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish", "category": "food"})
	insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "DIY Root", "ingredients": bson.A{bson.M{"name": "wax"}}, "category": "diy"})
	// A legacy document predating the category field must still count as food.
	insertRecipeDoc(t, db, ctx, bson.M{"author": author, "title": "Legacy Root", "ingredients": bson.A{bson.M{"name": "flour"}}, "kind": "dish"})

	totalUsers, err := db.CountUsers(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), totalUsers)

	totalAdmins, err := db.CountAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), totalAdmins)

	byCategory, err := db.RecipeCountsByCategory(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), byCategory[string(models.Food)])
	assert.Equal(t, int64(1), byCategory[string(models.Diy)])
}
