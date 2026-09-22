package recipe_service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

// SubmitTranslationSuggestion validates and stores a user-proposed fix to one
// locale's machine translation of a recipe. Structure (ingredient/step count
// and order) is always taken from the canonical recipe, never trusted from
// the request - a suggestion only ever touches linguistic content.
func (s *Service) SubmitTranslationSuggestion(ctx context.Context, recipeID, userID string, req models.SubmitTranslationSuggestionRequest) (models.TranslationSuggestion, error) {
	submittedBy, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return models.TranslationSuggestion{}, ErrForbidden
	}
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return models.TranslationSuggestion{}, ErrNotFound
	}
	locale, err := normalizeLocale(req.Locale)
	if err != nil || locale == "" {
		return models.TranslationSuggestion{}, fmt.Errorf("%w: locale is required", ErrInvalid)
	}
	canonical, err := s.db.GetRecipeById(recipeID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return models.TranslationSuggestion{}, ErrNotFound
		}
		return models.TranslationSuggestion{}, err
	}
	if canonical.SourceHash == "" {
		return models.TranslationSuggestion{}, fmt.Errorf("%w: recipe has no translation to fix yet", ErrInvalid)
	}
	if sameBaseLocale(canonical.SourceLocale, locale) {
		return models.TranslationSuggestion{}, fmt.Errorf("%w: cannot suggest a fix for the recipe's own source locale", ErrInvalid)
	}
	if !slices.Contains(s.targetLocales, locale) {
		return models.TranslationSuggestion{}, fmt.Errorf("%w: locale is not a configured translation target", ErrInvalid)
	}
	if len(req.Ingredients) != len(canonical.Ingredients) || len(req.Steps) != len(canonical.Steps) {
		return models.TranslationSuggestion{}, fmt.Errorf("%w: ingredients and steps must match the recipe's current structure", ErrInvalid)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return models.TranslationSuggestion{}, fmt.Errorf("%w: title is required", ErrInvalid)
	}

	hasPending, err := s.db.HasPendingTranslationSuggestion(ctx, recipeID, locale, userID)
	if err != nil {
		return models.TranslationSuggestion{}, err
	}
	if hasPending {
		return models.TranslationSuggestion{}, ErrSuggestionAlreadyPending
	}

	ingredients := make([]models.Ingredient, len(req.Ingredients))
	for i, ingredient := range req.Ingredients {
		canonicalIngredient := canonical.Ingredients[i]
		ingredients[i] = models.Ingredient{
			Name:      strings.TrimSpace(ingredient.Name),
			Unit:      strings.TrimSpace(ingredient.Unit),
			Label:     strings.TrimSpace(ingredient.Label),
			RefLabel:  strings.TrimSpace(ingredient.RefLabel),
			Quantity:  canonicalIngredient.Quantity,
			RecipeRef: canonicalIngredient.RecipeRef,
		}
		// A reference ingredient carries no free-text name; a plain one
		// carries no ref label - mirrors validateRecipe's own rule.
		if canonicalIngredient.RecipeRef != nil {
			ingredients[i].Name = ""
		} else {
			ingredients[i].RefLabel = ""
		}
	}
	steps := make([]models.Step, len(req.Steps))
	for i, step := range req.Steps {
		steps[i] = models.Step{
			Title:       strings.TrimSpace(step.Title),
			Description: strings.TrimSpace(step.Description),
			// Picture is structural, not translatable content - always taken
			// from canonical, same as the base translation pipeline.
			Picture: canonical.Steps[i].Picture,
		}
	}

	suggestion := models.TranslationSuggestion{
		RecipeID:    *canonical.Id,
		Locale:      locale,
		SubmittedBy: submittedBy,
		SourceHash:  canonical.SourceHash,
		Title:       title,
		Description: strings.TrimSpace(req.Description),
		Ingredients: ingredients,
		Steps:       steps,
	}
	return s.db.CreateTranslationSuggestion(ctx, suggestion)
}

