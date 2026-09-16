package mongo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/exp/slices"

	"recipes/internal/models"
	"recipes/internal/utils"
)

const recipesCollection string = "recipes"

var NotFoundError = errors.New("recipe not found")

var recipeAuthorPipeline = mongo.Pipeline{
	bson.D{{Key: "$lookup", Value: bson.D{
		{Key: "from", Value: userCollection},
		{Key: "localField", Value: "author"},
		{Key: "foreignField", Value: "_id"},
		{Key: "as", Value: "author"},
	}}},
	bson.D{{Key: "$unwind", Value: "$author"}},
}

func (c *Client) transformRecipe(cursor *mongo.Cursor) ([]models.Recipe, error) {
	var results []bson.M
	var recipes []models.Recipe

	if err := cursor.All(context.TODO(), &results); err != nil {
		return nil, err
	}

	for _, result := range results {
		var recipe models.Recipe
		bsonBytes, err := bson.Marshal(result)

		if err != nil {
			return nil, err
		}
		if err := bson.Unmarshal(bsonBytes, &recipe); err != nil {
			return nil, err
		}
		recipes = append(recipes, recipe)
	}
	return recipes, nil
}

func (c *Client) CreateRecipe(authorId string, infos *models.CreateRecipe) (models.Recipe, error) {
	authorObjectId, err := primitive.ObjectIDFromHex(authorId)
	if err != nil {
		return models.Recipe{}, err
	}

	recipe := utils.DupStruct[models.RecipeDB](infos)
	recipe.Author = &authorObjectId

	cursor, err := c.db.Collection(recipesCollection).InsertOne(context.TODO(), recipe)
	if err != nil {
		return models.Recipe{}, err
	}
	return c.GetRecipeById(cursor.InsertedID.(primitive.ObjectID).Hex())
}

func (c *Client) UpdateRecipeById(id string, recipe *models.UpdateRecipeRequest) (models.Recipe, error) {
	objectId, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Recipe{}, err
	}

	cursor := c.db.Collection(recipesCollection).FindOneAndUpdate(context.TODO(), bson.M{
		"_id": objectId,
	}, bson.M{
		"$set": recipe,
	})
	var updated models.RecipeDB
	if err := cursor.Decode(&updated); errors.Is(err, mongo.ErrNoDocuments) {
		return models.Recipe{}, NotFoundError
	} else if err != nil {
		return models.Recipe{}, err
	}
	return c.GetRecipeById(id)
}

func (c *Client) getRecipeById(id string, collection string) (models.Recipe, error) {
	objectId, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Recipe{}, err
	}

	var stages mongo.Pipeline = append([]bson.D{{
		{Key: "$match", Value: bson.D{{Key: "_id", Value: objectId}}},
	}}, recipeAuthorPipeline...)

	cursor, err := c.db.Collection(collection).Aggregate(context.TODO(), stages)
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

func (c *Client) GetRecipeById(id string) (models.Recipe, error) {
	return c.getRecipeById(id, recipesCollection)
}

func (c *Client) GetRecipeByIdForAuthor(ctx context.Context, id, authorID string) (models.Recipe, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Recipe{}, err
	}
	authorObjectID, err := primitive.ObjectIDFromHex(authorID)
	if err != nil {
		return models.Recipe{}, err
	}
	stages := append(mongo.Pipeline{{
		{Key: "$match", Value: bson.D{{Key: "_id", Value: objectID}, {Key: "author", Value: authorObjectID}}},
	}}, recipeAuthorPipeline...)
	cursor, err := c.db.Collection(recipesCollection).Aggregate(ctx, stages)
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

func (c *Client) ReplaceRecipeById(ctx context.Context, id string, recipe models.RecipeDB) (models.Recipe, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Recipe{}, err
	}
	recipe.Id = &objectID
	result := c.db.Collection(recipesCollection).FindOneAndReplace(ctx, bson.M{"_id": objectID}, recipe, options.FindOneAndReplace().SetReturnDocument(options.After))
	if err := result.Err(); errors.Is(err, mongo.ErrNoDocuments) {
		return models.Recipe{}, NotFoundError
	} else if err != nil {
		return models.Recipe{}, err
	}
	return c.GetRecipeById(id)
}

