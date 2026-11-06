package recipe_service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

const (
	maxFoodNameLength   = 100
	maxFoodAliases      = 30
	maxFoodKcalPer100g  = 900.0
	maxFoodGramsPer100g = 100.0
)

var foodIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// AdminCreateFood adds a food to the list.
func (s *Service) AdminCreateFood(ctx context.Context, req models.AdminFoodRequest) (models.FoodItem, error) {
	id := strings.TrimSpace(req.Id)
	if !foodIDPattern.MatchString(id) || len(id) > maxFoodNameLength {
		return models.FoodItem{}, fmt.Errorf("%w: id must be lowercase letters, digits and single dashes", ErrInvalid)
	}
	food, err := s.validateFood(ctx, id, req)
	if err != nil {
		return models.FoodItem{}, err
	}
	food.CreatedAt = time.Now().UTC()
	if err := s.db.CreateFood(ctx, food); err != nil {
		if errors.Is(err, mongorepo.FoodExistsError) {
			return models.FoodItem{}, fmt.Errorf("%w: id %q is taken", ErrFoodExists, id)
		}
		return models.FoodItem{}, err
	}
	return food, nil
}

// AdminUpdateFood overwrites an existing food; its id never changes.
func (s *Service) AdminUpdateFood(ctx context.Context, id string, req models.AdminFoodRequest) (models.FoodItem, error) {
	food, err := s.validateFood(ctx, id, req)
	if err != nil {
		return models.FoodItem{}, err
	}
	updated, err := s.db.UpdateFood(ctx, food)
	if errors.Is(err, mongorepo.FoodNotFoundError) {
		return models.FoodItem{}, ErrFoodNotFound
	}
	return updated, err
}

// AdminDeleteFood removes a food no ingredient link points at.
func (s *Service) AdminDeleteFood(ctx context.Context, id string) error {
	count, err := s.db.CountLinksForFood(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: %d ingredient link(s) point at it", ErrFoodInUse, count)
	}
	if err := s.db.DeleteFood(ctx, id); err != nil {
		if errors.Is(err, mongorepo.FoodNotFoundError) {
			return ErrFoodNotFound
		}
		return err
	}
	return nil
}

// validateFood normalizes req into the food stored under id: trimmed names,
// aliases trimmed, de-duplicated and without blanks, values in range, and
// no name or alias another food already uses in the same locale.
func (s *Service) validateFood(ctx context.Context, id string, req models.AdminFoodRequest) (models.FoodItem, error) {
	food := models.FoodItem{
		Id: id, Names: map[string]string{}, Aliases: map[string][]string{},
		GramsPerPiece: req.GramsPerPiece, GPer100ml: req.GPer100ml,
		KcalPer100g: req.KcalPer100g, ProteinG: req.ProteinG, CarbsG: req.CarbsG,
		FatG: req.FatG, SaltG: req.SaltG, SugarG: req.SugarG,
	}
	for locale, name := range req.Names {
		if !slices.Contains(models.FoodLocales, locale) {
			return models.FoodItem{}, fmt.Errorf("%w: unsupported locale %q", ErrInvalid, locale)
		}
		if name = strings.TrimSpace(name); name != "" {
			if len(name) > maxFoodNameLength {
				return models.FoodItem{}, fmt.Errorf("%w: names must be at most %d characters", ErrInvalid, maxFoodNameLength)
			}
			food.Names[locale] = name
		}
	}
	if food.Names["en"] == "" {
		return models.FoodItem{}, fmt.Errorf("%w: an English name is required", ErrInvalid)
	}
	for locale, aliases := range req.Aliases {
		if !slices.Contains(models.FoodLocales, locale) {
			return models.FoodItem{}, fmt.Errorf("%w: unsupported locale %q", ErrInvalid, locale)
		}
		seen := map[string]bool{foodNameKey(food.Names[locale]): true}
		cleaned := []string{}
		for _, alias := range aliases {
			alias = strings.TrimSpace(alias)
			if alias == "" || seen[foodNameKey(alias)] {
				continue
			}
			if len(alias) > maxFoodNameLength {
				return models.FoodItem{}, fmt.Errorf("%w: aliases must be at most %d characters", ErrInvalid, maxFoodNameLength)
			}
			seen[foodNameKey(alias)] = true
			cleaned = append(cleaned, alias)
		}
		if len(cleaned) > maxFoodAliases {
			return models.FoodItem{}, fmt.Errorf("%w: at most %d aliases per language", ErrInvalid, maxFoodAliases)
		}
		food.Aliases[locale] = cleaned
	}
	if food.KcalPer100g < 0 || food.KcalPer100g > maxFoodKcalPer100g {
		return models.FoodItem{}, fmt.Errorf("%w: kcal per 100 g must be between 0 and %g", ErrInvalid, maxFoodKcalPer100g)
	}
	for _, grams := range []float64{food.ProteinG, food.CarbsG, food.FatG, food.SaltG, food.SugarG} {
		if grams < 0 || grams > maxFoodGramsPer100g {
			return models.FoodItem{}, fmt.Errorf("%w: grams per 100 g must be between 0 and %g", ErrInvalid, maxFoodGramsPer100g)
		}
	}
	if food.GPer100ml != nil && (*food.GPer100ml < minNutritionGPer100ml || *food.GPer100ml > maxNutritionGPer100ml) {
		return models.FoodItem{}, fmt.Errorf("%w: density must be between %g and %g grams per 100ml", ErrInvalid, minNutritionGPer100ml, maxNutritionGPer100ml)
	}
	if food.GramsPerPiece != nil && (*food.GramsPerPiece < minNutritionGramsPerUnit || *food.GramsPerPiece > maxNutritionGramsPerUnit) {
		return models.FoodItem{}, fmt.Errorf("%w: grams per piece must be between %g and %g", ErrInvalid, minNutritionGramsPerUnit, maxNutritionGramsPerUnit)
	}

	foods, err := s.db.ListFoods(ctx)
	if err != nil {
		return models.FoodItem{}, err
	}
	for _, locale := range models.FoodLocales {
		mine := append([]string{food.Names[locale]}, food.Aliases[locale]...)
		for _, other := range foods {
			if other.Id == id {
				continue
			}
			theirs := append([]string{other.Names[locale]}, other.Aliases[locale]...)
			for _, name := range mine {
				if name != "" && slices.ContainsFunc(theirs, func(t string) bool { return foodNameKey(t) == foodNameKey(name) }) {
					return models.FoodItem{}, fmt.Errorf("%w: %q is already a name of %q", ErrFoodExists, name, other.Id)
				}
			}
		}
	}
	return food, nil
}

