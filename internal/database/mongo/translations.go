package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

const translationsCollection = "recipe_translations"

type recipeTranslationDocument struct {
	ID              primitive.ObjectID    `bson:"_id,omitempty"`
	RecipeID        primitive.ObjectID    `bson:"recipe_id"`
	Author          *primitive.ObjectID   `bson:"author,omitempty"`
	Title           string                `bson:"title,omitempty"`
	Description     string                `bson:"description,omitempty"`
	Quantity        int                   `bson:"quantity,omitempty"`
	Kind            models.RecipeKind     `bson:"kind,omitempty"`
	Category        models.RecipeCategory `bson:"category,omitempty"`
	PreparationTime int                   `bson:"preparation_time,omitempty"`
	CookingTime     int                   `bson:"cooking_time,omitempty"`
	RestingTime     int                   `bson:"resting_time,omitempty"`
	Ingredients     []models.Ingredient   `bson:"ingredients,omitempty"`
	Steps           []models.Step         `bson:"steps,omitempty"`
	// Pictures is deliberately absent: like VariationOf, pictures are
	// structural, not translatable content - pickTranslation always takes
	// them from the canonical recipe, never from a translation row.
	SourceLocale string `bson:"source_locale,omitempty"`
	Locale       string `bson:"locale,omitempty"`
	SourceHash   string `bson:"source_hash,omitempty"`
}

func translationFromRecipe(recipe models.Recipe, locale string) (recipeTranslationDocument, error) {
	if recipe.Id == nil {
		return recipeTranslationDocument{}, errors.New("recipe id is required")
	}
	var author *primitive.ObjectID
	if recipe.Author != nil {
		author = recipe.Author.Id
	}
	// ID is intentionally left zero: bson:"_id,omitempty" drops it from the
	// document so ReplaceOne keeps the matched translation's _id on replace and
	// lets MongoDB generate one on insert. Carrying a fresh _id here makes the
	// upsert fail on replace because _id is immutable.
	return recipeTranslationDocument{
		RecipeID:        *recipe.Id,
		Author:          author,
		Title:           recipe.Title,
		Description:     recipe.Description,
		Quantity:        recipe.Quantity,
		Kind:            recipe.Kind,
		Category:        recipe.Category,
		PreparationTime: recipe.PreparationTime,
		CookingTime:     recipe.CookingTime,
		RestingTime:     recipe.RestingTime,
		Ingredients:     recipe.Ingredients,
		Steps:           recipe.Steps,
		SourceLocale:    recipe.SourceLocale,
		Locale:          locale,
		SourceHash:      recipe.SourceHash,
	}, nil
}

func translationPipeline(match bson.D) mongo.Pipeline {
	return mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$set", Value: bson.D{{Key: "_id", Value: "$recipe_id"}}}},
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: userCollection},
			{Key: "localField", Value: "author"},
			{Key: "foreignField", Value: "_id"},
			{Key: "as", Value: "author"},
		}}},
		{{Key: "$unwind", Value: "$author"}},
	}
}

func (c *Client) AddLocaleRecipe(recipe models.Recipe, locale string) (models.Recipe, error) {
	document, err := translationFromRecipe(recipe, locale)
	if err != nil {
		return models.Recipe{}, err
	}
	collection := c.db.Collection(translationsCollection)
	_, err = collection.ReplaceOne(context.Background(), bson.D{
		{Key: "recipe_id", Value: document.RecipeID},
		{Key: "locale", Value: locale},
	}, document, options.Replace().SetUpsert(true))
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return c.getTranslatedRecipe(context.Background(), document.RecipeID, locale)
		}
		return models.Recipe{}, err
	}
	return c.getTranslatedRecipe(context.Background(), document.RecipeID, locale)
}

func (c *Client) getTranslatedRecipe(ctx context.Context, recipeID primitive.ObjectID, locale string) (models.Recipe, error) {
	cursor, err := c.db.Collection(translationsCollection).Aggregate(ctx, translationPipeline(bson.D{
		{Key: "recipe_id", Value: recipeID},
		{Key: "locale", Value: locale},
	}))
	if err != nil {
		return models.Recipe{}, err
	}
	recipes, err := c.transformRecipe(cursor)
	if err != nil {
		return models.Recipe{}, err
	}
	if len(recipes) == 0 {
		return models.Recipe{}, NotFoundError
	}
	return recipes[0], nil
}

func (c *Client) GetRecipeByIdLocale(id string, locale string) (models.Recipe, error) {
	recipeID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Recipe{}, err
	}
	return c.getTranslatedRecipe(context.Background(), recipeID, locale)
}

// GetTranslationsByRecipeIDs fetches the stored translation for each given
// recipe in one query, keyed by recipe id hex. Recipes without a translation
// for the locale are simply absent from the map.
func (c *Client) GetTranslationsByRecipeIDs(ctx context.Context, ids []string, locale string) (map[string]models.Recipe, error) {
	objectIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		objectID, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			return nil, err
		}
		objectIDs = append(objectIDs, objectID)
	}
	result := make(map[string]models.Recipe, len(objectIDs))
	if len(objectIDs) == 0 {
		return result, nil
	}
	cursor, err := c.db.Collection(translationsCollection).Aggregate(ctx, translationPipeline(bson.D{
		{Key: "recipe_id", Value: bson.D{{Key: "$in", Value: objectIDs}}},
		{Key: "locale", Value: locale},
	}))
	if err != nil {
		return nil, err
	}
	recipes, err := c.transformRecipe(cursor)
	if err != nil {
		return nil, err
	}
	for _, recipe := range recipes {
		if recipe.Id != nil {
			result[recipe.Id.Hex()] = recipe
		}
	}
	return result, nil
}