func (c *Client) DeleteLocalizedRecipesByID(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}
	_, err = c.db.Collection(translationsCollection).DeleteMany(ctx, bson.M{"recipe_id": objectID})
	return err
}

func (c *Client) DeleteRecipeById(id string) (models.RecipeDB, error) {
	var recipe models.RecipeDB
	objectId, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return recipe, err
	}

	cursor := c.db.Collection(recipesCollection).FindOneAndDelete(context.TODO(), bson.M{
		"_id": objectId,
	})
	if err := cursor.Decode(&recipe); errors.Is(err, mongo.ErrNoDocuments) {
		return recipe, NotFoundError
	} else if err != nil {
		return recipe, err
	}
	return recipe, nil
}

// variationsLookupStage attaches each candidate's own variations (never its
// root, never chained further) as a light `variations` array of {title,
// ingredients} - just enough for the search cross-match below, not a full
// recipe fetch.
var variationsLookupStage = bson.D{{Key: "$lookup", Value: bson.D{
	{Key: "from", Value: recipesCollection},
	{Key: "localField", Value: "_id"},
	{Key: "foreignField", Value: "variation_of"},
	{Key: "as", Value: "variations"},
	{Key: "pipeline", Value: mongo.Pipeline{{{Key: "$project", Value: bson.D{
		{Key: "title", Value: 1}, {Key: "ingredients", Value: 1},
	}}}}},
}}}

