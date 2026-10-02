package recipe_service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

const (
	minNutritionGPer100ml    = 20.0
	maxNutritionGPer100ml    = 250.0
	minNutritionGramsPerUnit = 0.1
	maxNutritionGramsPerUnit = 5000.0
	maxNutritionNameLength   = 200
	maxNutritionNoteLength   = 500
)

// ListNutritionIngredients returns the full curated Ciqual-derived reference
// list, for client-side matching - small and curated, same posture as the
// Toolbox lists.
func (s *Service) ListNutritionIngredients(ctx context.Context) ([]models.NutritionIngredient, error) {
	return s.db.ListNutritionIngredients(ctx)
}

// ListIngredientNutritionLinks returns every ingredient-name -> nutrition
// link, so the authoring UI can tell an already-linked ingredient from one
// with no link yet.
func (s *Service) ListIngredientNutritionLinks(ctx context.Context) ([]models.IngredientNutritionLink, error) {
	return s.db.ListIngredientNutritionLinks(ctx)
}

// SubmitNutritionLinkSuggestion validates and applies a user-proposed
// ingredient-name -> nutrition-entry link, optionally bundled with a
// unit-alias for the same ingredient's unit text. IngredientUnit decides
// whether a density or a per-unit weight is required for the link to
// actually resolve (see validateIngredientUnitRequirement) - unlike the
// admin CRUD endpoints, which have no single ingredient's unit to be smart
// about and so carry no such requirement.
//
// Applying vs. reviewing is decided field by field against whatever link/
// alias already exists for this name (see needsNutritionLinkReview): filling
// in a field that was previously unset applies immediately, but overwriting
// one that already had a value makes the *whole* submission a single
// pending suggestion requiring admin approval (see docs/nutrition.md
// "Suggestions") - a shared link affects every recipe using that ingredient
// name, so changing something already in use gets reviewed even though
// filling a gap doesn't. Either way the values actually stored/queued are
// the *merged* result (submitted value, falling back to whatever the
// existing link/alias already had) so a correction of just one field never
// silently wipes the other. An admin correcting a link directly uses the
// separate admin CRUD endpoints instead, which always apply immediately
// with no requirement and no merging.
func (s *Service) SubmitNutritionLinkSuggestion(ctx context.Context, userID string, req models.SubmitNutritionLinkRequest) (models.SubmitNutritionLinkResponse, error) {
	submittedBy, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return models.SubmitNutritionLinkResponse{}, ErrForbidden
	}
	name := strings.TrimSpace(req.IngredientName)
	if name == "" || len(name) > maxNutritionNameLength {
		return models.SubmitNutritionLinkResponse{}, fmt.Errorf("%w: ingredient name must be between 1 and %d characters", ErrInvalid, maxNutritionNameLength)
	}
	nutritionID, err := primitive.ObjectIDFromHex(req.NutritionID)
	if err != nil {
		return models.SubmitNutritionLinkResponse{}, fmt.Errorf("%w: invalid nutrition_id", ErrInvalid)
	}
	if _, err := s.db.GetNutritionIngredientById(ctx, nutritionID.Hex()); err != nil {
		if errors.Is(err, mongorepo.NutritionEntryNotFoundError) {
			return models.SubmitNutritionLinkResponse{}, ErrNutritionIngredientNotFound
		}
		return models.SubmitNutritionLinkResponse{}, err
	}
	if req.GPer100ml != nil && (*req.GPer100ml < minNutritionGPer100ml || *req.GPer100ml > maxNutritionGPer100ml) {
		return models.SubmitNutritionLinkResponse{}, fmt.Errorf("%w: density must be between %g and %g grams per 100ml", ErrInvalid, minNutritionGPer100ml, maxNutritionGPer100ml)
	}
	if req.GramsPerUnit != nil && (*req.GramsPerUnit < minNutritionGramsPerUnit || *req.GramsPerUnit > maxNutritionGramsPerUnit) {
		return models.SubmitNutritionLinkResponse{}, fmt.Errorf("%w: grams per unit must be between %g and %g", ErrInvalid, minNutritionGramsPerUnit, maxNutritionGramsPerUnit)
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > maxNutritionNoteLength {
		return models.SubmitNutritionLinkResponse{}, fmt.Errorf("%w: note must be at most %d characters", ErrInvalid, maxNutritionNoteLength)
	}

	var existingLink *models.IngredientNutritionLink
	if existing, err := s.db.GetIngredientNutritionLinkByNameLower(ctx, strings.ToLower(name)); err == nil {
		existingLink = &existing
	} else if !errors.Is(err, mongorepo.NutritionEntryNotFoundError) {
		return models.SubmitNutritionLinkResponse{}, err
	}

	unitAlias := strings.TrimSpace(req.UnitAlias)
	var unitID *primitive.ObjectID
	var existingUnitAlias *models.UnitAlias
	if unitAlias != "" {
		if len(unitAlias) > maxNutritionNameLength {
			return models.SubmitNutritionLinkResponse{}, fmt.Errorf("%w: unit alias must be at most %d characters", ErrInvalid, maxNutritionNameLength)
		}
		parsedUnitID, err := primitive.ObjectIDFromHex(req.UnitID)
		if err != nil {
			return models.SubmitNutritionLinkResponse{}, fmt.Errorf("%w: unit_id is required when unit_alias is set", ErrInvalid)
		}
		if _, err := s.db.GetToolboxUnitById(ctx, parsedUnitID.Hex()); err != nil {
			if errors.Is(err, mongorepo.ToolboxEntryNotFoundError) {
				return models.SubmitNutritionLinkResponse{}, ErrToolboxEntryNotFound
			}
			return models.SubmitNutritionLinkResponse{}, err
		}
		unitID = &parsedUnitID
		if existing, err := s.db.GetUnitAliasByAliasLower(ctx, strings.ToLower(unitAlias)); err == nil {
			existingUnitAlias = &existing
		} else if !errors.Is(err, mongorepo.NutritionEntryNotFoundError) {
			return models.SubmitNutritionLinkResponse{}, err
		}
	}

	// Merge first: what would actually end up stored, whichever path this
	// takes. A field left out of the request falls back to whatever the
	// existing link already has (nil if there's no existing link either).
	mergedGPer100ml := req.GPer100ml
	if mergedGPer100ml == nil && existingLink != nil {
		mergedGPer100ml = existingLink.GPer100ml
	}
	mergedGramsPerUnit := req.GramsPerUnit
	if mergedGramsPerUnit == nil && existingLink != nil {
		mergedGramsPerUnit = existingLink.GramsPerUnit
	}

	if err := s.validateIngredientUnitRequirement(ctx, req.IngredientUnit, mergedGPer100ml, mergedGramsPerUnit); err != nil {
		return models.SubmitNutritionLinkResponse{}, err
	}

	needsReview := needsNutritionLinkReview(existingLink, nutritionID, req.GPer100ml, req.GramsPerUnit) ||
		(unitID != nil && existingUnitAlias != nil && existingUnitAlias.UnitID != *unitID)

	if !needsReview {
		var link models.IngredientNutritionLink
		if existingLink == nil {
			link, err = s.db.CreateIngredientNutritionLink(ctx, name, nutritionID, mergedGPer100ml, mergedGramsPerUnit)
		} else {
			link, err = s.db.UpdateIngredientNutritionLink(ctx, existingLink.Id.Hex(), name, nutritionID, mergedGPer100ml, mergedGramsPerUnit)
		}
		if err != nil {
			return models.SubmitNutritionLinkResponse{}, err
		}
		if unitID != nil && existingUnitAlias == nil {
			if _, err := s.db.CreateUnitAlias(ctx, unitAlias, *unitID); err != nil {
				return models.SubmitNutritionLinkResponse{}, err
			}
		}
		return models.SubmitNutritionLinkResponse{Applied: true, Link: &link}, nil
	}

	var linkTargetID *primitive.ObjectID
	if existingLink != nil {
		linkTargetID = existingLink.Id
	}
	suggestion := models.NutritionSuggestion{
		SubmittedBy: submittedBy, Note: note, TargetID: linkTargetID,
		IngredientName: name, NutritionID: nutritionID, GPer100ml: mergedGPer100ml, GramsPerUnit: mergedGramsPerUnit,
	}
	if unitID != nil {
		suggestion.UnitAlias = unitAlias
		suggestion.UnitID = unitID
		if existingUnitAlias != nil {
			suggestion.UnitTargetID = existingUnitAlias.Id
		}
	}
	created, err := s.db.CreateNutritionSuggestion(ctx, suggestion)
	if err != nil {
		return models.SubmitNutritionLinkResponse{}, err
	}
	return models.SubmitNutritionLinkResponse{Applied: false, Suggestion: &created}, nil
}

