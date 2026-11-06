package mongo

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

const (
	toolboxUnitsCollection = "toolbox_units"
)

var ToolboxEntryNotFoundError = errors.New("toolbox entry not found")
var ToolboxUnitExistsError = errors.New("a unit with this name already exists")

// --- Units ---

func (c *Client) CreateToolboxUnit(ctx context.Context, name, symbol, kind string, toBase float64) (models.ToolboxUnit, error) {
	entry := models.ToolboxUnit{
		Name: name, NameLower: strings.ToLower(name), Symbol: symbol, Kind: kind, ToBase: toBase, CreatedAt: time.Now().UTC(),
	}
	result, err := c.db.Collection(toolboxUnitsCollection).InsertOne(ctx, entry)
	if mongo.IsDuplicateKeyError(err) {
		return models.ToolboxUnit{}, ToolboxUnitExistsError
	}
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
		if mongo.IsDuplicateKeyError(err) {
			return models.ToolboxUnit{}, ToolboxUnitExistsError
		}
		return models.ToolboxUnit{}, err
	}
	return updated, nil
}

func (c *Client) DeleteToolboxUnit(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return ToolboxEntryNotFoundError
	}
	result, err := c.db.Collection(toolboxUnitsCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: objectID}})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return ToolboxEntryNotFoundError
	}
	return nil
}

// CountUnitAliasesForUnit counts the unit aliases pointing at unit id.
func (c *Client) CountUnitAliasesForUnit(ctx context.Context, id primitive.ObjectID) (int64, error) {
	return c.db.Collection(unitAliasesCollection).CountDocuments(ctx, bson.D{{Key: "unit_id", Value: id}})
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

// SetToolboxUnitTranslations stamps a unit's detected source locale and
// replaces its translations.
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

// --- Seeding ---

// SeedToolboxDefaults inserts a starter set of units the first time the
// collection is empty - idempotent, safe to call on every startup. It never
// touches a collection that already has documents, so admin edits/deletions
// are never undone.
func (c *Client) SeedToolboxDefaults(ctx context.Context) error {
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