// familyIngredientsAllStages matches when every pattern is satisfied by the
// union of the candidate's own <fieldPrefix>.name values and every attached
// variation's ingredients.name values (see variationsLookupStage) - a
// per-ingredient union across the family, not "every ingredient within one
// single recipe": a query for "flour"+"cinnamon" matches a family where the
// root has flour and a sibling variation has cinnamon, even if no single
// recipe in the family has both. Accepted v1 behavior (decision #3).
func familyIngredientsAllStages(fieldPrefix string, patterns []interface{}) []bson.D {
	return []bson.D{
		{{Key: "$addFields", Value: bson.D{{Key: "family_ingredient_names", Value: bson.D{
			{Key: "$reduce", Value: bson.D{
				{Key: "input", Value: "$variations"},
				{Key: "initialValue", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$" + fieldPrefix + ".name", bson.A{}}}}},
				{Key: "in", Value: bson.D{{Key: "$setUnion", Value: bson.A{
					"$$value",
					bson.D{{Key: "$ifNull", Value: bson.A{"$$this.ingredients.name", bson.A{}}}},
				}}}},
			}},
		}}}}},
		{{Key: "$match", Value: bson.D{{Key: "family_ingredient_names", Value: bson.M{"$all": patterns}}}}},
		{{Key: "$unset", Value: "family_ingredient_names"}},
	}
}

// buildRecipeFilterPipeline returns the $lookup(author) plus every filter
// stage for parameters, before any count/sort/skip/limit stage is appended.
func buildRecipeFilterPipeline(parameters models.GetRecipesRequest) []bson.D {
	var pipeline []bson.D

	pipeline = append(pipeline, recipeAuthorPipeline...)

	if parameters.ExcludeFamily != "" {
		// Used by the "add recipe as ingredient" picker so a recipe can't
		// offer itself as a reference target. UX nicety only; the
		// authoritative enforcement is recipe_service.validateIngredientRefs.
		if excludeID, err := primitive.ObjectIDFromHex(parameters.ExcludeFamily); err == nil {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: bson.D{{Key: "$ne", Value: excludeID}}}}}})
		}
	}

	isFamilyListing := parameters.VariationOf == "" && !parameters.OwnRecipes

	switch {
	case parameters.VariationOf != "":
		// List only the variations of the given recipe id, never the root.
		if variationOfID, err := primitive.ObjectIDFromHex(parameters.VariationOf); err == nil {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "variation_of", Value: variationOfID}}}})
		}
	case parameters.OwnRecipes:
		// "My Recipes": no collapsing - every recipe matching Author is
		// listed flatly, roots and variations alike (see decision #4).
	default:
		// Default public discovery listing: collapse each family to its
		// root.
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "variation_of", Value: bson.D{{Key: "$exists", Value: false}}}}}})
		if parameters.Category == models.Diy {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "category", Value: models.Diy}}}})
		} else {
			// Empty or "food": exclude diy, but include both explicit "food"
			// docs and legacy docs with no category field at all (predate
			// this field) - NOT a plain equality match, see
			// GetRecipesRequest.Category's doc comment.
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "category", Value: bson.D{{Key: "$ne", Value: models.Diy}}}}}})
		}
	}

	// A title/ingredient search on the default discovery listing also
	// matches into each candidate's own variations, surfacing the root when
	// only a variation's text matches (decision #3) - never applied to the
	// variation_of/own_recipes listings above, which aren't being viewed as
	// "families" in that sense.
	includeVariationsMatch := isFamilyListing &&
		(parameters.Title != "" || len(parameters.Ingredients) > 0)
	if isFamilyListing {
		// Also needed (beyond the search cross-match above) to compute
		// family_sort_id below: a new variation should bump its root back to
		// the top of the newest-first default listing, so the variation
		// actually gets seen instead of sitting undiscoverable under
		// whenever the root itself was created.
		pipeline = append(pipeline, variationsLookupStage)
		pipeline = append(pipeline, bson.D{{Key: "$addFields", Value: bson.D{
			{Key: "family_sort_id", Value: bson.D{{Key: "$max", Value: bson.D{{Key: "$concatArrays", Value: bson.A{
				bson.A{"$_id"}, "$variations._id",
			}}}}}},
		}}})
	}

	if parameters.SearchLocale != "" && (parameters.Title != "" || len(parameters.Ingredients) > 0) {
		// Search the recipe_translations row for search_locale when one
		// exists. Otherwise, only fall back to the canonical fields when the
		// recipe's own source_locale IS search_locale — a translation row
		// never exists for a recipe's own source locale (translating a
		// language into itself is skipped, see targetLocalesFor), so without
		// this narrow fallback a same-locale recipe would wrongly drop out
		// of search. A recipe in a different, not-yet-translated (or never
		// configured) locale must NOT match on its unrelated-language
		// canonical text — it should simply not show up until it has an
		// actual translation into search_locale.
		pipeline = append(pipeline, bson.D{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: translationsCollection},
			{Key: "let", Value: bson.D{{Key: "recipeID", Value: "$_id"}}},
			{Key: "pipeline", Value: mongo.Pipeline{{{Key: "$match", Value: bson.D{
				{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$recipe_id", "$$recipeID"}}}},
				{Key: "locale", Value: parameters.SearchLocale},
			}}}}},
			{Key: "as", Value: "search_translation"},
		}}}, bson.D{{Key: "$addFields", Value: bson.D{
			{Key: "search_has_translation", Value: bson.D{{Key: "$gt", Value: bson.A{
				bson.D{{Key: "$size", Value: "$search_translation"}}, 0,
			}}}},
			{Key: "search_own_locale", Value: bson.D{{Key: "$eq", Value: bson.A{"$source_locale", parameters.SearchLocale}}}},
		}}}, bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{
			{Key: "$or", Value: bson.A{"$search_has_translation", "$search_own_locale"}},
		}}}}}, bson.D{{Key: "$addFields", Value: bson.D{
			{Key: "search_title", Value: bson.D{{Key: "$cond", Value: bson.D{
				{Key: "if", Value: "$search_has_translation"},
				{Key: "then", Value: bson.D{{Key: "$first", Value: "$search_translation.title"}}},
				{Key: "else", Value: "$title"},
			}}}},
			{Key: "search_ingredients", Value: bson.D{{Key: "$cond", Value: bson.D{
				{Key: "if", Value: "$search_has_translation"},
				{Key: "then", Value: bson.D{{Key: "$first", Value: "$search_translation.ingredients"}}},
				{Key: "else", Value: "$ingredients"},
			}}}},
		}}})
		if parameters.Title != "" {
			titleMatch := bson.D{{Key: "search_title", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Title), Options: "i"}}}
			if includeVariationsMatch {
				pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{
					titleMatch,
					bson.D{{Key: "variations.title", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Title), Options: "i"}}},
				}}}}})
			} else {
				pipeline = append(pipeline, bson.D{{Key: "$match", Value: titleMatch}})
			}
		}
		if len(parameters.Ingredients) > 0 {
			ingredients := make([]interface{}, 0, len(parameters.Ingredients))
			for _, ingredient := range parameters.Ingredients {
				ingredients = append(ingredients, primitive.Regex{Pattern: regexp.QuoteMeta(ingredient), Options: "i"})
			}
			if includeVariationsMatch {
				pipeline = append(pipeline, familyIngredientsAllStages("search_ingredients", ingredients)...)
			} else {
				pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "search_ingredients.name", Value: bson.M{"$all": ingredients}}}}})
			}
		}
		pipeline = append(pipeline, bson.D{{Key: "$unset", Value: bson.A{"search_translation", "search_has_translation", "search_own_locale", "search_title", "search_ingredients"}}})
	} else if parameters.Title != "" {
		if includeVariationsMatch {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{
				bson.D{{Key: "title", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Title), Options: "i"}}},
				bson.D{{Key: "variations.title", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Title), Options: "i"}}},
			}}}}})
		} else {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "title", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Title), Options: "i"}}}}})
		}
	}
	if parameters.Author != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "author.username", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Author), Options: "i"}}}}})
	}
	if parameters.Kind != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "kind", Value: primitive.Regex{Pattern: regexp.QuoteMeta(string(parameters.Kind)), Options: "i"}}}}})
	}

	if len(parameters.Ingredients) > 0 && parameters.SearchLocale == "" {
		ingredients := make([]interface{}, 0, len(parameters.Ingredients))
		for _, ingredient := range parameters.Ingredients {
			ingredients = append(ingredients, primitive.Regex{Pattern: regexp.QuoteMeta(ingredient), Options: "i"})
		}
		if includeVariationsMatch {
			pipeline = append(pipeline, familyIngredientsAllStages("ingredients", ingredients)...)
		} else {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "ingredients.name", Value: bson.M{"$all": ingredients}}}}})
		}
	}

	if isFamilyListing {
		pipeline = append(pipeline, bson.D{{Key: "$unset", Value: "variations"}})
	}

	return pipeline
}

