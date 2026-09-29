package mongo

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

const (
	toolboxIngredientsCollection   = "toolbox_ingredients"
	toolboxUnitsCollection         = "toolbox_units"
	toolboxSubstitutionsCollection = "toolbox_substitutions"
	toolboxSuggestionsCollection   = "toolbox_suggestions"
)

var ToolboxEntryNotFoundError = errors.New("toolbox entry not found")
var ToolboxSuggestionNotFoundError = errors.New("toolbox suggestion not found")

// --- Ingredients ---

func (c *Client) CreateToolboxIngredient(ctx context.Context, name string, gPer100ml float64, note string) (models.ToolboxIngredient, error) {
	entry := models.ToolboxIngredient{
		Name: name, NameLower: strings.ToLower(name), GPer100ml: gPer100ml, Note: note, CreatedAt: time.Now().UTC(),
	}
	result, err := c.db.Collection(toolboxIngredientsCollection).InsertOne(ctx, entry)
	if err != nil {
		return models.ToolboxIngredient{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	entry.Id = &id
	return entry, nil
}

func (c *Client) UpdateToolboxIngredient(ctx context.Context, id string, name string, gPer100ml float64, note string) (models.ToolboxIngredient, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.ToolboxIngredient{}, ToolboxEntryNotFoundError
	}
	set := bson.D{
		{Key: "name", Value: name}, {Key: "name_lower", Value: strings.ToLower(name)},
		{Key: "g_per_100ml", Value: gPer100ml}, {Key: "note", Value: note},
	}
	var updated models.ToolboxIngredient
	err = c.db.Collection(toolboxIngredientsCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxIngredient{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxIngredient{}, err
	}
	return updated, nil
}

func (c *Client) ListToolboxIngredients(ctx context.Context) ([]models.ToolboxIngredient, error) {
	cursor, err := c.db.Collection(toolboxIngredientsCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	items := []models.ToolboxIngredient{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) GetToolboxIngredientById(ctx context.Context, id string) (models.ToolboxIngredient, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.ToolboxIngredient{}, ToolboxEntryNotFoundError
	}
	var item models.ToolboxIngredient
	err = c.db.Collection(toolboxIngredientsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxIngredient{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxIngredient{}, err
	}
	return item, nil
}

// SetToolboxIngredientTranslations stamps an ingredient's detected source
// locale and its full set of machine translations, replacing whatever was
// there before - translate-on-write always regenerates every configured
// target locale in one pass, so there's never a partial map to merge.
func (c *Client) SetToolboxIngredientTranslations(ctx context.Context, id, sourceLocale string, translations map[string]models.ToolboxIngredientTranslation) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return ToolboxEntryNotFoundError
	}
	set := bson.D{{Key: "source_locale", Value: sourceLocale}, {Key: "translations", Value: translations}}
	result, err := c.db.Collection(toolboxIngredientsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ToolboxEntryNotFoundError
	}
	return nil
}

// GetToolboxIngredientByNameLower looks up an existing ingredient by
// case-insensitive name, for suggestion duplicate detection.
// ToolboxEntryNotFoundError means "no existing match" (a new entry).
func (c *Client) GetToolboxIngredientByNameLower(ctx context.Context, nameLower string) (models.ToolboxIngredient, error) {
	var item models.ToolboxIngredient
	err := c.db.Collection(toolboxIngredientsCollection).FindOne(ctx, bson.D{{Key: "name_lower", Value: nameLower}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxIngredient{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxIngredient{}, err
	}
	return item, nil
}

// --- Units ---

func (c *Client) CreateToolboxUnit(ctx context.Context, name, symbol, kind string, toBase float64) (models.ToolboxUnit, error) {
	entry := models.ToolboxUnit{
		Name: name, NameLower: strings.ToLower(name), Symbol: symbol, Kind: kind, ToBase: toBase, CreatedAt: time.Now().UTC(),
	}
	result, err := c.db.Collection(toolboxUnitsCollection).InsertOne(ctx, entry)
	if err != nil {
		return models.ToolboxUnit{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	entry.Id = &id
	return entry, nil
}

func (c *Client) UpdateToolboxUnit(ctx context.Context, id string, name, symbol, kind string, toBase float64) (models.ToolboxUnit, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.ToolboxUnit{}, ToolboxEntryNotFoundError
	}
	set := bson.D{
		{Key: "name", Value: name}, {Key: "name_lower", Value: strings.ToLower(name)},
		{Key: "symbol", Value: symbol}, {Key: "kind", Value: kind}, {Key: "to_base", Value: toBase},
	}
	var updated models.ToolboxUnit
	err = c.db.Collection(toolboxUnitsCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxUnit{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxUnit{}, err
	}
	return updated, nil
}

func (c *Client) ListToolboxUnits(ctx context.Context) ([]models.ToolboxUnit, error) {
	cursor, err := c.db.Collection(toolboxUnitsCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "kind", Value: 1}, {Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	items := []models.ToolboxUnit{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) GetToolboxUnitById(ctx context.Context, id string) (models.ToolboxUnit, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.ToolboxUnit{}, ToolboxEntryNotFoundError
	}
	var item models.ToolboxUnit
	err = c.db.Collection(toolboxUnitsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxUnit{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxUnit{}, err
	}
	return item, nil
}

// SetToolboxUnitTranslations is SetToolboxIngredientTranslations for units.
func (c *Client) SetToolboxUnitTranslations(ctx context.Context, id, sourceLocale string, translations map[string]models.ToolboxUnitTranslation) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return ToolboxEntryNotFoundError
	}
	set := bson.D{{Key: "source_locale", Value: sourceLocale}, {Key: "translations", Value: translations}}
	result, err := c.db.Collection(toolboxUnitsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ToolboxEntryNotFoundError
	}
	return nil
}

// GetToolboxUnitByNameLower looks up an existing unit by case-insensitive
// name, for suggestion duplicate detection.
func (c *Client) GetToolboxUnitByNameLower(ctx context.Context, nameLower string) (models.ToolboxUnit, error) {
	var item models.ToolboxUnit
	err := c.db.Collection(toolboxUnitsCollection).FindOne(ctx, bson.D{{Key: "name_lower", Value: nameLower}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxUnit{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxUnit{}, err
	}
	return item, nil
}

// --- Substitutions ---

func (c *Client) CreateToolboxSubstitution(ctx context.Context, problem, solution, tag string) (models.ToolboxSubstitution, error) {
	entry := models.ToolboxSubstitution{Problem: problem, Solution: solution, Tag: tag, CreatedAt: time.Now().UTC()}
	result, err := c.db.Collection(toolboxSubstitutionsCollection).InsertOne(ctx, entry)
	if err != nil {
		return models.ToolboxSubstitution{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	entry.Id = &id
	return entry, nil
}

func (c *Client) UpdateToolboxSubstitution(ctx context.Context, id string, problem, solution, tag string) (models.ToolboxSubstitution, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.ToolboxSubstitution{}, ToolboxEntryNotFoundError
	}
	set := bson.D{{Key: "problem", Value: problem}, {Key: "solution", Value: solution}, {Key: "tag", Value: tag}}
	var updated models.ToolboxSubstitution
	err = c.db.Collection(toolboxSubstitutionsCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxSubstitution{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxSubstitution{}, err
	}
	return updated, nil
}

func (c *Client) ListToolboxSubstitutions(ctx context.Context) ([]models.ToolboxSubstitution, error) {
	cursor, err := c.db.Collection(toolboxSubstitutionsCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "problem", Value: 1}}))
	if err != nil {
		return nil, err
	}
	items := []models.ToolboxSubstitution{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) GetToolboxSubstitutionById(ctx context.Context, id string) (models.ToolboxSubstitution, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.ToolboxSubstitution{}, ToolboxEntryNotFoundError
	}
	var item models.ToolboxSubstitution
	err = c.db.Collection(toolboxSubstitutionsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxSubstitution{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxSubstitution{}, err
	}
	return item, nil
}

// SetToolboxSubstitutionTranslations is SetToolboxIngredientTranslations
// for substitutions.
func (c *Client) SetToolboxSubstitutionTranslations(ctx context.Context, id, sourceLocale string, translations map[string]models.ToolboxSubstitutionTranslation) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return ToolboxEntryNotFoundError
	}
	set := bson.D{{Key: "source_locale", Value: sourceLocale}, {Key: "translations", Value: translations}}
	result, err := c.db.Collection(toolboxSubstitutionsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ToolboxEntryNotFoundError
	}
	return nil
}

// GetToolboxSubstitutionByProblemLower looks up an existing substitution by
// case-insensitive problem text, for suggestion duplicate detection.
func (c *Client) GetToolboxSubstitutionByProblemLower(ctx context.Context, problemLower string) (models.ToolboxSubstitution, error) {
	var item models.ToolboxSubstitution
	err := c.db.Collection(toolboxSubstitutionsCollection).FindOne(ctx, bson.D{{Key: "problem", Value: bson.D{{Key: "$regex", Value: "^" + regexp.QuoteMeta(problemLower) + "$"}, {Key: "$options", Value: "i"}}}}).Decode(&item)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxSubstitution{}, ToolboxEntryNotFoundError
		}
		return models.ToolboxSubstitution{}, err
	}
	return item, nil
}

// --- Suggestions ---

// CreateToolboxSuggestion inserts a new pending suggestion, stamping
// Status/CreatedAt server-side regardless of what the caller set.
func (c *Client) CreateToolboxSuggestion(ctx context.Context, suggestion models.ToolboxSuggestion) (models.ToolboxSuggestion, error) {
	suggestion.Id = nil
	suggestion.Status = models.ToolboxSuggestionPending
	suggestion.CreatedAt = time.Now().UTC()
	suggestion.ReviewedAt = nil
	suggestion.ReviewedBy = nil
	result, err := c.db.Collection(toolboxSuggestionsCollection).InsertOne(ctx, suggestion)
	if err != nil {
		return models.ToolboxSuggestion{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	suggestion.Id = &id
	return suggestion, nil
}

func (c *Client) GetToolboxSuggestionById(ctx context.Context, id string) (models.ToolboxSuggestion, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.ToolboxSuggestion{}, ToolboxSuggestionNotFoundError
	}
	var suggestion models.ToolboxSuggestion
	err = c.db.Collection(toolboxSuggestionsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&suggestion)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.ToolboxSuggestion{}, ToolboxSuggestionNotFoundError
		}
		return models.ToolboxSuggestion{}, err
	}
	return suggestion, nil
}

// ListToolboxSuggestions returns a page of suggestions, newest first,
// optionally filtered by status and/or kind (either "" means no filter).
func (c *Client) ListToolboxSuggestions(ctx context.Context, status, kind string, limit, offset int64) ([]models.ToolboxSuggestion, int64, error) {
	filter := bson.D{}
	if status != "" {
		filter = append(filter, bson.E{Key: "status", Value: status})
	}
	if kind != "" {
		filter = append(filter, bson.E{Key: "kind", Value: kind})
	}
	collection := c.db.Collection(toolboxSuggestionsCollection)
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
	suggestions := []models.ToolboxSuggestion{}
	if err := cursor.All(ctx, &suggestions); err != nil {
		return nil, 0, err
	}
	return suggestions, count, nil
}

// UpdateToolboxSuggestionStatus transitions a suggestion to a terminal
// status, stamping who reviewed it and when.
func (c *Client) UpdateToolboxSuggestionStatus(ctx context.Context, id, status, reviewedBy string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return ToolboxSuggestionNotFoundError
	}
	set := bson.D{{Key: "status", Value: status}, {Key: "reviewed_at", Value: time.Now().UTC()}}
	if reviewedBy != "" {
		if reviewerID, err := primitive.ObjectIDFromHex(reviewedBy); err == nil {
			set = append(set, bson.E{Key: "reviewed_by", Value: reviewerID})
		}
	}
	result, err := c.db.Collection(toolboxSuggestionsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}}, bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ToolboxSuggestionNotFoundError
	}
	return nil
}

