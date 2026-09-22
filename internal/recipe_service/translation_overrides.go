package recipe_service

import (
	"context"
	"strconv"
	"strings"

	"recipes/internal/models"
)

// applyTranslationOverrides overlays any durable per-field fixes onto a
// freshly machine-translated recipe before it's stored, so an approved
// suggestion survives a future retranslation triggered by an unrelated edit
// (see docs/translation-suggestions.md). An override whose ingredient/step
// list has been structurally altered since approval (count changed - indices
// no longer mean what they meant then) is dropped as a whole group; an
// override whose own source text has since changed is dropped individually.
// Either way the drop is silent: the field falls back to the fresh machine
// translation just produced.
func (s *Service) applyTranslationOverrides(canonical, translated models.Recipe, locale string) models.Recipe {
	if canonical.Id == nil {
		return translated
	}
	ctx := context.Background()
	recipeID := canonical.Id.Hex()
	overrides, err := s.db.ListTranslationOverrides(ctx, recipeID, locale)
	if err != nil || len(overrides) == 0 {
		return translated
	}

	ingredientsLen := len(canonical.Ingredients)
	stepsLen := len(canonical.Steps)
	ingredientsStructureChanged := false
	stepsStructureChanged := false
	for _, override := range overrides {
		if override.IngredientsLen != nil && *override.IngredientsLen != ingredientsLen {
			ingredientsStructureChanged = true
		}
		if override.StepsLen != nil && *override.StepsLen != stepsLen {
			stepsStructureChanged = true
		}
	}
	if ingredientsStructureChanged {
		_ = s.db.DeleteTranslationOverridesByFieldPrefix(ctx, recipeID, locale, "ingredients.")
	}
	if stepsStructureChanged {
		_ = s.db.DeleteTranslationOverridesByFieldPrefix(ctx, recipeID, locale, "steps.")
	}

	for _, override := range overrides {
		if override.IngredientsLen != nil && ingredientsStructureChanged {
			continue
		}
		if override.StepsLen != nil && stepsStructureChanged {
			continue
		}
		currentSource, ok := translationFieldValue(canonical, override.FieldPath)
		if !ok || currentSource != override.SourceSnapshot {
			if override.Id != nil {
				_, _ = s.db.DeleteTranslationOverride(ctx, override.Id.Hex())
			}
			continue
		}
		setTranslationFieldValue(&translated, override.FieldPath, override.Value)
	}
	return translated
}

// ListTranslationOverridesForAdmin resolves the recipe's display title
// alongside its active overrides, for the admin management view.
func (s *Service) ListTranslationOverridesForAdmin(ctx context.Context, recipeID, locale string) (models.ListTranslationOverridesResponse, error) {
	overrides, err := s.db.ListTranslationOverrides(ctx, recipeID, locale)
	if err != nil {
		return models.ListTranslationOverridesResponse{}, err
	}
	views := make([]models.TranslationOverrideView, 0, len(overrides))
	if len(overrides) == 0 {
		return models.ListTranslationOverridesResponse{Items: views}, nil
	}
	titles, err := s.db.GetRecipeTitles(ctx, []string{recipeID})
	if err != nil {
		return models.ListTranslationOverridesResponse{}, err
	}
	for _, override := range overrides {
		views = append(views, models.TranslationOverrideView{
			Id: override.Id, RecipeID: override.RecipeID, RecipeTitle: titles[override.RecipeID.Hex()],
			Locale: override.Locale, FieldPath: override.FieldPath, Value: override.Value, ApprovedAt: override.ApprovedAt,
		})
	}
	return models.ListTranslationOverridesResponse{Length: int64(len(views)), Items: views}, nil
}

// ClearTranslationOverride removes one override, then immediately
// re-translates that one locale so the revert to machine translation is
// visible right away instead of waiting for the next unrelated recipe edit.
func (s *Service) ClearTranslationOverride(ctx context.Context, id string) error {
	deleted, err := s.db.DeleteTranslationOverride(ctx, id)
	if err != nil {
		return ErrOverrideNotFound
	}
	return s.RetranslateLocale(ctx, deleted.RecipeID.Hex(), deleted.Locale)
}

// translationFieldPath decomposes a field path like "ingredients.2.name" into
// its list ("ingredients"/"steps"/"" for a scalar field), index, and leaf
// field name.
func translationFieldPath(fieldPath string) (list string, index int, field string, ok bool) {
	parts := strings.Split(fieldPath, ".")
	switch len(parts) {
	case 1:
		return "", 0, parts[0], true
	case 3:
		i, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", 0, "", false
		}
		return parts[0], i, parts[2], true
	default:
		return "", 0, "", false
	}
}

// translationFieldValue reads one field path's current source-language text
// off recipe - used both to snapshot at approval time and to detect drift on
// reapplication.
func translationFieldValue(recipe models.Recipe, fieldPath string) (string, bool) {
	list, index, field, ok := translationFieldPath(fieldPath)
	if !ok {
		return "", false
	}
	switch list {
	case "":
		switch field {
		case "title":
			return recipe.Title, true
		case "description":
			return recipe.Description, true
		}
	case "ingredients":
		if index < 0 || index >= len(recipe.Ingredients) {
			return "", false
		}
		ingredient := recipe.Ingredients[index]
		switch field {
		case "name":
			return ingredient.Name, true
		case "unit":
			return ingredient.Unit, true
		case "label":
			return ingredient.Label, true
		case "ref_label":
			return ingredient.RefLabel, true
		}
	case "steps":
		if index < 0 || index >= len(recipe.Steps) {
			return "", false
		}
		step := recipe.Steps[index]
		switch field {
		case "title":
			return step.Title, true
		case "description":
			return step.Description, true
		}
	}
	return "", false
}

// setTranslationFieldValue overwrites one field path on recipe with value.
// Silently a no-op for a path that no longer resolves (shouldn't happen -
// applyTranslationOverrides already dropped structurally-invalid overrides
// before calling this).
func setTranslationFieldValue(recipe *models.Recipe, fieldPath, value string) {
	list, index, field, ok := translationFieldPath(fieldPath)
	if !ok {
		return
	}
	switch list {
	case "":
		switch field {
		case "title":
			recipe.Title = value
		case "description":
			recipe.Description = value
		}
	case "ingredients":
		if index < 0 || index >= len(recipe.Ingredients) {
			return
		}
		switch field {
		case "name":
			recipe.Ingredients[index].Name = value
		case "unit":
			recipe.Ingredients[index].Unit = value
		case "label":
			recipe.Ingredients[index].Label = value
		case "ref_label":
			recipe.Ingredients[index].RefLabel = value
		}
	case "steps":
		if index < 0 || index >= len(recipe.Steps) {
			return
		}
		switch field {
		case "title":
			recipe.Steps[index].Title = value
		case "description":
			recipe.Steps[index].Description = value
		}
	}
}