// buildRecipeSortStages returns the sort stage(s) for parameters: either the
// plain newest-first sort, or (when a preparation/total time target is given)
// a sort by closeness to that target, ties broken newest-first. On the
// default family-collapsed listing, "newest" is family_sort_id (see
// buildRecipeFilterPipeline) rather than the root's own _id, so a fresh
// variation promotes its whole family back to the top instead of sitting
// buried under however old the root is.
func buildRecipeSortStages(parameters models.GetRecipesRequest) []bson.D {
	sortKey := "_id"
	if parameters.VariationOf == "" && !parameters.OwnRecipes {
		sortKey = "family_sort_id"
	}

	if parameters.PreparationTime == 0 && parameters.TotalTime == 0 {
		return []bson.D{{{Key: "$sort", Value: bson.D{{Key: sortKey, Value: -1}}}}}
	}

	var stages []bson.D
	differenceFields := bson.A{}
	// preparation_time/cooking_time/resting_time are plain ints with
	// `omitempty` bson tags, so a zero-minute value is stored as a missing
	// field rather than 0. $add/$subtract return null (not 0) for a missing
	// field, and null sorts before every real number - without $ifNull, any
	// recipe missing one of these fields would always sort first regardless
	// of actual closeness to the target.
	zeroIfMissing := func(field string) bson.D {
		return bson.D{{Key: "$ifNull", Value: bson.A{field, 0}}}
	}
	if parameters.PreparationTime != 0 {
		stages = append(stages, bson.D{{Key: "$addFields", Value: bson.D{
			{Key: "diffPrepTime", Value: bson.D{
				{Key: "$abs", Value: bson.A{
					bson.D{{Key: "$subtract", Value: bson.A{zeroIfMissing("$preparation_time"), parameters.PreparationTime}}},
				}},
			}},
		}}})
		differenceFields = append(differenceFields, "$diffPrepTime")
	}
	if parameters.TotalTime != 0 {
		stages = append(stages, bson.D{{Key: "$addFields", Value: bson.D{
			{Key: "diffTotalTime", Value: bson.D{
				{Key: "$abs", Value: bson.A{
					bson.D{{Key: "$subtract", Value: bson.A{
						bson.D{{Key: "$add", Value: bson.A{
							zeroIfMissing("$preparation_time"),
							zeroIfMissing("$cooking_time"),
							zeroIfMissing("$resting_time"),
						}}},
						parameters.TotalTime,
					}}},
				}},
			}},
		}}})
		differenceFields = append(differenceFields, "$diffTotalTime")
	}
	stages = append(stages, bson.D{{Key: "$addFields", Value: bson.D{
		{Key: "combinedDifference", Value: bson.D{
			{Key: "$add", Value: differenceFields},
		}},
	}}}, bson.D{{Key: "$sort", Value: bson.D{
		{Key: "combinedDifference", Value: 1},
		{Key: sortKey, Value: -1},
	}}})
	return stages
}