// needsNutritionLinkReview reports whether applying nutritionID/gPer100ml/
// gramsPerUnit on top of existing would overwrite a value existing already
// had - a nil existing (no link yet) never needs review, and a field the
// request leaves blank never counts as an overwrite of that field.
func needsNutritionLinkReview(existing *models.IngredientNutritionLink, nutritionID primitive.ObjectID, gPer100ml, gramsPerUnit *float64) bool {
	if existing == nil {
		return false
	}
	if existing.NutritionID != nutritionID {
		return true
	}
	if existing.GPer100ml != nil && gPer100ml != nil && *existing.GPer100ml != *gPer100ml {
		return true
	}
	if existing.GramsPerUnit != nil && gramsPerUnit != nil && *existing.GramsPerUnit != *gramsPerUnit {
		return true
	}
	return false
}

// validateIngredientUnitRequirement enforces that a link submitted through
// the contextual authoring/detail-page flows (the only callers that know
// one specific recipe ingredient's current unit) will actually resolve:
// mirrors resolveIngredientGrams's own resolution order, so a weight unit
// needs neither field, a volume unit needs a density, and a blank or
// unrecognized unit needs a per-unit weight. The admin CRUD endpoints never
// call this - a link there isn't tied to any one ingredient's unit.
func (s *Service) validateIngredientUnitRequirement(ctx context.Context, unitText string, gPer100ml, gramsPerUnit *float64) error {
	unitText = strings.TrimSpace(unitText)
	if unitText != "" {
		units, err := s.db.ListToolboxUnits(ctx)
		if err != nil {
			return err
		}
		if unit, ok := s.resolveToolboxUnit(ctx, unitText, units); ok {
			if unit.Kind == models.ToolboxUnitWeight {
				return nil
			}
			if gPer100ml == nil {
				return fmt.Errorf("%w: this ingredient's unit is measured by volume - a density (grams per 100ml) is required to link it", ErrInvalid)
			}
			return nil
		}
	}
	if gramsPerUnit == nil {
		return fmt.Errorf("%w: this ingredient has no recognized unit - a per-unit weight (grams per unit) is required to link it", ErrInvalid)
	}
	return nil
}

