package mongo

import (
	"context"
	_ "embed"
	"encoding/json"
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
	nutritionIngredientsCollection = "nutrition_ingredients"
	nutritionLinksCollection       = "ingredient_nutrition_links"
	unitAliasesCollection          = "unit_aliases"
	nutritionSuggestionsCollection = "nutrition_suggestions"
)

var NutritionEntryNotFoundError = errors.New("nutrition entry not found")
var NutritionSuggestionNotFoundError = errors.New("nutrition suggestion not found")

// --- Nutrition ingredients (Ciqual seed, read-only after seeding) ---

func (c *Client) ListNutritionIngredients(ctx context.Context) ([]models.NutritionIngredient, error) {
	cursor, err := c.db.Collection(nutritionIngredientsCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	items := []models.NutritionIngredient{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) GetNutritionIngredientById(ctx context.Context, id string) (models.NutritionIngredient, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.NutritionIngredient{}, NutritionEntryNotFoundError
	}
	var item models.NutritionIngredient
	err = c.db.Collection(nutritionIngredientsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.NutritionIngredient{}, NutritionEntryNotFoundError
		}
		return models.NutritionIngredient{}, err
	}
	return item, nil
}

// --- Ingredient nutrition links ---

func (c *Client) CreateIngredientNutritionLink(ctx context.Context, name string, nutritionID primitive.ObjectID, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
	entry := models.IngredientNutritionLink{
		Name: name, NameLower: strings.ToLower(name), NutritionID: nutritionID,
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

func (c *Client) UpdateIngredientNutritionLink(ctx context.Context, id string, name string, nutritionID primitive.ObjectID, gPer100ml, gramsPerUnit *float64) (models.IngredientNutritionLink, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.IngredientNutritionLink{}, NutritionEntryNotFoundError
	}
	set := bson.D{
		{Key: "name", Value: name}, {Key: "name_lower", Value: strings.ToLower(name)},
		{Key: "nutrition_id", Value: nutritionID},
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
// curated Ciqual list), so the authoring UI can tell "already linked" from
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

//go:embed seeddata/nutrition_ciqual.json
var nutritionCiqualSeed []byte

type ciqualSeedRow struct {
	Name        string            `json:"name"`
	Names       map[string]string `json:"names"`
	KcalPer100g float64           `json:"kcal_per_100g"`
	ProteinG    float64           `json:"protein_g_per_100g"`
	CarbsG      float64           `json:"carbs_g_per_100g"`
	FatG        float64           `json:"fat_g_per_100g"`
	SaltG       float64           `json:"salt_g_per_100g"`
	SugarG      float64           `json:"sugar_g_per_100g"`
}

// SeedNutritionDefaults inserts the imported Ciqual reference list the first
// time the collection is empty - idempotent, safe to call on every startup,
// same posture as SeedToolboxDefaults. It never touches a non-empty
// collection, so admin edits/deletions are never undone.
// IngredientNutritionLink/UnitAlias/NutritionSuggestion all start empty and
// grow only from user suggestions - no seed data for them.
func (c *Client) SeedNutritionDefaults(ctx context.Context) error {
	count, err := c.db.Collection(nutritionIngredientsCollection).CountDocuments(ctx, bson.D{})
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var rows []ciqualSeedRow
	if err := json.Unmarshal(nutritionCiqualSeed, &rows); err != nil {
		return fmt.Errorf("parse embedded nutrition seed data: %w", err)
	}
	now := time.Now().UTC()
	docs := make([]interface{}, 0, len(rows))
	for _, row := range rows {
		docs = append(docs, models.NutritionIngredient{
			Name: row.Name, NameLower: strings.ToLower(row.Name), Names: row.Names,
			KcalPer100g: row.KcalPer100g, ProteinG: row.ProteinG, CarbsG: row.CarbsG,
			FatG: row.FatG, SaltG: row.SaltG, SugarG: row.SugarG, CreatedAt: now,
		})
	}
	// InsertMany in batches: a single 3000+ document call is fine for Mongo's
	// own limits (well under the 16MB/100k-document ceiling either way), but
	// chunking keeps any single write comfortably small.
	const batchSize = 500
	collection := c.db.Collection(nutritionIngredientsCollection)
	for i := 0; i < len(docs); i += batchSize {
		end := min(i+batchSize, len(docs))
		if _, err := collection.InsertMany(ctx, docs[i:end]); err != nil {
			return fmt.Errorf("insert nutrition seed batch: %w", err)
		}
	}
	return nil
}