// ListTranslationSuggestionsForAdmin resolves each suggestion's recipe title,
// submitter username, and current live translation (the baseline the
// submitter actually saw), so the admin review tab can render an
// old-vs-suggested comparison without a second round trip.
func (s *Service) ListTranslationSuggestionsForAdmin(ctx context.Context, status string, limit, offset int64) (models.ListTranslationSuggestionsResponse, error) {
	suggestions, total, err := s.db.ListTranslationSuggestions(ctx, status, limit, offset)
	if err != nil {
		return models.ListTranslationSuggestionsResponse{}, err
	}
	views := make([]models.TranslationSuggestionView, 0, len(suggestions))
	if len(suggestions) == 0 {
		return models.ListTranslationSuggestionsResponse{Length: total, Items: views}, nil
	}

	recipeIDs := make([]string, 0, len(suggestions))
	userIDs := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		recipeIDs = append(recipeIDs, suggestion.RecipeID.Hex())
		userIDs = append(userIDs, suggestion.SubmittedBy.Hex())
	}
	titles, err := s.db.GetRecipeTitles(ctx, recipeIDs)
	if err != nil {
		return models.ListTranslationSuggestionsResponse{}, err
	}
	users, err := s.db.GetUsersByIDs(ctx, userIDs)
	if err != nil {
		return models.ListTranslationSuggestionsResponse{}, err
	}

	for _, suggestion := range suggestions {
		view := models.TranslationSuggestionView{
			Id: suggestion.Id, RecipeID: suggestion.RecipeID,
			RecipeTitle:          titles[suggestion.RecipeID.Hex()],
			Locale:               suggestion.Locale,
			SubmittedBy:          suggestion.SubmittedBy,
			SubmittedByUsername:  users[suggestion.SubmittedBy.Hex()].Username,
			Status:               suggestion.Status,
			CreatedAt:            suggestion.CreatedAt,
			ReviewedAt:           suggestion.ReviewedAt,
			SuggestedTitle:       suggestion.Title,
			SuggestedDescription: suggestion.Description,
			SuggestedIngredients: suggestion.Ingredients,
			SuggestedSteps:       suggestion.Steps,
		}
		if current, err := s.db.GetRecipeByIdLocale(suggestion.RecipeID.Hex(), suggestion.Locale); err == nil {
			view.CurrentTitle = current.Title
			view.CurrentDescription = current.Description
			view.CurrentIngredients = current.Ingredients
			view.CurrentSteps = current.Steps
		}
		views = append(views, view)
	}
	return models.ListTranslationSuggestionsResponse{Length: total, Items: views}, nil
}

// ApproveTranslationSuggestion applies a submitted fix to the live
// translation and pins the fields it actually changed (relative to the
// translation the submitter saw) as durable overrides - see
// docs/translation-suggestions.md's Background for why the SourceHash check
// and the AddLocaleRecipe write below matter.
func (s *Service) ApproveTranslationSuggestion(ctx context.Context, id, adminID string) (models.TranslationSuggestion, error) {
	suggestion, err := s.db.GetTranslationSuggestionById(ctx, id)
	if err != nil {
		if errors.Is(err, mongorepo.TranslationSuggestionNotFoundError) {
			return models.TranslationSuggestion{}, ErrSuggestionNotFound
		}
		return models.TranslationSuggestion{}, err
	}

	canonical, err := s.db.GetRecipeById(suggestion.RecipeID.Hex())
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			_ = s.db.UpdateTranslationSuggestionStatus(ctx, id, models.TranslationSuggestionStale, adminID)
			return models.TranslationSuggestion{}, ErrSuggestionStale
		}
		return models.TranslationSuggestion{}, err
	}
	if canonical.SourceHash == "" || canonical.SourceHash != suggestion.SourceHash {
		_ = s.db.UpdateTranslationSuggestionStatus(ctx, id, models.TranslationSuggestionStale, adminID)
		return models.TranslationSuggestion{}, ErrSuggestionStale
	}

	// The baseline the submitter actually saw: whatever is (still) live for
	// this locale. Zero value (not found) if none exists yet - every field
	// then reads as "changed", which is correct.
	baseline, _ := s.db.GetRecipeByIdLocale(suggestion.RecipeID.Hex(), suggestion.Locale)

	assembled := canonical
	assembled.Title = suggestion.Title
	assembled.Description = suggestion.Description
	assembled.Ingredients = suggestion.Ingredients
	assembled.Steps = suggestion.Steps
	assembled.Locale = suggestion.Locale
	assembled.SourceHash = canonical.SourceHash
	if _, err := s.db.AddLocaleRecipe(assembled, suggestion.Locale); err != nil {
		return models.TranslationSuggestion{}, err
	}

	for _, override := range diffTranslationOverrides(canonical, baseline, suggestion, adminID) {
		if err := s.db.UpsertTranslationOverride(ctx, override); err != nil {
			return models.TranslationSuggestion{}, err
		}
	}

	if err := s.db.UpdateTranslationSuggestionStatus(ctx, id, models.TranslationSuggestionApproved, adminID); err != nil {
		return models.TranslationSuggestion{}, err
	}
	suggestion.Status = models.TranslationSuggestionApproved
	return suggestion, nil
}