// --- Admin direct CRUD (links + unit aliases) ---

// AdminCreateNutritionLink directly inserts a new link - admin-only, always
// applies immediately regardless of whether the name already has one (an
// admin correcting an existing link should use AdminUpdateNutritionLink
// instead; this still succeeds either way since the collection has no
// application-level duplicate guard here, but a second link for the same
// name would just be shadowed by whichever resolveIngredientGrams' lookup
// returns first - callers should update, not re-create, for a correction).
func (s *Service) AdminCreateNutritionLink(ctx context.Context, req models.AdminNutritionLinkRequest) (models.IngredientNutritionLink, error) {
	name, nutritionID, err := s.validateNutritionLinkFields(ctx, req.IngredientName, req.NutritionID, req.GPer100ml, req.GramsPerUnit)
	if err != nil {
		return models.IngredientNutritionLink{}, err
	}
	return s.db.CreateIngredientNutritionLink(ctx, name, nutritionID, req.GPer100ml, req.GramsPerUnit)
}

// AdminUpdateNutritionLink directly overwrites an existing link - admin-only.
func (s *Service) AdminUpdateNutritionLink(ctx context.Context, id string, req models.AdminNutritionLinkRequest) (models.IngredientNutritionLink, error) {
	name, nutritionID, err := s.validateNutritionLinkFields(ctx, req.IngredientName, req.NutritionID, req.GPer100ml, req.GramsPerUnit)
	if err != nil {
		return models.IngredientNutritionLink{}, err
	}
	updated, err := s.db.UpdateIngredientNutritionLink(ctx, id, name, nutritionID, req.GPer100ml, req.GramsPerUnit)
	if err != nil {
		return models.IngredientNutritionLink{}, nutritionEntryError(err)
	}
	return updated, nil
}

// AdminDeleteNutritionLink removes a link outright - admin-only.
func (s *Service) AdminDeleteNutritionLink(ctx context.Context, id string) error {
	if err := s.db.DeleteIngredientNutritionLink(ctx, id); err != nil {
		return nutritionEntryError(err)
	}
	return nil
}

