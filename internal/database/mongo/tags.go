package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

const recipeTagsCollection = "recipe_tags"

var TagNotFoundError = errors.New("tag not found")
var TagExistsError = errors.New("tag already exists")

func (c *Client) ListRecipeTags(ctx context.Context) ([]models.RecipeTag, error) {
	cursor, err := c.db.Collection(recipeTagsCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	items := []models.RecipeTag{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) GetRecipeTag(ctx context.Context, slug string) (models.RecipeTag, error) {
	var tag models.RecipeTag
	err := c.db.Collection(recipeTagsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: slug}}).Decode(&tag)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.RecipeTag{}, TagNotFoundError
	}
	return tag, err
}

func (c *Client) CreateRecipeTag(ctx context.Context, tag models.RecipeTag) error {
	_, err := c.db.Collection(recipeTagsCollection).InsertOne(ctx, tag)
	if mongo.IsDuplicateKeyError(err) {
		return TagExistsError
	}
	return err
}

// UpdateRecipeTag overwrites every field of an existing tag except its slug
// and creation time.
func (c *Client) UpdateRecipeTag(ctx context.Context, tag models.RecipeTag) (models.RecipeTag, error) {
	set := bson.D{
		{Key: "group", Value: tag.Group}, {Key: "categories", Value: tag.Categories},
		{Key: "implies", Value: tag.Implies}, {Key: "labels", Value: tag.Labels},
		{Key: "source_locale", Value: tag.SourceLocale}, {Key: "icon", Value: tag.Icon},
	}
	update := bson.D{{Key: "$set", Value: set}}
	var updated models.RecipeTag
	err := c.db.Collection(recipeTagsCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: tag.Slug}}, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.RecipeTag{}, TagNotFoundError
	}
	return updated, err
}

func (c *Client) DeleteRecipeTag(ctx context.Context, slug string) error {
	result, err := c.db.Collection(recipeTagsCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: slug}})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return TagNotFoundError
	}
	return nil
}

// CountRecipesWithTag counts recipes carrying slug, narrowed to categories
// when non-empty (a recipe with no category counts as food).
func (c *Client) CountRecipesWithTag(ctx context.Context, slug string, categories []models.RecipeCategory) (int64, error) {
	filter := bson.D{{Key: "tags", Value: slug}}
	if len(categories) > 0 {
		in := bson.A{}
		for _, category := range categories {
			in = append(in, category)
			if category == models.Food {
				in = append(in, nil, "")
			}
		}
		filter = append(filter, bson.E{Key: "category", Value: bson.D{{Key: "$in", Value: in}}})
	}
	return c.db.Collection(recipesCollection).CountDocuments(ctx, filter)
}

// AddTagsToRecipesWithTag adds every tag in add to each recipe carrying slug,
// returning how many recipes changed.
func (c *Client) AddTagsToRecipesWithTag(ctx context.Context, slug string, add []string) (int64, error) {
	result, err := c.db.Collection(recipesCollection).UpdateMany(ctx,
		bson.D{{Key: "tags", Value: slug}},
		bson.D{{Key: "$addToSet", Value: bson.D{{Key: "tags", Value: bson.D{{Key: "$each", Value: add}}}}}})
	if err != nil {
		return 0, err
	}
	return result.ModifiedCount, nil
}

// FillRecipeTagLabel sets one locale's label from a machine translation, only
// while that locale is still blank and the source label is still the one that
// was translated, so it never overwrites an admin edit made in the meantime.
func (c *Client) FillRecipeTagLabel(ctx context.Context, slug, sourceLocale, sourceLabel, locale, label string) error {
	_, err := c.db.Collection(recipeTagsCollection).UpdateOne(ctx,
		bson.D{
			{Key: "_id", Value: slug},
			{Key: "source_locale", Value: sourceLocale},
			{Key: "labels." + sourceLocale, Value: sourceLabel},
			{Key: "labels." + locale, Value: bson.D{{Key: "$in", Value: bson.A{nil, ""}}}},
		},
		bson.D{{Key: "$set", Value: bson.D{{Key: "labels." + locale, Value: label}}}})
	return err
}

// SeedTagIcons gives each of the original tags its default icon while its
// icon is blank. It never overwrites an icon, so admin picks stay.
func (c *Client) SeedTagIcons(ctx context.Context) error {
	for slug, icon := range defaultTagIcons {
		filter := bson.D{{Key: "_id", Value: slug}, {Key: "$or", Value: bson.A{
			bson.D{{Key: "icon", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "icon", Value: ""}},
		}}}
		if _, err := c.db.Collection(recipeTagsCollection).UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "icon", Value: icon}}}}); err != nil {
			return err
		}
	}
	return nil
}

// defaultTagIcons maps the tags created before icons existed to a name from
// the site's tagIcons list.
var defaultTagIcons = map[string]string{
	"gluten-free-option": "wheat-off",
	"high-protein":       "dumbbell",
	"pescatarian":        "fish",
	"vegan":              "vegan",
	"vegetarian":         "carrot",
	"batch-cooking":      "meal-prep",
	"budget-friendly":    "piggy-bank",
	"freezer-friendly":   "snowflake",
	"leftovers":          "leftovers",
	"make-ahead":         "calendar-clock",
	"microwave":          "microwave",
	"no-cook":            "flame-off",
	"no-oven":            "oven-off",
	"one-pot":            "cooking-pot",
	"slow-cooking":       "slow-cooker",
}
