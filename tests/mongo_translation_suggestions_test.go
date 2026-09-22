package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"recipes/internal/database"
	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
	"recipes/internal/recipe_service"
)

func strPtr(s string) *string { return &s }

// newTestRecipeService wires a *recipe_service.Service against db with no
// translator - target locales are still configured (so SubmitTranslationSuggestion's
// "is this a configured target" check passes), but nothing ever machine-
// translates, which is fine: these tests only exercise the suggest/approve/
// reject path, never scheduleTranslations/translateLocale.
func newTestRecipeService(t *testing.T, db database.Database) *recipe_service.Service {
	t.Helper()
	svc, err := recipe_service.New(db, nil, "", 0, []string{"fr"})
	require.NoError(t, err)
	return svc
}

func TestTranslationSuggestionSubmitApprove(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	svc := newTestRecipeService(t, db)

	author := insertTestUser(t, db, ctx, "ts-author")
	admin := insertTestUser(t, db, ctx, "ts-admin")
	submitter := insertTestUser(t, db, ctx, "ts-submitter")

	created, err := svc.Create(ctx, author.Hex(), models.CreateRecipe{
		Title: "Pancakes", Quantity: 4, Category: models.Food, Kind: models.Breakfast,
		Ingredients:  []models.Ingredient{{Name: "Flour"}, {Name: "Egg"}},
		Steps:        []models.Step{{Description: "Mix"}, {Description: "Bake"}},
		SourceLocale: "en",
	}, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, created.SourceHash)

	// The baseline translation the submitter will see, inserted directly
	// (standing in for whatever the real translator would have produced).
	_, err = db.AddLocaleRecipe(models.Recipe{
		Id: created.Id, Author: created.Author, Title: "Crêpes", Description: "",
		Ingredients:  []models.Ingredient{{Name: "Farine"}, {Name: "Œuf"}},
		Steps:        []models.Step{{Description: "Mélanger"}, {Description: "Cuire"}},
		SourceLocale: "en", SourceHash: created.SourceHash,
	}, "fr")
	require.NoError(t, err)

	// The submitter only actually fixes the title - everything else matches
	// the baseline exactly, so only "title" should become a durable override.
	suggestion, err := svc.SubmitTranslationSuggestion(ctx, created.Id.Hex(), submitter.Hex(), models.SubmitTranslationSuggestionRequest{
		Locale: "fr", Title: "Crêpes (corrigé)", Description: "",
		Ingredients: []models.Ingredient{{Name: "Farine"}, {Name: "Œuf"}},
		Steps:       []models.Step{{Description: "Mélanger"}, {Description: "Cuire"}},
	})
	require.NoError(t, err)
	assert.Equal(t, models.TranslationSuggestionPending, suggestion.Status)

	// A second pending submission for the same recipe/locale/user is rejected.
	_, err = svc.SubmitTranslationSuggestion(ctx, created.Id.Hex(), submitter.Hex(), models.SubmitTranslationSuggestionRequest{
		Locale: "fr", Title: "Another fix",
		Ingredients: []models.Ingredient{{Name: "Farine"}, {Name: "Œuf"}},
		Steps:       []models.Step{{Description: "Mélanger"}, {Description: "Cuire"}},
	})
	assert.ErrorIs(t, err, recipe_service.ErrSuggestionAlreadyPending)

	approved, err := svc.ApproveTranslationSuggestion(ctx, suggestion.Id.Hex(), admin.Hex())
	require.NoError(t, err)
	assert.Equal(t, models.TranslationSuggestionApproved, approved.Status)

	// The live translation now serves the approved fix - pickTranslation
	// serves it straight from recipe_translations, never touching a
	// translator (there isn't one wired here at all).
	live, err := db.GetRecipeByIdLocale(created.Id.Hex(), "fr")
	require.NoError(t, err)
	assert.Equal(t, "Crêpes (corrigé)", live.Title)
	assert.Equal(t, "Farine", live.Ingredients[0].Name) // untouched field carried through unchanged

	overrides, err := db.ListTranslationOverrides(ctx, created.Id.Hex(), "fr")
	require.NoError(t, err)
	require.Len(t, overrides, 1, "only the field the submitter actually changed becomes an override")
	assert.Equal(t, "title", overrides[0].FieldPath)
	assert.Equal(t, "Crêpes (corrigé)", overrides[0].Value)
	assert.Equal(t, "Pancakes", overrides[0].SourceSnapshot)
}

func TestTranslationSuggestionApproveDetectsStaleness(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	svc := newTestRecipeService(t, db)

	author := insertTestUser(t, db, ctx, "ts-stale-author")
	admin := insertTestUser(t, db, ctx, "ts-stale-admin")
	submitter := insertTestUser(t, db, ctx, "ts-stale-submitter")

	created, err := svc.Create(ctx, author.Hex(), models.CreateRecipe{
		Title: "Waffles", Quantity: 4, Category: models.Food, Kind: models.Breakfast,
		Ingredients:  []models.Ingredient{{Name: "Flour"}},
		Steps:        []models.Step{{Description: "Mix"}},
		SourceLocale: "en",
	}, nil, nil)
	require.NoError(t, err)

	_, err = db.AddLocaleRecipe(models.Recipe{
		Id: created.Id, Author: created.Author, Title: "Gaufres",
		Ingredients:  []models.Ingredient{{Name: "Farine"}},
		Steps:        []models.Step{{Description: "Mélanger"}},
		SourceLocale: "en", SourceHash: created.SourceHash,
	}, "fr")
	require.NoError(t, err)

	suggestion, err := svc.SubmitTranslationSuggestion(ctx, created.Id.Hex(), submitter.Hex(), models.SubmitTranslationSuggestionRequest{
		Locale: "fr", Title: "Gaufres (corrigé)",
		Ingredients: []models.Ingredient{{Name: "Farine"}},
		Steps:       []models.Step{{Description: "Mélanger"}},
	})
	require.NoError(t, err)

	// The recipe changes underneath the pending suggestion - this resets
	// SourceHash (service.go's Update), so it no longer matches what the
	// suggestion was submitted against.
	_, err = svc.Update(ctx, created, models.UpdateRecipeRequest{Description: strPtr("A crisper waffle")}, nil, nil, false)
	require.NoError(t, err)

	_, err = svc.ApproveTranslationSuggestion(ctx, suggestion.Id.Hex(), admin.Hex())
	require.True(t, errors.Is(err, recipe_service.ErrSuggestionStale), "err = %v, want ErrSuggestionStale", err)

	stale, err := db.GetTranslationSuggestionById(ctx, suggestion.Id.Hex())
	require.NoError(t, err)
	assert.Equal(t, models.TranslationSuggestionStale, stale.Status)

	// Not a silent overwrite: Update already dropped the stored translation
	// row (it invalidates every locale on a source edit); the failed approval
	// must not have repopulated it with the suggestion's now-stale content.
	_, err = db.GetRecipeByIdLocale(created.Id.Hex(), "fr")
	assert.ErrorIs(t, err, mongorepo.NotFoundError)
}