// --- Seeding ---

// SeedToolboxDefaults inserts a starter set of common ingredients and units
// the first time each collection is empty - idempotent, safe to call on
// every startup. It never touches a collection that already has documents,
// so admin edits/deletions are never undone. Substitutions get no seed data
// - that list starts empty and grows only from user suggestions.
func (c *Client) SeedToolboxDefaults(ctx context.Context) error {
	if count, err := c.db.Collection(toolboxIngredientsCollection).CountDocuments(ctx, bson.D{}); err != nil {
		return err
	} else if count == 0 {
		docs := []interface{}{}
		for _, i := range defaultToolboxIngredients {
			docs = append(docs, models.ToolboxIngredient{Name: i.name, NameLower: strings.ToLower(i.name), GPer100ml: i.gPer100ml, CreatedAt: time.Now().UTC()})
		}
		if _, err := c.db.Collection(toolboxIngredientsCollection).InsertMany(ctx, docs); err != nil {
			return err
		}
	}
	if count, err := c.db.Collection(toolboxUnitsCollection).CountDocuments(ctx, bson.D{}); err != nil {
		return err
	} else if count == 0 {
		docs := []interface{}{}
		for _, u := range defaultToolboxUnits {
			docs = append(docs, models.ToolboxUnit{Name: u.name, NameLower: strings.ToLower(u.name), Symbol: u.symbol, Kind: u.kind, ToBase: u.toBase, CreatedAt: time.Now().UTC()})
		}
		if _, err := c.db.Collection(toolboxUnitsCollection).InsertMany(ctx, docs); err != nil {
			return err
		}
	}
	return nil
}