func (s *Service) validateNutritionLinkFields(ctx context.Context, ingredientName, nutritionIDHex string, gPer100ml, gramsPerUnit *float64) (string, primitive.ObjectID, error) {
	name := strings.TrimSpace(ingredientName)
	if name == "" || len(name) > maxNutritionNameLength {
		return "", primitive.ObjectID{}, fmt.Errorf("%w: ingredient name must be between 1 and %d characters", ErrInvalid, maxNutritionNameLength)
	}
	nutritionID, err := primitive.ObjectIDFromHex(nutritionIDHex)
	if err != nil {
		return "", primitive.ObjectID{}, fmt.Errorf("%w: invalid nutrition_id", ErrInvalid)
	}
	if _, err := s.db.GetNutritionIngredientById(ctx, nutritionID.Hex()); err != nil {
		if errors.Is(err, mongorepo.NutritionEntryNotFoundError) {
			return "", primitive.ObjectID{}, ErrNutritionIngredientNotFound
		}
		return "", primitive.ObjectID{}, err
	}
	if gPer100ml != nil && (*gPer100ml < minNutritionGPer100ml || *gPer100ml > maxNutritionGPer100ml) {
		return "", primitive.ObjectID{}, fmt.Errorf("%w: density must be between %g and %g grams per 100ml", ErrInvalid, minNutritionGPer100ml, maxNutritionGPer100ml)
	}
	if gramsPerUnit != nil && (*gramsPerUnit < minNutritionGramsPerUnit || *gramsPerUnit > maxNutritionGramsPerUnit) {
		return "", primitive.ObjectID{}, fmt.Errorf("%w: grams per unit must be between %g and %g", ErrInvalid, minNutritionGramsPerUnit, maxNutritionGramsPerUnit)
	}
	return name, nutritionID, nil
}

// ListUnitAliases returns every alias, for the admin back-office's Links tab.
func (s *Service) ListUnitAliases(ctx context.Context) ([]models.UnitAlias, error) {
	return s.db.ListUnitAliases(ctx)
}

// AdminCreateUnitAlias directly inserts a new unit alias - admin-only.
func (s *Service) AdminCreateUnitAlias(ctx context.Context, req models.AdminUnitAliasRequest) (models.UnitAlias, error) {
	alias, unitID, err := s.validateUnitAliasFields(ctx, req.Alias, req.UnitID)
	if err != nil {
		return models.UnitAlias{}, err
	}
	return s.db.CreateUnitAlias(ctx, alias, unitID)
}

// AdminUpdateUnitAlias directly overwrites an existing unit alias - admin-only.
func (s *Service) AdminUpdateUnitAlias(ctx context.Context, id string, req models.AdminUnitAliasRequest) (models.UnitAlias, error) {
	alias, unitID, err := s.validateUnitAliasFields(ctx, req.Alias, req.UnitID)
	if err != nil {
		return models.UnitAlias{}, err
	}
	updated, err := s.db.UpdateUnitAlias(ctx, id, alias, unitID)
	if err != nil {
		return models.UnitAlias{}, nutritionEntryError(err)
	}
	return updated, nil
}

// AdminDeleteUnitAlias removes a unit alias outright - admin-only.
func (s *Service) AdminDeleteUnitAlias(ctx context.Context, id string) error {
	if err := s.db.DeleteUnitAlias(ctx, id); err != nil {
		return nutritionEntryError(err)
	}
	return nil
}

func (s *Service) validateUnitAliasFields(ctx context.Context, alias, unitIDHex string) (string, primitive.ObjectID, error) {
	trimmed := strings.TrimSpace(alias)
	if trimmed == "" || len(trimmed) > maxNutritionNameLength {
		return "", primitive.ObjectID{}, fmt.Errorf("%w: alias must be between 1 and %d characters", ErrInvalid, maxNutritionNameLength)
	}
	unitID, err := primitive.ObjectIDFromHex(unitIDHex)
	if err != nil {
		return "", primitive.ObjectID{}, fmt.Errorf("%w: invalid unit_id", ErrInvalid)
	}
	if _, err := s.db.GetToolboxUnitById(ctx, unitID.Hex()); err != nil {
		if errors.Is(err, mongorepo.ToolboxEntryNotFoundError) {
			return "", primitive.ObjectID{}, ErrToolboxEntryNotFound
		}
		return "", primitive.ObjectID{}, err
	}
	return trimmed, unitID, nil
}