func (c *Client) GetRecipeDocuments(parameters models.GetRecipesRequest) ([]models.Recipe, int64, error) {
	pipeline := buildRecipeFilterPipeline(parameters)

	// Count total number of documents before sorting without limit and skip
	cursor, err := c.db.Collection(recipesCollection).Aggregate(context.TODO(), append(slices.Clone(pipeline), bson.D{{Key: "$count", Value: "total"}}))
	if err != nil {
		return nil, 0, err
	}

	var count int64
	if cursor.Next(context.Background()) {
		var result struct {
			Total int64 `bson:"total"`
		}
		if err := cursor.Decode(&result); err != nil {
			return nil, 0, err
		}
		count = result.Total
	}

	pipeline = append(pipeline, buildRecipeSortStages(parameters)...)

	// Apply limit and offset for pagination
	if parameters.Offset != 0 {
		pipeline = append(pipeline, bson.D{{Key: "$skip", Value: parameters.Offset}})
	}
	if parameters.Limit != 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: parameters.Limit}})
	} else {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: 15}})
	}

	cursor, err = c.db.Collection(recipesCollection).Aggregate(context.TODO(), pipeline)
	if err != nil {
		return nil, 0, err
	}

	recipes, err := c.transformRecipe(cursor)
	return recipes, count, err
}

// boostedFavoritesCap caps how many favorited-matching recipes
// GetRecipeDocumentsBoosted returns in its unpaginated block, as a safety net
// against an unbounded response for a pathologically large favorites list.
const boostedFavoritesCap = 500