// RejectTranslationSuggestion dismisses a submitted fix with no further
// effect - no reason is recorded, matching the spec's v1 scope.
func (s *Service) RejectTranslationSuggestion(ctx context.Context, id, adminID string) (models.TranslationSuggestion, error) {
	suggestion, err := s.db.GetTranslationSuggestionById(ctx, id)
	if err != nil {
		if errors.Is(err, mongorepo.TranslationSuggestionNotFoundError) {
			return models.TranslationSuggestion{}, ErrSuggestionNotFound
		}
		return models.TranslationSuggestion{}, err
	}
	if err := s.db.UpdateTranslationSuggestionStatus(ctx, id, models.TranslationSuggestionRejected, adminID); err != nil {
		return models.TranslationSuggestion{}, err
	}
	suggestion.Status = models.TranslationSuggestionRejected
	return suggestion, nil
}

// translationFieldDiff is one candidate field comparison: has the submitter's
// value diverged from the baseline they were shown?
type translationFieldDiff struct {
	path      string
	suggested string
	baseline  string
	canonical string
}

// diffTranslationOverrides compares the suggestion against the baseline
// translation the submitter saw, field by field, and returns one
// TranslationOverride per field that actually changed - only those fields
// become durable (see docs/translation-suggestions.md). canonical supplies
// each changed field's current source-language text (the snapshot future
// retranslations compare against) and the ingredient/step counts the
// override is approved against.
func diffTranslationOverrides(canonical, baseline models.Recipe, suggestion models.TranslationSuggestion, adminID string) []models.TranslationOverride {
	approvedBy, _ := primitive.ObjectIDFromHex(adminID)
	now := time.Now().UTC()
	base := models.TranslationOverride{
		RecipeID: suggestion.RecipeID, Locale: suggestion.Locale,
		SuggestionID: suggestion.Id, ApprovedAt: now, ApprovedBy: approvedBy,
	}

	diffs := []translationFieldDiff{
		{"title", suggestion.Title, baseline.Title, canonical.Title},
		{"description", suggestion.Description, baseline.Description, canonical.Description},
	}

	for i, suggested := range suggestion.Ingredients {
		var baselineIngredient, canonicalIngredient models.Ingredient
		if i < len(baseline.Ingredients) {
			baselineIngredient = baseline.Ingredients[i]
		}
		if i < len(canonical.Ingredients) {
			canonicalIngredient = canonical.Ingredients[i]
		}
		diffs = append(diffs,
			translationFieldDiff{fmt.Sprintf("ingredients.%d.name", i), suggested.Name, baselineIngredient.Name, canonicalIngredient.Name},
			translationFieldDiff{fmt.Sprintf("ingredients.%d.unit", i), suggested.Unit, baselineIngredient.Unit, canonicalIngredient.Unit},
			translationFieldDiff{fmt.Sprintf("ingredients.%d.label", i), suggested.Label, baselineIngredient.Label, canonicalIngredient.Label},
			translationFieldDiff{fmt.Sprintf("ingredients.%d.ref_label", i), suggested.RefLabel, baselineIngredient.RefLabel, canonicalIngredient.RefLabel},
		)
	}
	for i, suggested := range suggestion.Steps {
		var baselineStep, canonicalStep models.Step
		if i < len(baseline.Steps) {
			baselineStep = baseline.Steps[i]
		}
		if i < len(canonical.Steps) {
			canonicalStep = canonical.Steps[i]
		}
		diffs = append(diffs,
			translationFieldDiff{fmt.Sprintf("steps.%d.title", i), suggested.Title, baselineStep.Title, canonicalStep.Title},
			translationFieldDiff{fmt.Sprintf("steps.%d.description", i), suggested.Description, baselineStep.Description, canonicalStep.Description},
		)
	}

	ingredientsLen := len(canonical.Ingredients)
	stepsLen := len(canonical.Steps)
	var overrides []models.TranslationOverride
	for _, diff := range diffs {
		if diff.suggested == diff.baseline {
			continue
		}
		override := base
		override.FieldPath = diff.path
		override.Value = diff.suggested
		override.SourceSnapshot = diff.canonical
		switch {
		case strings.HasPrefix(diff.path, "ingredients."):
			override.IngredientsLen = &ingredientsLen
		case strings.HasPrefix(diff.path, "steps."):
			override.StepsLen = &stepsLen
		}
		overrides = append(overrides, override)
	}
	return overrides
}