// ListNutritionSuggestionsForAdmin resolves each suggestion's submitter
// username plus display context (target nutrition entry's name, unit name,
// and - for a correction - what it would replace) for the admin review tab.
func (s *Service) ListNutritionSuggestionsForAdmin(ctx context.Context, status string, limit, offset int64) (models.ListNutritionSuggestionsResponse, error) {
	suggestions, total, err := s.db.ListNutritionSuggestions(ctx, status, limit, offset)
	if err != nil {
		return models.ListNutritionSuggestionsResponse{}, err
	}
	views := make([]models.NutritionSuggestionView, 0, len(suggestions))
	if len(suggestions) == 0 {
		return models.ListNutritionSuggestionsResponse{Length: total, Items: views}, nil
	}

	userIDs := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		userIDs = append(userIDs, suggestion.SubmittedBy.Hex())
	}
	users, err := s.db.GetUsersByIDs(ctx, userIDs)
	if err != nil {
		return models.ListNutritionSuggestionsResponse{}, err
	}

	for _, suggestion := range suggestions {
		view := models.NutritionSuggestionView{
			Id: suggestion.Id, TargetID: suggestion.TargetID, IsCorrection: suggestion.TargetID != nil,
			SubmittedBy: suggestion.SubmittedBy, SubmittedByUsername: users[suggestion.SubmittedBy.Hex()].Username,
			Note: suggestion.Note, Status: suggestion.Status, CreatedAt: suggestion.CreatedAt, ReviewedAt: suggestion.ReviewedAt,
			IngredientName: suggestion.IngredientName, NutritionID: suggestion.NutritionID,
			GPer100ml: suggestion.GPer100ml, GramsPerUnit: suggestion.GramsPerUnit,
			UnitAlias: suggestion.UnitAlias, IsUnitCorrection: suggestion.UnitTargetID != nil,
		}
		if nutrition, err := s.db.GetNutritionIngredientById(ctx, suggestion.NutritionID.Hex()); err == nil {
			view.NutritionName = nutrition.Name
		}
		if suggestion.UnitID != nil {
			if unit, err := s.db.GetToolboxUnitById(ctx, suggestion.UnitID.Hex()); err == nil {
				view.UnitName = unit.Name
			}
		}
		if suggestion.TargetID != nil {
			if link, err := s.db.GetIngredientNutritionLinkById(ctx, suggestion.TargetID.Hex()); err == nil {
				view.CurrentName = link.Name
			}
		}
		views = append(views, view)
	}
	return models.ListNutritionSuggestionsResponse{Length: total, Items: views}, nil
}

// ApproveNutritionSuggestion applies a submitted addition/correction to the
// live link tables: updates the target link (and unit alias, if any) in
// place for a correction, otherwise inserts new entries.
func (s *Service) ApproveNutritionSuggestion(ctx context.Context, id, adminID string) (models.NutritionSuggestion, error) {
	suggestion, err := s.db.GetNutritionSuggestionById(ctx, id)
	if err != nil {
		if errors.Is(err, mongorepo.NutritionSuggestionNotFoundError) {
			return models.NutritionSuggestion{}, ErrNutritionSuggestionNotFound
		}
		return models.NutritionSuggestion{}, err
	}

	if suggestion.TargetID != nil {
		if _, err := s.db.UpdateIngredientNutritionLink(ctx, suggestion.TargetID.Hex(), suggestion.IngredientName, suggestion.NutritionID, suggestion.GPer100ml, suggestion.GramsPerUnit); err != nil {
			return models.NutritionSuggestion{}, nutritionEntryError(err)
		}
	} else if _, err := s.db.CreateIngredientNutritionLink(ctx, suggestion.IngredientName, suggestion.NutritionID, suggestion.GPer100ml, suggestion.GramsPerUnit); err != nil {
		return models.NutritionSuggestion{}, err
	}

	if suggestion.UnitAlias != "" && suggestion.UnitID != nil {
		if suggestion.UnitTargetID != nil {
			if _, err := s.db.UpdateUnitAlias(ctx, suggestion.UnitTargetID.Hex(), suggestion.UnitAlias, *suggestion.UnitID); err != nil {
				return models.NutritionSuggestion{}, nutritionEntryError(err)
			}
		} else if _, err := s.db.CreateUnitAlias(ctx, suggestion.UnitAlias, *suggestion.UnitID); err != nil {
			return models.NutritionSuggestion{}, err
		}
	}

	if err := s.db.UpdateNutritionSuggestionStatus(ctx, id, models.NutritionSuggestionApproved, adminID); err != nil {
		return models.NutritionSuggestion{}, err
	}
	suggestion.Status = models.NutritionSuggestionApproved
	return suggestion, nil
}