// GetRecipeDocumentsBoosted partitions the recipes matching parameters into
// every one userID has favorited (returned in full, up to boostedFavoritesCap,
// only on the Offset == 0 call) and the remaining non-favorited matches
// (paginated per parameters.Offset/Limit). Offset addresses the non-favorited
// segment only: a caller pages through it by incrementing Offset over the
// count of non-favorited items each page actually returned (favorited items
// don't consume any of the offset budget), and never re-requests offset 0
// mid-session — that's what makes favoriting/unfavoriting mid-scroll shift at
// most the non-favorited tail once, instead of reshuffling everything above
// the caller's current position.
func (c *Client) GetRecipeDocumentsBoosted(ctx context.Context, userID string, parameters models.GetRecipesRequest) ([]models.Recipe, []models.Recipe, int64, error) {
	favoriteIDs, err := c.favoritedRecipeIDs(ctx, userID)
	if err != nil {
		return nil, nil, 0, err
	}

	base := buildRecipeFilterPipeline(parameters)
	sortStages := buildRecipeSortStages(parameters)

	countMatching := func(extra bson.D) (int64, error) {
		pipeline := append(slices.Clone(base), extra, bson.D{{Key: "$count", Value: "total"}})
		cursor, err := c.db.Collection(recipesCollection).Aggregate(ctx, pipeline)
		if err != nil {
			return 0, err
		}
		var result struct {
			Total int64 `bson:"total"`
		}
		if cursor.Next(ctx) {
			if err := cursor.Decode(&result); err != nil {
				return 0, err
			}
		}
		return result.Total, nil
	}
	fetchMatching := func(extra bson.D, skip, limit int) ([]models.Recipe, error) {
		pipeline := append(slices.Clone(base), extra)
		pipeline = append(pipeline, sortStages...)
		if skip > 0 {
			pipeline = append(pipeline, bson.D{{Key: "$skip", Value: skip}})
		}
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: limit}})
		cursor, err := c.db.Collection(recipesCollection).Aggregate(ctx, pipeline)
		if err != nil {
			return nil, err
		}
		return c.transformRecipe(cursor)
	}

	inFavorites := bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: favoriteIDs}}}}}}
	notInFavorites := bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: bson.D{{Key: "$nin", Value: favoriteIDs}}}}}}

	favoritedCount, err := countMatching(inFavorites)
	if err != nil {
		return nil, nil, 0, err
	}
	restCount, err := countMatching(notInFavorites)
	if err != nil {
		return nil, nil, 0, err
	}

	// The favorited block is only ever included on the first page (Offset ==
	// 0): a caller pages through the non-favorited remainder by incrementing
	// Offset over the count of non-favorited items each page actually
	// returned, never re-requesting offset 0 mid-session, so returning the
	// block again on later pages would just duplicate it.
	var favorited []models.Recipe
	if parameters.Offset == 0 {
		favorited, err = fetchMatching(inFavorites, 0, boostedFavoritesCap)
		if err != nil {
			return nil, nil, 0, err
		}
	}

	limit := parameters.Limit
	if limit == 0 {
		limit = 15
	}
	rest, err := fetchMatching(notInFavorites, parameters.Offset, limit)
	if err != nil {
		return nil, nil, 0, err
	}

	return favorited, rest, favoritedCount + restCount, nil
}

func (c *Client) GetRecipesByAuthor(ctx context.Context, authorID, cursorID string, limit int) ([]models.Recipe, int64, error) {
	authorObjectID, err := primitive.ObjectIDFromHex(authorID)
	if err != nil {
		return nil, 0, err
	}
	match := bson.D{{Key: "author", Value: authorObjectID}}
	if cursorID != "" {
		cursorObjectID, err := primitive.ObjectIDFromHex(cursorID)
		if err != nil {
			return nil, 0, err
		}
		match = append(match, bson.E{Key: "_id", Value: bson.M{"$lt": cursorObjectID}})
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$sort", Value: bson.D{{Key: "_id", Value: -1}}}},
		{{Key: "$limit", Value: limit}},
	}
	pipeline = append(pipeline, recipeAuthorPipeline...)
	dbCursor, err := c.db.Collection(recipesCollection).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	recipes, err := c.transformRecipe(dbCursor)
	if err != nil {
		return nil, 0, err
	}
	count, err := c.db.Collection(recipesCollection).CountDocuments(ctx, bson.M{"author": authorObjectID})
	return recipes, count, err
}

func (c *Client) RecipeConflicts(recipe models.RecipeDB) (models.RecipeDB, error) {
	val := reflect.ValueOf(&recipe).Elem()
	typ := val.Type()
	dbRecipe := models.RecipeDB{}

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldInfos := typ.Field(i)

		if field.IsZero() {
			continue
		}

		jsonName := fieldInfos.Tag.Get("json")
		// Split to remove potential ,omitempty
		bsonName := strings.Split(fieldInfos.Tag.Get("bson"), ",")[0]

		if err := c.db.Collection(recipesCollection).FindOne(context.TODO(), bson.M{bsonName: field.Interface()}).Decode(&dbRecipe); err == nil {
			return dbRecipe, fmt.Errorf("%s is already taken", jsonName)
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return models.RecipeDB{}, fmt.Errorf("check recipe conflict: %w", err)
		}
	}

	return models.RecipeDB{}, nil
}

