package recipe_service

import (
	"context"
	"slices"
	"strings"

	"recipes/internal/models"
)

// ListFoods returns the whole food list, for client-side matching.
func (s *Service) ListFoods(ctx context.Context) ([]models.FoodItem, error) {
	return s.db.ListFoods(ctx)
}

// FindFood returns the food with name as a name or alias in any locale.
func (s *Service) FindFood(ctx context.Context, name string) (models.FoodItem, bool, error) {
	foods, err := s.db.ListFoods(ctx)
	if err != nil {
		return models.FoodItem{}, false, err
	}
	food, ok := newFoodIndex(foods, "").match(name)
	return food, ok, nil
}

// foodIndex finds a food by id, or by an exact, case-insensitive name or
// alias in one locale.
type foodIndex struct {
	byID   map[string]models.FoodItem
	byName map[string]string
}

// newFoodIndex indexes foods' names and aliases in locale's base language.
// A locale with no food names (blank, or one the list doesn't cover)
// searches every locale, in models.FoodLocales order.
func newFoodIndex(foods []models.FoodItem, locale string) foodIndex {
	index := foodIndex{byID: make(map[string]models.FoodItem, len(foods)), byName: make(map[string]string)}
	for _, food := range foods {
		index.byID[food.Id] = food
	}
	locales := models.FoodLocales
	if base, _, _ := strings.Cut(locale, "-"); slices.Contains(models.FoodLocales, base) {
		locales = []string{base}
	}
	add := func(name, id string) {
		key := foodNameKey(name)
		if _, taken := index.byName[key]; key != "" && !taken {
			index.byName[key] = id
		}
	}
	for _, l := range locales {
		for _, food := range foods {
			add(food.Names[l], food.Id)
			for _, alias := range food.Aliases[l] {
				add(alias, food.Id)
			}
		}
	}
	return index
}

func foodNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (i foodIndex) food(id string) (models.FoodItem, bool) {
	food, ok := i.byID[id]
	return food, ok
}

func (i foodIndex) match(name string) (models.FoodItem, bool) {
	id, ok := i.byName[foodNameKey(name)]
	if !ok {
		return models.FoodItem{}, false
	}
	return i.food(id)
}

// ingredientFood is the food a recipe ingredient resolves to, with its
// link's overrides already applied over the food's defaults.
type ingredientFood struct {
	food          models.FoodItem
	gPer100ml     *float64
	gramsPerPiece *float64
}

// matchIngredientFood resolves an ingredient name: its link first, else a
// food named that way in index's locale.
func (s *Service) matchIngredientFood(ctx context.Context, name string, index foodIndex) (ingredientFood, bool) {
	if link, err := s.db.GetIngredientNutritionLinkByNameLower(ctx, foodNameKey(name)); err == nil {
		food, ok := index.food(link.FoodID)
		if !ok {
			return ingredientFood{}, false
		}
		return ingredientFood{food: food, gPer100ml: firstSet(link.GPer100ml, food.GPer100ml), gramsPerPiece: firstSet(link.GramsPerUnit, food.GramsPerPiece)}, true
	}
	food, ok := index.match(name)
	if !ok {
		return ingredientFood{}, false
	}
	return ingredientFood{food: food, gPer100ml: food.GPer100ml, gramsPerPiece: food.GramsPerPiece}, true
}

func firstSet(values ...*float64) *float64 {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// foodDisplayName is food's English name, else its id.
func foodDisplayName(food models.FoodItem) string {
	if name := food.Names["en"]; name != "" {
		return name
	}
	return food.Id
}