func nutritionEntryError(err error) error {
	if errors.Is(err, mongorepo.NutritionEntryNotFoundError) {
		return ErrNutritionIngredientNotFound
	}
	return err
}

// RejectNutritionSuggestion dismisses a submitted addition/correction with
// no further effect.
func (s *Service) RejectNutritionSuggestion(ctx context.Context, id, adminID string) (models.NutritionSuggestion, error) {
	suggestion, err := s.db.GetNutritionSuggestionById(ctx, id)
	if err != nil {
		if errors.Is(err, mongorepo.NutritionSuggestionNotFoundError) {
			return models.NutritionSuggestion{}, ErrNutritionSuggestionNotFound
		}
		return models.NutritionSuggestion{}, err
	}
	if err := s.db.UpdateNutritionSuggestionStatus(ctx, id, models.NutritionSuggestionRejected, adminID); err != nil {
		return models.NutritionSuggestion{}, err
	}
	suggestion.Status = models.NutritionSuggestionRejected
	return suggestion, nil
}

// nutritionTotals is six running macro sums, used both for a whole recipe's
// aggregate and for one ingredient line's own contribution - the same shape
// either way, since a recipe_ref line's contribution *is* another recipe's
// aggregate (see computeRecipeNutrition).
type nutritionTotals struct {
	Kcal, Protein, Carbs, Fat, Salt, Sugar float64
}

func (t nutritionTotals) addScaled(other nutritionTotals, factor float64) nutritionTotals {
	return nutritionTotals{
		Kcal: t.Kcal + other.Kcal*factor, Protein: t.Protein + other.Protein*factor,
		Carbs: t.Carbs + other.Carbs*factor, Fat: t.Fat + other.Fat*factor,
		Salt: t.Salt + other.Salt*factor, Sugar: t.Sugar + other.Sugar*factor,
	}
}

func (t nutritionTotals) scale(factor float64) nutritionTotals {
	return nutritionTotals{Kcal: t.Kcal * factor, Protein: t.Protein * factor, Carbs: t.Carbs * factor, Fat: t.Fat * factor, Salt: t.Salt * factor, Sugar: t.Sugar * factor}
}

func (t nutritionTotals) round1() nutritionTotals {
	r := func(v float64) float64 { return math.Round(v*10) / 10 }
	return nutritionTotals{Kcal: r(t.Kcal), Protein: r(t.Protein), Carbs: r(t.Carbs), Fat: r(t.Fat), Salt: r(t.Salt), Sugar: r(t.Sugar)}
}

// nutritionUnitContext bundles the Toolbox unit list and ingredient-density
// map computeRecipeNutrition needs on every call (including recursive ones
// into a recipe_ref target) - fetched once per top-level GetRecipeNutrition
// call rather than refetched at every recursion depth.
type nutritionUnitContext struct {
	units                     []models.ToolboxUnit
	toolboxDensityByNameLower map[string]float64
}

// GetRecipeNutrition computes best-effort nutrition totals for one recipe at
// the given serving count (0 or negative means "the recipe's own base
// servings", i.e. no scaling). Always matches against the recipe's
// canonical (source-locale) ingredient text, regardless of which locale a
// caller displays - see docs/nutrition.md "Locale".
func (s *Service) GetRecipeNutrition(ctx context.Context, recipeID string, servings int) (models.RecipeNutrition, error) {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return models.RecipeNutrition{}, ErrNotFound
	}
	recipe, err := s.db.GetRecipeById(recipeID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return models.RecipeNutrition{}, ErrNotFound
		}
		return models.RecipeNutrition{}, err
	}
	if servings <= 0 {
		servings = recipe.Quantity
	}
	ratio := 1.0
	if recipe.Quantity > 0 {
		ratio = float64(servings) / float64(recipe.Quantity)
	}

	units, err := s.db.ListToolboxUnits(ctx)
	if err != nil {
		return models.RecipeNutrition{}, err
	}
	toolboxIngredients, err := s.db.ListToolboxIngredients(ctx)
	if err != nil {
		return models.RecipeNutrition{}, err
	}
	toolboxDensityByNameLower := make(map[string]float64, len(toolboxIngredients))
	for _, ti := range toolboxIngredients {
		toolboxDensityByNameLower[ti.NameLower] = ti.GPer100ml
	}
	unitCtx := nutritionUnitContext{units: units, toolboxDensityByNameLower: toolboxDensityByNameLower}

	// Seeded with the recipe's own id: a (malformed) direct self-reference is
	// caught the same way an indirect cycle is, by the same visited check.
	visited := map[string]bool{recipeID: true}
	totals, lines, matched := s.computeRecipeNutrition(ctx, recipe, unitCtx, visited)

	scaledLines := make([]models.IngredientNutrition, len(lines))
	for i, line := range lines {
		scaled := nutritionTotals{Kcal: line.Kcal, Protein: line.ProteinG, Carbs: line.CarbsG, Fat: line.FatG, Salt: line.SaltG, Sugar: line.SugarG}.scale(ratio).round1()
		scaledLines[i] = models.IngredientNutrition{
			Matched: line.Matched, Kcal: scaled.Kcal, ProteinG: scaled.Protein, CarbsG: scaled.Carbs,
			FatG: scaled.Fat, SaltG: scaled.Salt, SugarG: scaled.Sugar,
		}
	}
	scaledTotals := totals.scale(ratio).round1()

	return models.RecipeNutrition{
		Servings: servings, Kcal: scaledTotals.Kcal, ProteinG: scaledTotals.Protein, CarbsG: scaledTotals.Carbs,
		FatG: scaledTotals.Fat, SaltG: scaledTotals.Salt, SugarG: scaledTotals.Sugar,
		MatchedCount: matched, TotalCount: len(recipe.Ingredients), Ingredients: scaledLines,
	}, nil
}