// AdminCreateUnit adds a unit and translates its name in the background.
func (s *Service) AdminCreateUnit(ctx context.Context, req models.AdminUnitRequest) (models.ToolboxUnit, error) {
	req, err := validateUnit(req)
	if err != nil {
		return models.ToolboxUnit{}, err
	}
	unit, err := s.db.CreateToolboxUnit(ctx, req.Name, req.Symbol, req.Kind, req.ToBase)
	if err != nil {
		return models.ToolboxUnit{}, unitError(err)
	}
	if unit.Id != nil {
		s.scheduleToolboxTranslation(toolboxKindUnit, *unit.Id)
	}
	return unit, nil
}

// AdminUpdateUnit overwrites a unit and re-translates its name.
func (s *Service) AdminUpdateUnit(ctx context.Context, id string, req models.AdminUnitRequest) (models.ToolboxUnit, error) {
	req, err := validateUnit(req)
	if err != nil {
		return models.ToolboxUnit{}, err
	}
	unit, err := s.db.UpdateToolboxUnit(ctx, id, req.Name, req.Symbol, req.Kind, req.ToBase)
	if err != nil {
		return models.ToolboxUnit{}, unitError(err)
	}
	if unit.Id != nil {
		s.scheduleToolboxTranslation(toolboxKindUnit, *unit.Id)
	}
	return unit, nil
}

// AdminDeleteUnit removes a unit no unit alias points at.
func (s *Service) AdminDeleteUnit(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return ErrToolboxEntryNotFound
	}
	count, err := s.db.CountUnitAliasesForUnit(ctx, objectID)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: %d unit alias(es) point at it", ErrUnitInUse, count)
	}
	if err := s.db.DeleteToolboxUnit(ctx, id); err != nil {
		return unitError(err)
	}
	return nil
}

func validateUnit(req models.AdminUnitRequest) (models.AdminUnitRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Symbol = strings.TrimSpace(req.Symbol)
	if req.Name == "" || len(req.Name) > maxToolboxNameLength {
		return req, fmt.Errorf("%w: name must be between 1 and %d characters", ErrInvalid, maxToolboxNameLength)
	}
	if req.Symbol == "" || len(req.Symbol) > maxToolboxSymbolLength {
		return req, fmt.Errorf("%w: symbol must be between 1 and %d characters", ErrInvalid, maxToolboxSymbolLength)
	}
	if req.Kind != models.ToolboxUnitWeight && req.Kind != models.ToolboxUnitVolume {
		return req, fmt.Errorf("%w: kind must be weight or volume", ErrInvalid)
	}
	if req.ToBase < minToolboxUnitToBase || req.ToBase > maxToolboxUnitToBase {
		return req, fmt.Errorf("%w: to_base must be between %g and %g", ErrInvalid, minToolboxUnitToBase, maxToolboxUnitToBase)
	}
	return req, nil
}

func unitError(err error) error {
	switch {
	case errors.Is(err, mongorepo.ToolboxEntryNotFoundError):
		return ErrToolboxEntryNotFound
	case errors.Is(err, mongorepo.ToolboxUnitExistsError):
		return ErrUnitExists
	}
	return err
}