var defaultToolboxIngredients = []struct {
	name      string
	gPer100ml float64
}{
	{"Milk", 103}, {"Water", 100}, {"Flour (all-purpose)", 53}, {"Sugar (granulated)", 85},
	{"Brown sugar", 90}, {"Butter", 96}, {"Honey", 141}, {"Olive oil", 92}, {"Vegetable oil", 92},
	{"Rice (uncooked)", 85}, {"Cocoa powder", 53}, {"Heavy cream", 100}, {"Yogurt", 103},
}

var defaultToolboxUnits = []struct {
	name, symbol, kind string
	toBase             float64
}{
	{"Gram", "g", models.ToolboxUnitWeight, 1},
	{"Kilogram", "kg", models.ToolboxUnitWeight, 1000},
	{"Ounce", "oz", models.ToolboxUnitWeight, 28.35},
	{"Pound", "lb", models.ToolboxUnitWeight, 453.6},
	{"Milliliter", "ml", models.ToolboxUnitVolume, 1},
	{"Centiliter", "cl", models.ToolboxUnitVolume, 10},
	{"Liter", "L", models.ToolboxUnitVolume, 1000},
	{"Cup", "cup", models.ToolboxUnitVolume, 240},
	{"Tablespoon", "tbsp", models.ToolboxUnitVolume, 15},
	{"Teaspoon", "tsp", models.ToolboxUnitVolume, 5},
}