// computeRecipeNutrition sums recipe's ingredients at recipe's own base
// servings (no ratio applied - GetRecipeNutrition scales the result once at
// the end, including for a recipe_ref line's recursed-into contribution).
// A recipe_ref ingredient recurses into the referenced recipe's own totals,
// scaled by "how many batches" (the ingredient's Quantity, or 1 when unset -
// see recipes.ts's getReferenceQuantity: a blank quantity means "use the
// whole recipe") - but only when Unit doesn't name an actual weight/volume
// unit, see computeRecipeRefLine. It counts as matched only if that
// recursion itself matched anything - a reference to a recipe with zero
// linkable ingredients contributes nothing, same as any other unresolvable
// line. visited guards
// against a reference cycle (should already be impossible via the write-time
// guards in service.go, but this is what actually prevents infinite
// recursion if one ever slips through): a recipe already on the current
// path is treated as unmatched rather than recursed into again.
func (s *Service) computeRecipeNutrition(ctx context.Context, recipe models.Recipe, unitCtx nutritionUnitContext, visited map[string]bool) (nutritionTotals, []models.IngredientNutrition, int) {
	var totals nutritionTotals
	matched := 0
	lines := make([]models.IngredientNutrition, len(recipe.Ingredients))

	for i, ingredient := range recipe.Ingredients {
		if ingredient.RecipeRef != nil {
			line, ok := s.computeRecipeRefLine(ctx, ingredient, unitCtx, visited)
			lines[i] = line
			if ok {
				matched++
				totals = totals.addScaled(nutritionTotals{Kcal: line.Kcal, Protein: line.ProteinG, Carbs: line.CarbsG, Fat: line.FatG, Salt: line.SaltG, Sugar: line.SugarG}, 1)
			}
			continue
		}
		if strings.TrimSpace(ingredient.Name) == "" {
			continue
		}
		link, err := s.db.GetIngredientNutritionLinkByNameLower(ctx, strings.ToLower(ingredient.Name))
		if err != nil {
			continue
		}
		grams, ok := s.resolveIngredientGrams(ctx, ingredient, link, unitCtx.units, unitCtx.toolboxDensityByNameLower)
		if !ok {
			continue
		}
		nutrition, err := s.db.GetNutritionIngredientById(ctx, link.NutritionID.Hex())
		if err != nil {
			continue
		}
		matched++
		factor := grams / 100
		line := models.IngredientNutrition{
			Matched: true, Kcal: factor * nutrition.KcalPer100g, ProteinG: factor * nutrition.ProteinG,
			CarbsG: factor * nutrition.CarbsG, FatG: factor * nutrition.FatG, SaltG: factor * nutrition.SaltG, SugarG: factor * nutrition.SugarG,
		}
		lines[i] = line
		totals = totals.addScaled(nutritionTotals{Kcal: line.Kcal, Protein: line.ProteinG, Carbs: line.CarbsG, Fat: line.FatG, Salt: line.SaltG, Sugar: line.SugarG}, 1)
	}
	return totals, lines, matched
}