// GetOldestVariationID returns the id of rootID's oldest remaining variation
// (by creation order), or (nil, nil) if it has none - a missing result is a
// valid zero-state, not an error, matching the convention already used by
// GetFavoriteInfo/favoritedRecipeIDs for "nothing found".
func (c *Client) GetOldestVariationID(ctx context.Context, rootID string) (*primitive.ObjectID, error) {
	rootObjectID, err := primitive.ObjectIDFromHex(rootID)
	if err != nil {
		return nil, err
	}
	var doc struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	err = c.db.Collection(recipesCollection).FindOne(ctx,
		bson.M{"variation_of": rootObjectID},
		options.FindOne().SetSort(bson.D{{Key: "_id", Value: 1}}).SetProjection(bson.M{"_id": 1}),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &doc.ID, nil
}

// RepointVariations re-points every variation of oldRootID (other than
// newRootID itself) to newRootID.
func (c *Client) RepointVariations(ctx context.Context, oldRootID, newRootID string) error {
	oldRootObjectID, err := primitive.ObjectIDFromHex(oldRootID)
	if err != nil {
		return err
	}
	newRootObjectID, err := primitive.ObjectIDFromHex(newRootID)
	if err != nil {
		return err
	}
	_, err = c.db.Collection(recipesCollection).UpdateMany(ctx,
		bson.M{"variation_of": oldRootObjectID, "_id": bson.M{"$ne": newRootObjectID}},
		bson.M{"$set": bson.M{"variation_of": newRootObjectID}},
	)
	return err
}

// PromoteRecipeToRoot clears id's variation_of, making it a root.
func (c *Client) PromoteRecipeToRoot(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}
	_, err = c.db.Collection(recipesCollection).UpdateOne(ctx,
		bson.M{"_id": objectID},
		bson.M{"$unset": bson.M{"variation_of": ""}},
	)
	return err
}

// SetVariationOf points recipeID at rootID, turning a standalone recipe into
// a variation - the counterpart of PromoteRecipeToRoot, which clears the
// field back off.
func (c *Client) SetVariationOf(ctx context.Context, recipeID, rootID string) error {
	objectID, err := primitive.ObjectIDFromHex(recipeID)
	if err != nil {
		return err
	}
	rootObjectID, err := primitive.ObjectIDFromHex(rootID)
	if err != nil {
		return err
	}
	_, err = c.db.Collection(recipesCollection).UpdateOne(ctx,
		bson.M{"_id": objectID},
		bson.M{"$set": bson.M{"variation_of": rootObjectID}},
	)
	return err
}

// GetVariationCounts returns, for each given root id, how many variations it
// has. A root with none is simply absent from the map.
func (c *Client) GetVariationCounts(ctx context.Context, rootIDs []string) (map[string]int64, error) {
	result := make(map[string]int64, len(rootIDs))
	objectIDs := make([]primitive.ObjectID, 0, len(rootIDs))
	for _, id := range rootIDs {
		objectID, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			return nil, err
		}
		objectIDs = append(objectIDs, objectID)
	}
	if len(objectIDs) == 0 {
		return result, nil
	}
	cursor, err := c.db.Collection(recipesCollection).Aggregate(ctx, bson.A{
		bson.D{{Key: "$match", Value: bson.D{{Key: "variation_of", Value: bson.D{{Key: "$in", Value: objectIDs}}}}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$variation_of"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
	})
	if err != nil {
		return nil, err
	}
	var docs []struct {
		ID    primitive.ObjectID `bson:"_id"`
		Count int64              `bson:"count"`
	}
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	for _, doc := range docs {
		result[doc.ID.Hex()] = doc.Count
	}
	return result, nil
}

// RecipeCountsByCategory returns the number of recipes per category, keyed by
// category string ("food"/"diy"). Legacy documents with no category field
// (predating GetRecipesRequest.Category, see buildRecipeFilterPipeline) count
// as "food", matching how the default listing already treats them.
func (c *Client) RecipeCountsByCategory(ctx context.Context) (map[string]int64, error) {
	cursor, err := c.db.Collection(recipesCollection).Aggregate(ctx, bson.A{
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$category", string(models.Food)}}}},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
	})
	if err != nil {
		return nil, err
	}
	var docs []struct {
		ID    string `bson:"_id"`
		Count int64  `bson:"count"`
	}
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(docs))
	for _, doc := range docs {
		result[doc.ID] = doc.Count
	}
	return result, nil
}

