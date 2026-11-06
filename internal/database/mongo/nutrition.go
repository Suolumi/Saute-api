package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

const (
	nutritionLinksCollection       = "ingredient_nutrition_links"
	unitAliasesCollection          = "unit_aliases"
	nutritionSuggestionsCollection = "nutrition_suggestions"
)

var NutritionEntryNotFoundError = errors.New("nutrition entry not found")
var NutritionSuggestionNotFoundError = errors.New("nutrition suggestion not found")

// --- Ingredient nutrition links ---

func (c *Client) CreateIngredientNutritionLink(ctx context.Context, name, foodID string, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
	entry := models.IngredientNutritionLink{
		Name: name, NameLower: strings.ToLower(name), FoodID: foodID,
		GPer100ml: gPer100ml, GramsPerUnit: gramsPerUnit, CreatedAt: time.Now().UTC(),
	}
	result, err := c.db.Collection(nutritionLinksCollection).InsertOne(ctx, entry)
	if err != nil {
		return models.IngredientNutritionLink{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	entry.Id = &id
	return entry, nil
}

func (c *Client) UpdateIngredientNutritionLink(ctx context.Context, id, name, foodID string, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.IngredientNutritionLink{}, NutritionEntryNotFoundError
	}
	set := bson.D{
		{Key: "name", Value: name}, {Key: "name_lower", Value: strings.ToLower(name)},
		{Key: "food_id", Value: foodID},
	}
	if gPer100ml != nil {
		set = append(set, bson.E{Key: "g_per_100ml", Value: *gPer100ml})
	} else {
		set = append(set, bson.E{Key: "g_per_100ml", Value: nil})
	}
	if gramsPerUnit != nil {
		set = append(set, bson.E{Key: "grams_per_unit", Value: *gramsPerUnit})
	} else {
		set = append(set, bson.E{Key: "grams_per_unit", Value: nil})
	}
	var updated models.IngredientNutritionLink
	err = c.db.Collection(nutritionLinksCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.IngredientNutritionLink{}, NutritionEntryNotFoundError
		}
		return models.IngredientNutritionLink{}, err
	}
	return updated, nil
}

func (c *Client) GetIngredientNutritionLinkById(ctx context.Context, id string) (models.IngredientNutritionLink, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.IngredientNutritionLink{}, NutritionEntryNotFoundError
	}
	var item models.IngredientNutritionLink
	err = c.db.Collection(nutritionLinksCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.IngredientNutritionLink{}, NutritionEntryNotFoundError
		}
		return models.IngredientNutritionLink{}, err
	}
	return item, nil
}

// ListIngredientNutritionLinks returns every link - small and growing only
// from suggestions (same "no pagination, client-filtered" posture as the
// food list), so the authoring UI can tell "already linked" from
// "no link yet" without a lookup per ingredient.
func (c *Client) ListIngredientNutritionLinks(ctx context.Context) ([]models.IngredientNutritionLink, error) {
	cursor, err := c.db.Collection(nutritionLinksCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	items := []models.IngredientNutritionLink{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// DeleteIngredientNutritionLink removes a link outright - admin-only,
// reverts the ingredient back to fully unlinked.
func (c *Client) DeleteIngredientNutritionLink(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return NutritionEntryNotFoundError
	}
	result, err := c.db.Collection(nutritionLinksCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: objectID}})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return NutritionEntryNotFoundError
	}
	return nil
}

// GetIngredientNutritionLinkByNameLower looks up an existing link by
// case-insensitive ingredient name. NutritionEntryNotFoundError means "no
// existing link" - either "no match yet" (suggestion flow) or "this
// ingredient isn't linked" (resolution flow).
func (c *Client) GetIngredientNutritionLinkByNameLower(ctx context.Context, nameLower string) (models.IngredientNutritionLink, error) {
	var item models.IngredientNutritionLink
	err := c.db.Collection(nutritionLinksCollection).FindOne(ctx, bson.D{{Key: "name_lower", Value: nameLower}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.IngredientNutritionLink{}, NutritionEntryNotFoundError
		}
		return models.IngredientNutritionLink{}, err
	}
	return item, nil
}

// --- Unit aliases ---

func (c *Client) CreateUnitAlias(ctx context.Context, alias string, unitID primitive.ObjectID) (models.UnitAlias, error) {
	entry := models.UnitAlias{Alias: alias, AliasLower: strings.ToLower(alias), UnitID: unitID, CreatedAt: time.Now().UTC()}
	result, err := c.db.Collection(unitAliasesCollection).InsertOne(ctx, entry)
	if err != nil {
		return models.UnitAlias{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	entry.Id = &id
	return entry, nil
}

func (c *Client) UpdateUnitAlias(ctx context.Context, id string, alias string, unitID primitive.ObjectID) (models.UnitAlias, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.UnitAlias{}, NutritionEntryNotFoundError
	}
	set := bson.D{{Key: "alias", Value: alias}, {Key: "alias_lower", Value: strings.ToLower(alias)}, {Key: "unit_id", Value: unitID}}
	var updated models.UnitAlias
	err = c.db.Collection(unitAliasesCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.UnitAlias{}, NutritionEntryNotFoundError
		}
		return models.UnitAlias{}, err
	}
	return updated, nil
}

// ListUnitAliases returns every alias - same small/growing/client-filtered
// posture as ListIngredientNutritionLinks.
func (c *Client) ListUnitAliases(ctx context.Context) ([]models.UnitAlias, error) {
	cursor, err := c.db.Collection(unitAliasesCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "alias", Value: 1}}))
	if err != nil {
		return nil, err
	}
	items := []models.UnitAlias{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// DeleteUnitAlias removes an alias outright - admin-only.
func (c *Client) DeleteUnitAlias(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return NutritionEntryNotFoundError
	}
	result, err := c.db.Collection(unitAliasesCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: objectID}})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return NutritionEntryNotFoundError
	}
	return nil
}

