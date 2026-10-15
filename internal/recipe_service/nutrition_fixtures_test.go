package recipe_service

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

// nutritionFixtureFile is shared with Saute-web's client-side port of
// computeRecipeNutrition, so both implementations are held to the same
// expected values.
type nutritionFixtureFile struct {
	Data struct {
		Units              []models.ToolboxUnit             `json:"units"`
		ToolboxIngredients []models.ToolboxIngredient       `json:"toolbox_ingredients"`
		UnitAliases        []models.UnitAlias               `json:"unit_aliases"`
		Links              []models.IngredientNutritionLink `json:"links"`
		Foods              []models.NutritionIngredient     `json:"foods"`
	} `json:"data"`
	Cases []struct {
		Name     string                  `json:"name"`
		Recipe   models.Recipe           `json:"recipe"`
		Servings int                     `json:"servings"`
		Expected *models.RecipeNutrition `json:"expected"`
	} `json:"cases"`
}

func TestGetRecipeNutritionSharedFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/nutrition_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures nutritionFixtureFile
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	data := fixtures.Data
	for i := range data.ToolboxIngredients {
		data.ToolboxIngredients[i].NameLower = strings.ToLower(data.ToolboxIngredients[i].Name)
	}

	for _, tc := range fixtures.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			recipeID := primitive.NewObjectID()
			recipe := tc.Recipe
			recipe.Id = &recipeID
			store := &fakeStore{
				getRecipeByIdFn:          func(string) (models.Recipe, error) { return recipe, nil },
				listToolboxUnitsFn:       func() ([]models.ToolboxUnit, error) { return data.Units, nil },
				listToolboxIngredientsFn: func() ([]models.ToolboxIngredient, error) { return data.ToolboxIngredients, nil },
				getIngredientNutritionLinkByNameLowerFn: func(nameLower string) (models.IngredientNutritionLink, error) {
					for _, link := range data.Links {
						if strings.ToLower(link.Name) == nameLower {
							return link, nil
						}
					}
					return models.IngredientNutritionLink{}, mongorepo.NutritionEntryNotFoundError
				},
				getUnitAliasByAliasLowerFn: func(aliasLower string) (models.UnitAlias, error) {
					for _, alias := range data.UnitAliases {
						if strings.ToLower(alias.Alias) == aliasLower {
							return alias, nil
						}
					}
					return models.UnitAlias{}, mongorepo.NutritionEntryNotFoundError
				},
				getNutritionIngredientByIdFn: func(id string) (models.NutritionIngredient, error) {
					for _, food := range data.Foods {
						if food.Id != nil && food.Id.Hex() == id {
							return food, nil
						}
					}
					return models.NutritionIngredient{}, mongorepo.NutritionEntryNotFoundError
				},
			}
			s := &Service{db: store}

			got, err := s.GetRecipeNutrition(context.Background(), recipeID.Hex(), tc.Servings)
			if err != nil {
				t.Fatalf("GetRecipeNutrition: %v", err)
			}
			if tc.Expected == nil || !reflect.DeepEqual(got, *tc.Expected) {
				actual, _ := json.Marshal(got)
				t.Fatalf("got %s", actual)
			}
		})
	}
}
