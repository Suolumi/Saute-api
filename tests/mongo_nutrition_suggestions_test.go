package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"recipes/internal/database"
	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

// seedTestFoods inserts the embedded food list and returns one food id.
func seedTestFoods(t *testing.T, db database.Database, ctx context.Context) string {
	t.Helper()
	require.NoError(t, db.MigrateFoods(ctx))
	return "flour"
}

// TestNutritionLinkFreshNameAppliesDirectly covers the no-review path: an
// ingredient name with no existing link goes live immediately, no admin
// approval involved.
func TestNutritionLinkFreshNameAppliesDirectly(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	svc := newTestRecipeService(t, db)

	submitter := insertTestUser(t, db, ctx, "ns-fresh-submitter")
	wheatFlour := seedTestFoods(t, db, ctx)

	density := 53.0
	gramsPerUnit := 120.0
	result, err := svc.SubmitNutritionLinkSuggestion(ctx, submitter.Hex(), models.SubmitNutritionLinkRequest{
		IngredientName: "Flour", FoodID: wheatFlour, GPer100ml: &density, GramsPerUnit: &gramsPerUnit,
	})
	require.NoError(t, err)
	assert.True(t, result.Applied, "a fresh ingredient name has no existing link to correct, so it applies immediately")
	require.NotNil(t, result.Link)
	assert.Nil(t, result.Suggestion)

	link, err := db.GetIngredientNutritionLinkByNameLower(ctx, "flour")
	require.NoError(t, err)
	assert.Equal(t, "Flour", link.Name)
	assert.Equal(t, wheatFlour, link.FoodID)
	require.NotNil(t, link.GPer100ml)
	assert.Equal(t, 53.0, *link.GPer100ml)
}

// TestNutritionLinkCorrectionBecomesSuggestionUntilApproved covers the case
// that still goes through review: a submission for an ingredient name that
// already has a link, which would *overwrite* an already-set field (here,
// its density), becomes a pending suggestion with no effect on the live
// link until an admin approves it - unlike merely filling in a field that
// was still empty, which applies directly (see
// TestNutritionLinkFreshNameAppliesDirectly's sibling in nutrition_test.go,
// TestSubmitNutritionLinkSuggestionFillingAnEmptyFieldAppliesDirectly).
func TestNutritionLinkCorrectionBecomesSuggestionUntilApproved(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	svc := newTestRecipeService(t, db)

	admin := insertTestUser(t, db, ctx, "ns-correction-admin")
	submitter := insertTestUser(t, db, ctx, "ns-correction-submitter")
	wheatFlour := seedTestFoods(t, db, ctx)

	density := 53.0
	gramsPerUnit := 120.0
	first, err := svc.SubmitNutritionLinkSuggestion(ctx, submitter.Hex(), models.SubmitNutritionLinkRequest{
		IngredientName: "Flour", FoodID: wheatFlour, GPer100ml: &density, GramsPerUnit: &gramsPerUnit,
	})
	require.NoError(t, err)
	require.True(t, first.Applied)
	original := *first.Link

	// A second submission for "flour" (different case) that changes the
	// already-set density is a correction - it becomes a pending suggestion
	// instead of applying directly.
	correctedDensity := 60.0
	second, err := svc.SubmitNutritionLinkSuggestion(ctx, submitter.Hex(), models.SubmitNutritionLinkRequest{
		IngredientName: "flour", FoodID: wheatFlour, GPer100ml: &correctedDensity,
	})
	require.NoError(t, err)
	assert.False(t, second.Applied, "overwriting an already-set density requires review")
	require.NotNil(t, second.Suggestion)
	assert.Equal(t, models.NutritionSuggestionPending, second.Suggestion.Status)
	require.NotNil(t, second.Suggestion.TargetID)
	assert.Equal(t, *original.Id, *second.Suggestion.TargetID)

	// Not yet applied - the live link is unchanged until approval.
	unchanged, err := db.GetIngredientNutritionLinkByNameLower(ctx, "flour")
	require.NoError(t, err)
	require.NotNil(t, unchanged.GPer100ml)
	assert.Equal(t, density, *unchanged.GPer100ml)

	_, err = svc.ApproveNutritionSuggestion(ctx, second.Suggestion.Id.Hex(), admin.Hex())
	require.NoError(t, err)

	updated, err := db.GetIngredientNutritionLinkByNameLower(ctx, "flour")
	require.NoError(t, err)
	assert.Equal(t, *original.Id, *updated.Id, "the correction updates the same link in place")
	require.NotNil(t, updated.GPer100ml)
	assert.Equal(t, 60.0, *updated.GPer100ml)
}

// TestNutritionLinkRejectedCorrectionLeavesLinkUnchanged covers rejection:
// dismissing a correction suggestion has no effect on the live link.
func TestNutritionLinkRejectedCorrectionLeavesLinkUnchanged(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	svc := newTestRecipeService(t, db)

	admin := insertTestUser(t, db, ctx, "ns-reject-admin")
	submitter := insertTestUser(t, db, ctx, "ns-reject-submitter")
	wheatFlour := seedTestFoods(t, db, ctx)

	gramsPerUnit := 5.0
	first, err := svc.SubmitNutritionLinkSuggestion(ctx, submitter.Hex(), models.SubmitNutritionLinkRequest{
		IngredientName: "Sugar", FoodID: wheatFlour, GramsPerUnit: &gramsPerUnit,
	})
	require.NoError(t, err)
	require.True(t, first.Applied)

	second, err := svc.SubmitNutritionLinkSuggestion(ctx, submitter.Hex(), models.SubmitNutritionLinkRequest{
		IngredientName: "sugar", FoodID: "rye-flour",
	})
	require.NoError(t, err)
	require.False(t, second.Applied)
	require.NotNil(t, second.Suggestion)

	rejected, err := svc.RejectNutritionSuggestion(ctx, second.Suggestion.Id.Hex(), admin.Hex())
	require.NoError(t, err)
	assert.Equal(t, models.NutritionSuggestionRejected, rejected.Status)

	unchanged, err := db.GetIngredientNutritionLinkByNameLower(ctx, "sugar")
	require.NoError(t, err)
	assert.Equal(t, *first.Link.Id, *unchanged.Id, "rejection leaves the original link exactly as it was")
}

// TestAdminNutritionLinkCRUDAppliesImmediately covers the admin back-office's
// direct create/update/delete - never a suggestion, regardless of whether
// the name already has a link.
func TestAdminNutritionLinkCRUDAppliesImmediately(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	svc := newTestRecipeService(t, db)
	wheatFlour := seedTestFoods(t, db, ctx)

	created, err := svc.AdminCreateNutritionLink(ctx, models.AdminNutritionLinkRequest{
		IngredientName: "Butter", FoodID: wheatFlour,
	})
	require.NoError(t, err)
	require.NotNil(t, created.Id)

	density := 96.0
	updated, err := svc.AdminUpdateNutritionLink(ctx, created.Id.Hex(), models.AdminNutritionLinkRequest{
		IngredientName: "Butter", FoodID: wheatFlour, GPer100ml: &density,
	})
	require.NoError(t, err)
	require.NotNil(t, updated.GPer100ml)
	assert.Equal(t, 96.0, *updated.GPer100ml)

	// No suggestion trail was created for either step.
	_, total, err := db.ListNutritionSuggestions(ctx, "", 100, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	err = svc.AdminDeleteNutritionLink(ctx, created.Id.Hex())
	require.NoError(t, err)
	_, err = db.GetIngredientNutritionLinkByNameLower(ctx, "butter")
	assert.ErrorIs(t, err, mongorepo.NutritionEntryNotFoundError)
}
