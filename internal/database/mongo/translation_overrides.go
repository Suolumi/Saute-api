package mongo

import (
	"context"
	"errors"
	"regexp"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

const translationOverridesCollection = "recipe_translation_overrides"

var TranslationOverrideNotFoundError = errors.New("translation override not found")

// UpsertTranslationOverride replaces any existing override for the same
// (recipe, locale, field), or inserts a new one - the same replace-upsert
// idiom AddLocaleRecipe uses for recipe_translations.
func (c *Client) UpsertTranslationOverride(ctx context.Context, override models.TranslationOverride) error {
	override.Id = nil
	_, err := c.db.Collection(translationOverridesCollection).ReplaceOne(ctx,
		bson.D{
			{Key: "recipe_id", Value: override.RecipeID},
			{Key: "locale", Value: override.Locale},
			{Key: "field_path", Value: override.FieldPath},
		},
		override,
		options.Replace().SetUpsert(true),
	)
	return err
}

// ListTranslationOverrides returns every override for a recipe, optionally
// narrowed to one locale ("" means every locale).
func (c *Client) ListTranslationOverrides(ctx context.Context, recipeID, locale string) ([]models.TranslationOverride, error) {
	recipeObjectID, err := primitive.ObjectIDFromHex(recipeID)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "recipe_id", Value: recipeObjectID}}
	if locale != "" {
		filter = append(filter, bson.E{Key: "locale", Value: locale})
	}
	cursor, err := c.db.Collection(translationOverridesCollection).Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "field_path", Value: 1}}))
	if err != nil {
		return nil, err
	}
	overrides := []models.TranslationOverride{}
	if err := cursor.All(ctx, &overrides); err != nil {
		return nil, err
	}
	return overrides, nil
}

// DeleteTranslationOverride removes one override by id, returning the
// document that was deleted so the caller can act on its recipe/locale (see
// recipe_service.ClearTranslationOverride, which re-translates that locale
// right after so the effect is visible immediately).
func (c *Client) DeleteTranslationOverride(ctx context.Context, id string) (models.TranslationOverride, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.TranslationOverride{}, TranslationOverrideNotFoundError
	}
	var deleted models.TranslationOverride
	err = c.db.Collection(translationOverridesCollection).FindOneAndDelete(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&deleted)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.TranslationOverride{}, TranslationOverrideNotFoundError
		}
		return models.TranslationOverride{}, err
	}
	return deleted, nil
}

// DeleteTranslationOverridesByFieldPrefix removes every override for a
// (recipe, locale) whose field path starts with prefix ("ingredients." or
// "steps.") - used to drop a whole list's overrides in one shot when the
// list's length no longer matches what the overrides were approved against.
func (c *Client) DeleteTranslationOverridesByFieldPrefix(ctx context.Context, recipeID, locale, prefix string) error {
	recipeObjectID, err := primitive.ObjectIDFromHex(recipeID)
	if err != nil {
		return err
	}
	_, err = c.db.Collection(translationOverridesCollection).DeleteMany(ctx, bson.D{
		{Key: "recipe_id", Value: recipeObjectID},
		{Key: "locale", Value: locale},
		{Key: "field_path", Value: bson.D{{Key: "$regex", Value: "^" + regexp.QuoteMeta(prefix)}}},
	})
	return err
}
