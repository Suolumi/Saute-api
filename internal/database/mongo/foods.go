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
	"recipes/internal/utils"
)

const (
	foodsCollection = "foods"

	// Collections replaced by foods; MigrateFoods drops them.
	legacyCiqualCollection             = "nutrition_ingredients"
	legacyToolboxIngredientsCollection = "toolbox_ingredients"
	legacyToolboxSuggestionsCollection = "toolbox_suggestions"
	legacySubstitutionsCollection      = "toolbox_substitutions"
)

var FoodNotFoundError = errors.New("food not found")
var FoodExistsError = errors.New("food already exists")

//go:embed seeddata/foods.json
var foodsSeed []byte

// foodSeedRow is one food of seeddata/foods.json. Ciqual lists the names of
// the old Ciqual entries folded into it, used only to remap old links.
type foodSeedRow struct {
	Id            string              `json:"id"`
	Names         map[string]string   `json:"names"`
	Aliases       map[string][]string `json:"aliases"`
	GramsPerPiece *float64            `json:"grams_per_piece"`
	GPer100ml     *float64            `json:"g_per_100ml"`
	Nutrition     struct {
		Kcal    float64 `json:"kcal_per_100g"`
		Protein float64 `json:"protein_g_per_100g"`
		Carbs   float64 `json:"carbs_g_per_100g"`
		Fat     float64 `json:"fat_g_per_100g"`
		Salt    float64 `json:"salt_g_per_100g"`
		Sugar   float64 `json:"sugar_g_per_100g"`
	} `json:"nutrition"`
	Ciqual []string `json:"ciqual"`
}