// GetUnitAliasByAliasLower looks up an existing alias by case-insensitive
// text. NutritionEntryNotFoundError means "no existing alias".
func (c *Client) GetUnitAliasByAliasLower(ctx context.Context, aliasLower string) (models.UnitAlias, error) {
	var item models.UnitAlias
	err := c.db.Collection(unitAliasesCollection).FindOne(ctx, bson.D{{Key: "alias_lower", Value: aliasLower}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.UnitAlias{}, NutritionEntryNotFoundError
		}
		return models.UnitAlias{}, err
	}
	return item, nil
}

// --- Suggestions ---

func (c *Client) CreateNutritionSuggestion(ctx context.Context, suggestion models.NutritionSuggestion) (models.NutritionSuggestion, error) {
	suggestion.Id = nil
	suggestion.Status = models.NutritionSuggestionPending
	suggestion.CreatedAt = time.Now().UTC()
	suggestion.ReviewedAt = nil
	suggestion.ReviewedBy = nil
	result, err := c.db.Collection(nutritionSuggestionsCollection).InsertOne(ctx, suggestion)
	if err != nil {
		return models.NutritionSuggestion{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	suggestion.Id = &id
	return suggestion, nil
}

func (c *Client) GetNutritionSuggestionById(ctx context.Context, id string) (models.NutritionSuggestion, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.NutritionSuggestion{}, NutritionSuggestionNotFoundError
	}
	var suggestion models.NutritionSuggestion
	err = c.db.Collection(nutritionSuggestionsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&suggestion)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.NutritionSuggestion{}, NutritionSuggestionNotFoundError
		}
		return models.NutritionSuggestion{}, err
	}
	return suggestion, nil
}

// ListNutritionSuggestions returns a page of suggestions, newest first,
// optionally filtered by status ("" means no filter).
func (c *Client) ListNutritionSuggestions(ctx context.Context, status string, limit, offset int64) ([]models.NutritionSuggestion, int64, error) {
	filter := bson.D{}
	if status != "" {
		filter = append(filter, bson.E{Key: "status", Value: status})
	}
	collection := c.db.Collection(nutritionSuggestionsCollection)
	count, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	findOptions := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	if limit > 0 {
		findOptions.SetLimit(limit)
	}
	if offset > 0 {
		findOptions.SetSkip(offset)
	}
	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, 0, err
	}
	suggestions := []models.NutritionSuggestion{}
	if err := cursor.All(ctx, &suggestions); err != nil {
		return nil, 0, err
	}
	return suggestions, count, nil
}

func (c *Client) UpdateNutritionSuggestionStatus(ctx context.Context, id, status, reviewedBy string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return NutritionSuggestionNotFoundError
	}
	set := bson.D{{Key: "status", Value: status}, {Key: "reviewed_at", Value: time.Now().UTC()}}
	if reviewedBy != "" {
		if reviewerID, err := primitive.ObjectIDFromHex(reviewedBy); err == nil {
			set = append(set, bson.E{Key: "reviewed_by", Value: reviewerID})
		}
	}
	result, err := c.db.Collection(nutritionSuggestionsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return NutritionSuggestionNotFoundError
	}
	return nil
}

// --- Seeding ---

// SeedNutritionDefaults adds the default unit aliases that don't exist yet
// (see seedDefaultUnitAliases). Links and suggestions start empty.
func (c *Client) SeedNutritionDefaults(ctx context.Context) error {
	return c.seedDefaultUnitAliases(ctx)
}

// defaultUnitAliases maps unit spellings the Toolbox units' own names and
// symbols don't cover to the name of the unit they mean.
var defaultUnitAliases = []struct{ alias, unitName string }{
	{"克", "Gram"}, {"公克", "Gram"},
	{"千克", "Kilogram"}, {"公斤", "Kilogram"},
	{"盎司", "Ounce"}, {"磅", "Pound"},
	{"毫升", "Milliliter"}, {"厘升", "Centiliter"}, {"升", "Liter"}, {"公升", "Liter"},
	{"杯", "Cup"},
	{"汤匙", "Tablespoon"}, {"大勺", "Tablespoon"}, {"大匙", "Tablespoon"},
	{"茶匙", "Teaspoon"}, {"小勺", "Teaspoon"}, {"小匙", "Teaspoon"},
}

// seedDefaultUnitAliases inserts each default alias that doesn't exist yet,
// skipping any whose unit is gone. An existing alias is left alone, even if
// an admin pointed it at another unit.
func (c *Client) seedDefaultUnitAliases(ctx context.Context) error {
	units, err := c.ListToolboxUnits(ctx)
	if err != nil {
		return err
	}
	unitIDs := make(map[string]primitive.ObjectID, len(units))
	for _, unit := range units {
		if unit.Id != nil {
			unitIDs[unit.NameLower] = *unit.Id
		}
	}
	collection := c.db.Collection(unitAliasesCollection)
	now := time.Now().UTC()
	for _, d := range defaultUnitAliases {
		unitID, ok := unitIDs[strings.ToLower(d.unitName)]
		if !ok {
			continue
		}
		aliasLower := strings.ToLower(d.alias)
		_, err := collection.UpdateOne(ctx,
			bson.D{{Key: "alias_lower", Value: aliasLower}},
			bson.D{{Key: "$setOnInsert", Value: models.UnitAlias{Alias: d.alias, AliasLower: aliasLower, UnitID: unitID, CreatedAt: now}}},
			options.Update().SetUpsert(true))
		if err != nil && !mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("seed unit alias %q: %w", d.alias, err)
		}
	}
	return nil
}