// computeRecipeRefLine resolves one recipe_ref ingredient's own contribution:
// the referenced recipe's full nutrition (at its own base servings) times
// how many batches this ingredient calls for. "Batches" is only a defined
// reading of Quantity when Unit doesn't name an actual weight/volume unit
// (blank, or free text like "batch"/"batches" that isn't a registered
// ToolboxUnit) - a quantity given in a real weight or volume unit (e.g. "20g"
// of a sauce sub-recipe) means a fraction of one batch, but nothing tracks a
// recipe's total yield weight/volume to compute that fraction from, so
// rather than silently reading "20g" as "20 batches" (wildly overcounting -
// the referenced recipe's *entire* nutrition, ×20), that case is left
// unmatched, same as any other quantity this feature can't resolve.
func (s *Service) computeRecipeRefLine(ctx context.Context, ingredient models.Ingredient, unitCtx nutritionUnitContext, visited map[string]bool) (models.IngredientNutrition, bool) {
	refID := ingredient.RecipeRef.Hex()
	if visited[refID] {
		return models.IngredientNutrition{}, false
	}
	if unitText := strings.TrimSpace(ingredient.Unit); unitText != "" {
		if _, ok := s.resolveToolboxUnit(ctx, unitText, unitCtx.units); ok {
			return models.IngredientNutrition{}, false
		}
	}
	referenced, err := s.db.GetRecipeById(refID)
	if err != nil {
		return models.IngredientNutrition{}, false
	}
	batches := ingredient.Quantity
	if batches <= 0 {
		batches = 1
	}

	childVisited := make(map[string]bool, len(visited)+1)
	for id := range visited {
		childVisited[id] = true
	}
	childVisited[refID] = true

	subTotals, _, subMatched := s.computeRecipeNutrition(ctx, referenced, unitCtx, childVisited)
	if subMatched == 0 {
		return models.IngredientNutrition{}, false
	}
	scaled := subTotals.scale(batches)
	return models.IngredientNutrition{
		Matched: true, Kcal: scaled.Kcal, ProteinG: scaled.Protein, CarbsG: scaled.Carbs,
		FatG: scaled.Fat, SaltG: scaled.Salt, SugarG: scaled.Sugar,
	}, true
}

// resolveIngredientGrams converts one ingredient's quantity to grams via its
// link, in the order described in docs/nutrition.md "Resolving a quantity
// to grams": a resolvable weight/volume unit first, falling back to a
// per-unit count when the unit is blank or unrecognized, else unresolved.
func (s *Service) resolveIngredientGrams(ctx context.Context, ingredient models.Ingredient, link models.IngredientNutritionLink, units []models.ToolboxUnit, toolboxDensityByNameLower map[string]float64) (float64, bool) {
	if ingredient.Quantity <= 0 {
		return 0, false
	}
	unitText := strings.TrimSpace(ingredient.Unit)
	if unitText != "" {
		if unit, ok := s.resolveToolboxUnit(ctx, unitText, units); ok {
			if unit.Kind == models.ToolboxUnitWeight {
				return ingredient.Quantity * unit.ToBase, true
			}
			density := link.GPer100ml
			if density == nil {
				if d, ok := toolboxDensityByNameLower[strings.ToLower(ingredient.Name)]; ok {
					density = &d
				}
			}
			if density == nil {
				return 0, false
			}
			return ingredient.Quantity * unit.ToBase * (*density) / 100, true
		}
	}
	if link.GramsPerUnit != nil {
		return ingredient.Quantity * (*link.GramsPerUnit), true
	}
	return 0, false
}

// resolveToolboxUnit resolves free text to a ToolboxUnit: first a direct
// case-insensitive match against the unit's own Name/Symbol (covers common
// abbreviations like "g"/"cup" for free), falling back to a UnitAlias
// lookup for anything else ("grammes", "tasse").
func (s *Service) resolveToolboxUnit(ctx context.Context, text string, units []models.ToolboxUnit) (models.ToolboxUnit, bool) {
	q := strings.ToLower(text)
	for _, unit := range units {
		if strings.ToLower(unit.Name) == q || strings.ToLower(unit.Symbol) == q {
			return unit, true
		}
	}
	alias, err := s.db.GetUnitAliasByAliasLower(ctx, q)
	if err != nil {
		return models.ToolboxUnit{}, false
	}
	for _, unit := range units {
		if unit.Id != nil && *unit.Id == alias.UnitID {
			return unit, true
		}
	}
	return models.ToolboxUnit{}, false
}
