package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (c *Client) EnsureIndexes(ctx context.Context) error {
	users := c.db.Collection(userCollection)
	if _, err := users.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "username", Value: 1}}, Options: options.Index().SetUnique(true).SetName("users_username_unique")},
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true).SetName("users_email_unique")},
	}); err != nil {
		return fmt.Errorf("create user indexes: %w", err)
	}
	if _, err := c.db.Collection(recipesCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "author", Value: 1}, {Key: "_id", Value: -1}}, Options: options.Index().SetName("recipes_author_cursor")},
		{Keys: bson.D{{Key: "variation_of", Value: 1}}, Options: options.Index().SetSparse(true).SetName("recipes_variation_of")},
		{Keys: bson.D{{Key: "ingredients.recipe_ref", Value: 1}}, Options: options.Index().SetSparse(true).SetName("recipes_ingredients_recipe_ref")},
	}); err != nil {
		return fmt.Errorf("create recipe indexes: %w", err)
	}
	if _, err := c.db.Collection(translationsCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "recipe_id", Value: 1}, {Key: "locale", Value: 1}}, Options: options.Index().SetUnique(true).SetName("recipe_translations_recipe_locale_unique")},
		{Keys: bson.D{{Key: "locale", Value: 1}, {Key: "title", Value: 1}}, Options: options.Index().SetName("recipe_translations_locale_title")},
	}); err != nil {
		return fmt.Errorf("create translation indexes: %w", err)
	}
	if _, err := c.db.Collection(favoritesCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "recipe", Value: 1}}, Options: options.Index().SetUnique(true).SetName("favorites_recipe_unique")},
		{Keys: bson.D{{Key: "users", Value: 1}}, Options: options.Index().SetName("favorites_users")},
	}); err != nil {
		return fmt.Errorf("create favorite indexes: %w", err)
	}
	if _, err := c.db.Collection(translationSuggestionsCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "created_at", Value: -1}}, Options: options.Index().SetName("recipe_translation_suggestions_status_created")},
		{Keys: bson.D{{Key: "recipe_id", Value: 1}, {Key: "locale", Value: 1}, {Key: "submitted_by", Value: 1}, {Key: "status", Value: 1}}, Options: options.Index().SetName("recipe_translation_suggestions_pending_lookup")},
	}); err != nil {
		return fmt.Errorf("create translation suggestion indexes: %w", err)
	}
	if _, err := c.db.Collection(translationOverridesCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "recipe_id", Value: 1}, {Key: "locale", Value: 1}, {Key: "field_path", Value: 1}}, Options: options.Index().SetUnique(true).SetName("recipe_translation_overrides_recipe_locale_field_unique")},
	}); err != nil {
		return fmt.Errorf("create translation override indexes: %w", err)
	}
	return nil
}
