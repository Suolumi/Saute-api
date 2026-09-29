package database

import (
	"context"
	"recipes/internal/models"

	"go.mongodb.org/mongo-driver/bson/primitive"
	mongodriver "go.mongodb.org/mongo-driver/mongo"
)

type Database interface {
	EnsureIndexes(ctx context.Context) error
	Close(ctx context.Context) error
	CreateUser(user models.UserDB) (models.UserDB, error)
	GetUsers(username string, limit, offset int) ([]models.UserDB, int64, error)
	GetUserById(id string) (models.UserDB, error)
	GetUserByIdentifier(identifier string) (models.UserDB, error)
	UpdateUserById(id string, user models.UserDB) (models.UserDB, error)
	UpdateUserInterfaceById(id string, user interface{}) (models.UserDB, error)
	DeleteUserById(id string) (models.UserDB, error)
	UserConflicts(user models.UserDB) (models.UserDB, error)
	BumpMCPAuthVersion(ctx context.Context, id string) error
	CountUsers(ctx context.Context) (int64, error)
	CountAdmins(ctx context.Context) (int64, error)

	CreateRecipe(authorId string, infos *models.CreateRecipe) (models.Recipe, error)
	AddLocaleRecipe(recipe models.Recipe, locale string) (models.Recipe, error)
	GetRecipeDocuments(parameters models.GetRecipesRequest) ([]models.Recipe, int64, error)
	GetRecipeDocumentsBoosted(ctx context.Context, userID string, parameters models.GetRecipesRequest) (favorited, rest []models.Recipe, total int64, err error)
	GetRecipesByAuthor(ctx context.Context, authorID, cursor string, limit int) ([]models.Recipe, int64, error)
	GetRecipeById(id string) (models.Recipe, error)
	GetRecipeByIdForAuthor(ctx context.Context, id, authorID string) (models.Recipe, error)
	GetRecipeByIdLocale(id string, locale string) (models.Recipe, error)
	GetTranslationsByRecipeIDs(ctx context.Context, ids []string, locale string) (map[string]models.Recipe, error)
	ReferencedPictures(ctx context.Context) (map[string]struct{}, error)
	UpdateRecipeById(id string, recipe *models.UpdateRecipeRequest) (models.Recipe, error)
	ReplaceRecipeById(ctx context.Context, id string, recipe models.RecipeDB) (models.Recipe, error)
	DeleteRecipeById(id string) (models.RecipeDB, error)
	DeleteLocalizedRecipesByID(ctx context.Context, id string) error
	RecipeConflicts(recipe models.RecipeDB) (models.RecipeDB, error)
	GetOldestVariationID(ctx context.Context, rootID string) (*primitive.ObjectID, error)
	RepointVariations(ctx context.Context, oldRootID, newRootID string) error
	PromoteRecipeToRoot(ctx context.Context, id string) error
	SetVariationOf(ctx context.Context, recipeID, rootID string) error
	GetVariationCounts(ctx context.Context, rootIDs []string) (map[string]int64, error)
	RecipeCountsByCategory(ctx context.Context) (map[string]int64, error)
	RepointRecipeReferences(ctx context.Context, oldRootID, newRootID string) error
	HasIncomingReferences(ctx context.Context, recipeID string) (bool, error)
	FamilyReferencesRecipe(ctx context.Context, familyRootID, targetID string) (bool, error)
	GetRecipeTitles(ctx context.Context, ids []string) (map[string]string, error)
	GetUsersByIDs(ctx context.Context, ids []string) (map[string]models.UserView, error)

	AddFavorite(ctx context.Context, userID, recipeID string) error
	RemoveFavorite(ctx context.Context, userID, recipeID string) error
	DeleteFavoritesByRecipeID(ctx context.Context, recipeID string) error
	GetFavoriteInfo(ctx context.Context, ids []string, userID string) (map[string]models.FavoriteInfo, error)
	GetFamilyFavoriteInfo(ctx context.Context, rootIDs []string, userID string) (map[string]models.FavoriteInfo, error)
	GetRecipeFavoriters(ctx context.Context, recipeID string, limit, offset int64) ([]models.UserView, int64, error)

	CreateTranslationSuggestion(ctx context.Context, suggestion models.TranslationSuggestion) (models.TranslationSuggestion, error)
	GetTranslationSuggestionById(ctx context.Context, id string) (models.TranslationSuggestion, error)
	ListTranslationSuggestions(ctx context.Context, status string, limit, offset int64) ([]models.TranslationSuggestion, int64, error)
	UpdateTranslationSuggestionStatus(ctx context.Context, id, status, reviewedBy string) error
	HasPendingTranslationSuggestion(ctx context.Context, recipeID, locale, userID string) (bool, error)

	UpsertTranslationOverride(ctx context.Context, override models.TranslationOverride) error
	ListTranslationOverrides(ctx context.Context, recipeID, locale string) ([]models.TranslationOverride, error)
	DeleteTranslationOverride(ctx context.Context, id string) (models.TranslationOverride, error)
	DeleteTranslationOverridesByFieldPrefix(ctx context.Context, recipeID, locale, prefix string) error

	CreateToolboxIngredient(ctx context.Context, name string, gPer100ml float64, note string) (models.ToolboxIngredient, error)
	UpdateToolboxIngredient(ctx context.Context, id string, name string, gPer100ml float64, note string) (models.ToolboxIngredient, error)
	ListToolboxIngredients(ctx context.Context) ([]models.ToolboxIngredient, error)
	GetToolboxIngredientById(ctx context.Context, id string) (models.ToolboxIngredient, error)
	GetToolboxIngredientByNameLower(ctx context.Context, nameLower string) (models.ToolboxIngredient, error)
	SetToolboxIngredientTranslations(ctx context.Context, id, sourceLocale string, translations map[string]models.ToolboxIngredientTranslation) error

	CreateToolboxUnit(ctx context.Context, name, symbol, kind string, toBase float64) (models.ToolboxUnit, error)
	UpdateToolboxUnit(ctx context.Context, id string, name, symbol, kind string, toBase float64) (models.ToolboxUnit, error)
	ListToolboxUnits(ctx context.Context) ([]models.ToolboxUnit, error)
	GetToolboxUnitById(ctx context.Context, id string) (models.ToolboxUnit, error)
	GetToolboxUnitByNameLower(ctx context.Context, nameLower string) (models.ToolboxUnit, error)
	SetToolboxUnitTranslations(ctx context.Context, id, sourceLocale string, translations map[string]models.ToolboxUnitTranslation) error

	CreateToolboxSubstitution(ctx context.Context, problem, solution, tag string) (models.ToolboxSubstitution, error)
	UpdateToolboxSubstitution(ctx context.Context, id string, problem, solution, tag string) (models.ToolboxSubstitution, error)
	ListToolboxSubstitutions(ctx context.Context) ([]models.ToolboxSubstitution, error)
	GetToolboxSubstitutionById(ctx context.Context, id string) (models.ToolboxSubstitution, error)
	GetToolboxSubstitutionByProblemLower(ctx context.Context, problemLower string) (models.ToolboxSubstitution, error)
	SetToolboxSubstitutionTranslations(ctx context.Context, id, sourceLocale string, translations map[string]models.ToolboxSubstitutionTranslation) error

	CreateToolboxSuggestion(ctx context.Context, suggestion models.ToolboxSuggestion) (models.ToolboxSuggestion, error)
	GetToolboxSuggestionById(ctx context.Context, id string) (models.ToolboxSuggestion, error)
	ListToolboxSuggestions(ctx context.Context, status, kind string, limit, offset int64) ([]models.ToolboxSuggestion, int64, error)
	UpdateToolboxSuggestionStatus(ctx context.Context, id, status, reviewedBy string) error
	SeedToolboxDefaults(ctx context.Context) error

	ListNutritionIngredients(ctx context.Context) ([]models.NutritionIngredient, error)
	GetNutritionIngredientById(ctx context.Context, id string) (models.NutritionIngredient, error)

	CreateIngredientNutritionLink(ctx context.Context, name string, nutritionID primitive.ObjectID, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error)
	UpdateIngredientNutritionLink(ctx context.Context, id string, name string, nutritionID primitive.ObjectID, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error)
	GetIngredientNutritionLinkById(ctx context.Context, id string) (models.IngredientNutritionLink, error)
	GetIngredientNutritionLinkByNameLower(ctx context.Context, nameLower string) (models.IngredientNutritionLink, error)
	ListIngredientNutritionLinks(ctx context.Context) ([]models.IngredientNutritionLink, error)
	DeleteIngredientNutritionLink(ctx context.Context, id string) error

	CreateUnitAlias(ctx context.Context, alias string, unitID primitive.ObjectID) (models.UnitAlias, error)
	UpdateUnitAlias(ctx context.Context, id string, alias string, unitID primitive.ObjectID) (models.UnitAlias, error)
	GetUnitAliasByAliasLower(ctx context.Context, aliasLower string) (models.UnitAlias, error)
	ListUnitAliases(ctx context.Context) ([]models.UnitAlias, error)
	DeleteUnitAlias(ctx context.Context, id string) error

	CreateNutritionSuggestion(ctx context.Context, suggestion models.NutritionSuggestion) (models.NutritionSuggestion, error)
	GetNutritionSuggestionById(ctx context.Context, id string) (models.NutritionSuggestion, error)
	ListNutritionSuggestions(ctx context.Context, status string, limit, offset int64) ([]models.NutritionSuggestion, int64, error)
	UpdateNutritionSuggestionStatus(ctx context.Context, id, status, reviewedBy string) error
	SeedNutritionDefaults(ctx context.Context) error

	RawDatabase() *mongodriver.Database
}