func (c *Client) ListFoods(ctx context.Context) ([]models.FoodItem, error) {
	cursor, err := c.db.Collection(foodsCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	items := []models.FoodItem{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) GetFoodById(ctx context.Context, id string) (models.FoodItem, error) {
	var item models.FoodItem
	err := c.db.Collection(foodsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.FoodItem{}, FoodNotFoundError
	}
	return item, err
}

func (c *Client) CreateFood(ctx context.Context, food models.FoodItem) error {
	_, err := c.db.Collection(foodsCollection).InsertOne(ctx, food)
	if mongo.IsDuplicateKeyError(err) {
		return FoodExistsError
	}
	return err
}

// UpdateFood overwrites every field of an existing food except its id and
// creation time.
func (c *Client) UpdateFood(ctx context.Context, food models.FoodItem) (models.FoodItem, error) {
	set := bson.D{
		{Key: "names", Value: food.Names}, {Key: "aliases", Value: food.Aliases},
		{Key: "grams_per_piece", Value: food.GramsPerPiece}, {Key: "g_per_100ml", Value: food.GPer100ml},
		{Key: "substitutes", Value: food.Substitutes},
		{Key: "kcal_per_100g", Value: food.KcalPer100g}, {Key: "protein_g_per_100g", Value: food.ProteinG},
		{Key: "carbs_g_per_100g", Value: food.CarbsG}, {Key: "fat_g_per_100g", Value: food.FatG},
		{Key: "salt_g_per_100g", Value: food.SaltG}, {Key: "sugar_g_per_100g", Value: food.SugarG},
	}
	var updated models.FoodItem
	err := c.db.Collection(foodsCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: food.Id}}, bson.D{{Key: "$set", Value: set}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.FoodItem{}, FoodNotFoundError
	}
	return updated, err
}

func (c *Client) DeleteFood(ctx context.Context, id string) error {
	result, err := c.db.Collection(foodsCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return FoodNotFoundError
	}
	return nil
}

// CountLinksForFood counts the ingredient links pointing at food id.
func (c *Client) CountLinksForFood(ctx context.Context, id string) (int64, error) {
	return c.db.Collection(nutritionLinksCollection).CountDocuments(ctx, bson.D{{Key: "food_id", Value: id}})
}

// MigrateFoods seeds the food list when the collection is empty, then moves
// data off the collections it replaces: Toolbox substitutions move onto the
// food their problem names (see migrateSubstitutions); links and nutrition
// suggestions
// pointing at an old Ciqual entry are repointed at the food that entry was
// folded into, or deleted (and logged) when it was dropped. The old
// collections are dropped only once every link is handled, so a run cut
// short is finished by the next startup.
func (c *Client) MigrateFoods(ctx context.Context) error {
	var rows []foodSeedRow
	if err := json.Unmarshal(foodsSeed, &rows); err != nil {
		return fmt.Errorf("parse embedded food seed data: %w", err)
	}
	count, err := c.db.Collection(foodsCollection).CountDocuments(ctx, bson.D{})
	if err != nil {
		return err
	}
	if count == 0 {
		if err := c.insertFoods(ctx, rows); err != nil {
			return err
		}
	}

	names, err := c.db.ListCollectionNames(ctx, bson.D{{Key: "name", Value: bson.D{{Key: "$in", Value: bson.A{
		legacyCiqualCollection, legacyToolboxIngredientsCollection, legacyToolboxSuggestionsCollection, legacySubstitutionsCollection,
	}}}}})
	if err != nil {
		return err
	}
	for _, name := range names {
		var err error
		switch name {
		case legacyCiqualCollection:
			err = c.remapCiqualReferences(ctx, rows)
		case legacySubstitutionsCollection:
			err = c.migrateSubstitutions(ctx)
		}
		if err != nil {
			return err
		}
	}
	for _, name := range names {
		if err := c.db.Collection(name).Drop(ctx); err != nil {
			return fmt.Errorf("drop %s: %w", name, err)
		}
	}
	return nil
}

func (c *Client) insertFoods(ctx context.Context, rows []foodSeedRow) error {
	now := time.Now().UTC()
	docs := make([]interface{}, 0, len(rows))
	for _, row := range rows {
		aliases := row.Aliases
		if aliases == nil {
			aliases = map[string][]string{}
		}
		docs = append(docs, models.FoodItem{
			Id: row.Id, Names: row.Names, Aliases: aliases, GramsPerPiece: row.GramsPerPiece, GPer100ml: row.GPer100ml,
			KcalPer100g: row.Nutrition.Kcal, ProteinG: row.Nutrition.Protein, CarbsG: row.Nutrition.Carbs,
			FatG: row.Nutrition.Fat, SaltG: row.Nutrition.Salt, SugarG: row.Nutrition.Sugar, CreatedAt: now,
		})
	}
	if _, err := c.db.Collection(foodsCollection).InsertMany(ctx, docs); err != nil {
		return fmt.Errorf("insert food seed: %w", err)
	}
	return nil
}

// remapCiqualReferences repoints every link and nutrition suggestion that
// still carries a nutrition_id.
func (c *Client) remapCiqualReferences(ctx context.Context, rows []foodSeedRow) error {
	foodByCiqualName := make(map[string]string)
	for _, row := range rows {
		for _, name := range row.Ciqual {
			foodByCiqualName[strings.ToLower(name)] = row.Id
		}
	}
	cursor, err := c.db.Collection(legacyCiqualCollection).Find(ctx, bson.D{}, options.Find().SetProjection(bson.D{{Key: "name_lower", Value: 1}}))
	if err != nil {
		return err
	}
	var entries []struct {
		ID        primitive.ObjectID `bson:"_id"`
		NameLower string             `bson:"name_lower"`
	}
	if err := cursor.All(ctx, &entries); err != nil {
		return err
	}
	foodByCiqualID := make(map[primitive.ObjectID]string, len(entries))
	for _, entry := range entries {
		if food, ok := foodByCiqualName[entry.NameLower]; ok {
			foodByCiqualID[entry.ID] = food
		}
	}
	for _, collection := range []string{nutritionLinksCollection, nutritionSuggestionsCollection} {
		if err := c.remapNutritionIDs(ctx, collection, foodByCiqualID); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) remapNutritionIDs(ctx context.Context, collection string, foodByCiqualID map[primitive.ObjectID]string) error {
	cursor, err := c.db.Collection(collection).Find(ctx, bson.D{{Key: "nutrition_id", Value: bson.D{{Key: "$exists", Value: true}}}})
	if err != nil {
		return err
	}
	var docs []struct {
		ID          primitive.ObjectID `bson:"_id"`
		Name        string             `bson:"name"`
		Ingredient  string             `bson:"ingredient_name"`
		NutritionID primitive.ObjectID `bson:"nutrition_id"`
	}
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	var writes []mongo.WriteModel
	for _, doc := range docs {
		filter := bson.D{{Key: "_id", Value: doc.ID}}
		food, ok := foodByCiqualID[doc.NutritionID]
		if !ok {
			utils.LogError("Dropped a nutrition reference with no matching food", nil, collection, doc.Name+doc.Ingredient)
			writes = append(writes, mongo.NewDeleteOneModel().SetFilter(filter))
			continue
		}
		writes = append(writes, mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(bson.D{
			{Key: "$set", Value: bson.D{{Key: "food_id", Value: food}}},
			{Key: "$unset", Value: bson.D{{Key: "nutrition_id", Value: ""}}},
		}))
	}
	if len(writes) == 0 {
		return nil
	}
	if _, err := c.db.Collection(collection).BulkWrite(ctx, writes); err != nil {
		return fmt.Errorf("remap %s: %w", collection, err)
	}
	return nil
}

// substitutionProblemPrefixes are the "no X" openings a Toolbox
// substitution's problem text used before naming the missing food.
var substitutionProblemPrefixes = []string{"no ", "pas de ", "pas d'", "ei ", "没有"}

// substituteProblemFood returns the id of the food a substitution's problem
// names, with or without a "no ..." opening, by exact name or alias in any
// locale; "" when none does.
func substituteProblemFood(problem string, foods []models.FoodItem) string {
	key := strings.ToLower(strings.TrimSpace(problem))
	candidates := []string{key}
	for _, prefix := range substitutionProblemPrefixes {
		if rest, ok := strings.CutPrefix(key, prefix); ok {
			candidates = append(candidates, strings.TrimSpace(rest))
		}
	}
	for _, food := range foods {
		for _, locale := range models.FoodLocales {
			names := append([]string{food.Names[locale]}, food.Aliases[locale]...)
			for _, name := range names {
				name = strings.ToLower(strings.TrimSpace(name))
				for _, candidate := range candidates {
					if name != "" && name == candidate {
						return food.Id
					}
				}
			}
		}
	}
	return ""
}

// migrateSubstitutions appends each Toolbox substitution's solution, in its
// source locale and every stored translation, to the substitutes of the
// food its problem names. Unmatched ones are logged and left to be dropped.
func (c *Client) migrateSubstitutions(ctx context.Context) error {
	foods, err := c.ListFoods(ctx)
	if err != nil {
		return err
	}
	var items []struct {
		Problem      string `bson:"problem"`
		Solution     string `bson:"solution"`
		SourceLocale string `bson:"source_locale"`
		Translations map[string]struct {
			Solution string `bson:"solution"`
		} `bson:"translations"`
	}
	cursor, err := c.db.Collection(legacySubstitutionsCollection).Find(ctx, bson.D{})
	if err != nil {
		return err
	}
	if err := cursor.All(ctx, &items); err != nil {
		return err
	}
	for _, item := range items {
		food := substituteProblemFood(item.Problem, foods)
		if food == "" {
			utils.LogError("Dropped a substitution with no matching food", nil, item.Problem)
			continue
		}
		lines := bson.D{}
		source := item.SourceLocale
		if source == "" {
			source = "en"
		}
		for _, locale := range models.FoodLocales {
			text := item.Translations[locale].Solution
			if locale == source {
				text = item.Solution
			}
			if text = strings.TrimSpace(text); text != "" {
				lines = append(lines, bson.E{Key: "substitutes." + locale, Value: text})
			}
		}
		if len(lines) == 0 {
			continue
		}
		if _, err := c.db.Collection(foodsCollection).UpdateOne(ctx, bson.D{{Key: "_id", Value: food}}, bson.D{{Key: "$addToSet", Value: lines}}); err != nil {
			return fmt.Errorf("move substitution %q: %w", item.Problem, err)
		}
	}
	return nil
}