// RepointRecipeReferences updates every ingredients[].recipe_ref equal to
// oldRootID (across all recipes) to point at newRootID instead - the
// cross-recipe-reference analogue of RepointVariations, needing a filtered
// positional array update since recipe_ref lives inside an ingredients
// array, not a top-level scalar.
func (c *Client) RepointRecipeReferences(ctx context.Context, oldRootID, newRootID string) error {
	oldRootObjectID, err := primitive.ObjectIDFromHex(oldRootID)
	if err != nil {
		return err
	}
	newRootObjectID, err := primitive.ObjectIDFromHex(newRootID)
	if err != nil {
		return err
	}
	_, err = c.db.Collection(recipesCollection).UpdateMany(ctx,
		bson.M{"ingredients.recipe_ref": oldRootObjectID},
		bson.M{"$set": bson.M{"ingredients.$[elem].recipe_ref": newRootObjectID}},
		options.Update().SetArrayFilters(options.ArrayFilters{
			Filters: bson.A{bson.M{"elem.recipe_ref": oldRootObjectID}},
		}),
	)
	return err
}

// HasIncomingReferences reports whether any recipe's ingredients reference
// recipeID - the existence-only counterpart of the reverse
// Ingredient.RecipeRef edge, powering Delete's block-check. Uses the same
// sparse ingredients.recipe_ref index as RepointRecipeReferences.
func (c *Client) HasIncomingReferences(ctx context.Context, recipeID string) (bool, error) {
	objectID, err := primitive.ObjectIDFromHex(recipeID)
	if err != nil {
		return false, err
	}
	count, err := c.db.Collection(recipesCollection).CountDocuments(ctx,
		bson.M{"ingredients.recipe_ref": objectID},
		options.Count().SetLimit(1),
	)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// FamilyReferencesRecipe reports whether any recipe in familyRootID's family
// (the root itself or any of its variations) has an ingredient referencing
// targetID - the family-scoped counterpart of HasIncomingReferences, powering
// LinkVariation's self-reference-loop check.
func (c *Client) FamilyReferencesRecipe(ctx context.Context, familyRootID, targetID string) (bool, error) {
	rootObjectID, err := primitive.ObjectIDFromHex(familyRootID)
	if err != nil {
		return false, err
	}
	targetObjectID, err := primitive.ObjectIDFromHex(targetID)
	if err != nil {
		return false, err
	}
	count, err := c.db.Collection(recipesCollection).CountDocuments(ctx,
		bson.M{
			"$or":                    bson.A{bson.M{"_id": rootObjectID}, bson.M{"variation_of": rootObjectID}},
			"ingredients.recipe_ref": targetObjectID,
		},
		options.Count().SetLimit(1),
	)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetRecipeTitles returns each id's current title, for cheaply resolving a
// reference ingredient's display title in bulk (one query regardless of how
// many recipes/ingredients reference the same or different targets). An id
// with no matching document (a dangling reference - shouldn't happen given
// write-time validation, tolerated defensively) is simply absent from the
// result.
func (c *Client) GetRecipeTitles(ctx context.Context, ids []string) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	objectIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		if objectID, err := primitive.ObjectIDFromHex(id); err == nil {
			objectIDs = append(objectIDs, objectID)
		}
	}
	if len(objectIDs) == 0 {
		return result, nil
	}
	cursor, err := c.db.Collection(recipesCollection).Find(ctx,
		bson.M{"_id": bson.M{"$in": objectIDs}},
		options.Find().SetProjection(bson.M{"title": 1}),
	)
	if err != nil {
		return nil, err
	}
	var docs []struct {
		ID    primitive.ObjectID `bson:"_id"`
		Title string             `bson:"title"`
	}
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	for _, doc := range docs {
		result[doc.ID.Hex()] = doc.Title
	}
	return result, nil
}
