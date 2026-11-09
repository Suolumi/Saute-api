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
	if recipe.FamilyID == nil {
		id := primitive.NewObjectID()
		recipe.Id = &id
		recipe.FamilyID = &id
	}

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

// isFamilyListingRequest reports whether parameters selects the default,
// family-collapsed discovery listing (as opposed to family/own_recipes/
// favorites_only/flat, which are flat).
func isFamilyListingRequest(parameters models.GetRecipesRequest) bool {
	return parameters.Family == "" && !parameters.OwnRecipes && !parameters.FavoritesOnly && !parameters.Flat
}

// familyCollapseStages keeps, per family, the newest recipe that survived the
// filters before it. "matched" counts the family's matching recipes, for
// recipeCountStage; callers $unset it before fetching items.
var familyCollapseStages = []bson.D{
	{{Key: "$sort", Value: bson.D{{Key: "_id", Value: -1}}}},
	{{Key: "$group", Value: bson.D{
		// A recipe missing family_id (only possible before
		// MigrateRecipeFamilies ran) stays its own family.
		{Key: "_id", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$family_id", "$_id"}}}},
		{Key: "doc", Value: bson.D{{Key: "$first", Value: "$$ROOT"}}},
		{Key: "matched", Value: bson.D{{Key: "$sum", Value: 1}}},
	}}},
	{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: bson.D{{Key: "$mergeObjects", Value: bson.A{
		"$doc", bson.D{{Key: "matched", Value: "$matched"}},
	}}}}}}},
}

// buildRecipeFilterPipeline returns the $lookup(author) plus every filter
// stage for parameters, before any count/sort/skip/limit stage is appended.
// On the family-collapsed listing (isFamilyListingRequest), the filters apply
// per recipe, then each family collapses to its newest matching recipe, which
// still carries "matched" (see familyCollapseStages).
func buildRecipeFilterPipeline(parameters models.GetRecipesRequest) []bson.D {
	var pipeline []bson.D

	pipeline = append(pipeline, recipeAuthorPipeline...)

	if parameters.ExcludeFamily != "" {
		if excludeID, err := primitive.ObjectIDFromHex(parameters.ExcludeFamily); err == nil {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "family_id", Value: bson.D{{Key: "$ne", Value: excludeID}}}}}})
		}
	}

	isFamilyListing := isFamilyListingRequest(parameters)

	switch {
	case parameters.Family != "":
		familyID, err := primitive.ObjectIDFromHex(parameters.Family)
		if err != nil {
			familyID = primitive.NilObjectID
		}
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "family_id", Value: familyID}}}})
	case parameters.FavoritesOnly:
		// "My Favorites": flat, exact-recipe listing narrowed to whatever
		// FavoritedByUserID has favorited - never family-resolved, unlike the
		// boosting path's favoritedFamilyIDs, since the exact version
		// favorited is what should show up here.
		if userObjectID, err := primitive.ObjectIDFromHex(parameters.FavoritedByUserID); err == nil {
			pipeline = append(pipeline, bson.D{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: favoritesCollection},
				{Key: "let", Value: bson.D{{Key: "recipeID", Value: "$_id"}}},
				{Key: "pipeline", Value: mongo.Pipeline{{{Key: "$match", Value: bson.D{
					{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$recipe", "$$recipeID"}}}},
					{Key: "users", Value: userObjectID},
				}}}}},
				{Key: "as", Value: "own_favorite"},
			}}})
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "own_favorite", Value: bson.D{{Key: "$ne", Value: bson.A{}}}}}}})
			pipeline = append(pipeline, bson.D{{Key: "$unset", Value: "own_favorite"}})
		} else {
			// No caller id resolved (Service.List guards against this in
			// practice) - fail closed instead of leaking every recipe as
			// "favorited".
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: primitive.NilObjectID}}}})
		}
		// Always narrows by category, like the default discovery listing -
		// unlike OwnRecipes below, since My Favorites is split into separate
		// Recipes/DIY sub-tabs rather than being category-agnostic.
		if parameters.Category == models.Diy {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "category", Value: models.Diy}}}})
		} else {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "category", Value: bson.D{{Key: "$ne", Value: models.Diy}}}}}})
		}
	case parameters.OwnRecipes:
		// "My Recipes"/admin moderation: every recipe matching Author, never
		// collapsed. Category is otherwise agnostic here, except an explicit
		// "diy" narrows to just DIY entries - the admin back-office's DIY tab
		// reuses this same flat listing with category=diy.
		if parameters.Category == models.Diy {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "category", Value: models.Diy}}}})
		}
	default:
		// Default public discovery listing (collapsed unless Flat).
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
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "search_title", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Title), Options: "i"}}}}})
		}
		if len(parameters.Ingredients) > 0 {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "search_ingredients.name", Value: bson.M{"$all": ingredientPatterns(parameters.Ingredients)}}}}})
		}
		pipeline = append(pipeline, bson.D{{Key: "$unset", Value: bson.A{"search_translation", "search_has_translation", "search_own_locale", "search_title", "search_ingredients"}}})
	} else {
		if parameters.Title != "" {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "title", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Title), Options: "i"}}}}})
		}
		if len(parameters.Ingredients) > 0 {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "ingredients.name", Value: bson.M{"$all": ingredientPatterns(parameters.Ingredients)}}}}})
		}
	}
	if parameters.Author != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "author.username", Value: primitive.Regex{Pattern: regexp.QuoteMeta(parameters.Author), Options: "i"}}}}})
	}
	if parameters.Kind != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "kind", Value: primitive.Regex{Pattern: regexp.QuoteMeta(string(parameters.Kind)), Options: "i"}}}}})
	}
	if len(parameters.Tags) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "tags", Value: bson.M{"$all": parameters.Tags}}}}})
	}

	if isFamilyListing {
		pipeline = append(pipeline, familyCollapseStages...)
	}

	if parameters.Popular {
		// Only needed for the "Most Popular" sort - skipped otherwise to
		// avoid the extra $lookup on every listing.
		pipeline = append(pipeline, popularityCountStages(isFamilyListing)...)
	}

	return pipeline
}

// ingredientPatterns turns ingredient search terms into case-insensitive
// literal regexes for an $all match.
func ingredientPatterns(terms []string) []interface{} {
	patterns := make([]interface{}, 0, len(terms))
	for _, term := range terms {
		patterns = append(patterns, primitive.Regex{Pattern: regexp.QuoteMeta(term), Options: "i"})
	}
	return patterns
}

// recipeCountStage returns the aggregation stage that reduces a filtered
// pipeline down to a single {total: N} document (or no document at all if
// nothing matched, same as $count). On the family-collapsed listing it sums
// "matched", so the total counts every matching recipe, not each family once.
func recipeCountStage(isFamilyListing bool) bson.D {
	if !isFamilyListing {
		return bson.D{{Key: "$count", Value: "total"}}
	}
	return bson.D{{Key: "$group", Value: bson.D{
		{Key: "_id", Value: nil},
		{Key: "total", Value: bson.D{{Key: "$sum", Value: "$matched"}}},
	}}}
}

// buildRecipeSortStages returns the sort stage(s) for parameters: the plain
// newest-first sort; a sort by closeness to a target when a preparation/
// total time target is given; for the "Quickest" preset (QuickestPrep/
// QuickestTotal), a sort by the actual time ascending; or, for "Most Popular"
// (Popular), a sort by popularity_count (computed in buildRecipeFilterPipeline)
// descending - ties broken newest-first in every one of these filtered cases.
// On the family-collapsed listing, a family sorts by its representative.
func buildRecipeSortStages(parameters models.GetRecipesRequest) []bson.D {
	if parameters.FavoritesOnly {
		return favoritesSortStages(parameters)
	}

	const sortKey = "_id"

	if parameters.Popular {
		return []bson.D{{{Key: "$sort", Value: bson.D{
			{Key: "popularity_count", Value: -1},
			{Key: sortKey, Value: -1},
		}}}}
	}

	if stages, field, ok := quickestSortStage(parameters); ok {
		stages = append(stages, bson.D{{Key: "$sort", Value: bson.D{
			{Key: field, Value: 1},
			{Key: sortKey, Value: -1},
		}}})
		return stages
	}

	if parameters.PreparationTime == 0 && parameters.TotalTime == 0 {
		return []bson.D{{{Key: "$sort", Value: bson.D{{Key: sortKey, Value: -1}}}}}
	}

	stages, differenceFields := timeDifferenceStages(parameters)
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

// zeroIfMissing treats a missing preparation_time/cooking_time/resting_time
// field as 0. Those fields are plain ints with `omitempty` bson tags, so a
// zero-minute value is stored as a missing field rather than 0; $add/
// $subtract return null (not 0) for a missing field, and null sorts before
// every real number - without this, any recipe missing one of these fields
// would always sort first regardless of its actual time.
func zeroIfMissing(field string) bson.D {
	return bson.D{{Key: "$ifNull", Value: bson.A{field, 0}}}
}

// quickestSortStage returns the $addFields stage plus the field name to sort
// ascending by, for the "Quickest" ready-in preset (QuickestPrep/
// QuickestTotal): the recipe's actual preparation/total time, rather than
// closeness to a target minutes value. ok is false when neither is set.
func quickestSortStage(parameters models.GetRecipesRequest) (stages []bson.D, field string, ok bool) {
	switch {
	case parameters.QuickestPrep:
		return []bson.D{{{Key: "$addFields", Value: bson.D{
			{Key: "quickestTime", Value: zeroIfMissing("$preparation_time")},
		}}}}, "quickestTime", true
	case parameters.QuickestTotal:
		return []bson.D{{{Key: "$addFields", Value: bson.D{
			{Key: "quickestTime", Value: bson.D{{Key: "$add", Value: bson.A{
				zeroIfMissing("$preparation_time"),
				zeroIfMissing("$cooking_time"),
				zeroIfMissing("$resting_time"),
			}}}},
		}}}}, "quickestTime", true
	default:
		return nil, "", false
	}
}

// popularityCountStages computes popularity_count, the metric behind the
// "Most Popular" sort. On the family-collapsed listing it's the number of
// distinct people who favorited any version of the representative's family,
// matching what recipe_service.decorateFamilyFavorite displays. Every flat
// listing gets each recipe's own count instead.
func popularityCountStages(isFamilyListing bool) []bson.D {
	if isFamilyListing {
		return []bson.D{
			{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: recipesCollection},
				{Key: "localField", Value: "family_id"},
				{Key: "foreignField", Value: "family_id"},
				{Key: "as", Value: "family_members"},
				{Key: "pipeline", Value: mongo.Pipeline{{{Key: "$project", Value: bson.D{{Key: "_id", Value: 1}}}}}},
			}}},
			{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: favoritesCollection},
				{Key: "localField", Value: "family_members._id"},
				{Key: "foreignField", Value: "recipe"},
				{Key: "as", Value: "family_favorites"},
			}}},
			{{Key: "$addFields", Value: bson.D{
				{Key: "popularity_count", Value: bson.D{{Key: "$size", Value: bson.D{{Key: "$reduce", Value: bson.D{
					{Key: "input", Value: "$family_favorites.users"},
					{Key: "initialValue", Value: bson.A{}},
					{Key: "in", Value: bson.D{{Key: "$setUnion", Value: bson.A{"$$value", "$$this"}}}},
				}}}}}},
			}}},
			{{Key: "$unset", Value: bson.A{"family_members", "family_favorites"}}},
		}
	}
	return []bson.D{
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: favoritesCollection},
			{Key: "localField", Value: "_id"},
			{Key: "foreignField", Value: "recipe"},
			{Key: "as", Value: "own_favorites"},
		}}},
		{{Key: "$addFields", Value: bson.D{
			{Key: "popularity_count", Value: bson.D{{Key: "$size", Value: bson.D{
				{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$own_favorites.users"}}, bson.A{}}},
			}}}},
		}}},
		{{Key: "$unset", Value: "own_favorites"}},
	}
}

// timeDifferenceStages returns the $addFields stage(s) computing, per
// candidate document, its absolute difference from parameters'
// preparation/total time target(s) - shared by buildRecipeSortStages and
// favoritesSortStages, which each combine and sort by these fields
// differently (newest-first vs alphabetical ties).
func timeDifferenceStages(parameters models.GetRecipesRequest) ([]bson.D, bson.A) {
	var stages []bson.D
	differenceFields := bson.A{}
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
	return stages, differenceFields
}

// favoritesSortStages sorts My Favorites alphabetically by title
// (case-insensitive) by default - unlike every other listing, which defaults
// to newest-first. A ready-in-time filter (a target, or the "Quickest"
// preset) or "Most Popular" still takes priority (matching every other
// listing's behavior for the same filters), with ties broken alphabetically
// instead of newest-first.
func favoritesSortStages(parameters models.GetRecipesRequest) []bson.D {
	addSortTitle := bson.D{{Key: "$addFields", Value: bson.D{{Key: "sort_title", Value: bson.D{{Key: "$toLower", Value: "$title"}}}}}}
	unsetSortTitle := bson.D{{Key: "$unset", Value: "sort_title"}}

	if parameters.Popular {
		return []bson.D{addSortTitle, {{Key: "$sort", Value: bson.D{
			{Key: "popularity_count", Value: -1},
			{Key: "sort_title", Value: 1},
		}}}, unsetSortTitle}
	}

	if stages, field, ok := quickestSortStage(parameters); ok {
		stages = append(stages, addSortTitle, bson.D{{Key: "$sort", Value: bson.D{
			{Key: field, Value: 1},
			{Key: "sort_title", Value: 1},
		}}}, unsetSortTitle)
		return stages
	}

	if parameters.PreparationTime == 0 && parameters.TotalTime == 0 {
		return []bson.D{addSortTitle, {{Key: "$sort", Value: bson.D{{Key: "sort_title", Value: 1}}}}, unsetSortTitle}
	}

	stages, differenceFields := timeDifferenceStages(parameters)
	stages = append(stages, addSortTitle, bson.D{{Key: "$addFields", Value: bson.D{
		{Key: "combinedDifference", Value: bson.D{
			{Key: "$add", Value: differenceFields},
		}},
	}}}, bson.D{{Key: "$sort", Value: bson.D{
		{Key: "combinedDifference", Value: 1},
		{Key: "sort_title", Value: 1},
	}}}, unsetSortTitle)
	return stages
}

func (c *Client) GetRecipeDocuments(parameters models.GetRecipesRequest) ([]models.Recipe, int64, error) {
	pipeline := buildRecipeFilterPipeline(parameters)
	isFamilyListing := isFamilyListingRequest(parameters)

	// Count total number of documents before sorting without limit and skip
	cursor, err := c.db.Collection(recipesCollection).Aggregate(context.TODO(), append(slices.Clone(pipeline), recipeCountStage(isFamilyListing)))
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

	if isFamilyListing {
		pipeline = append(pipeline, bson.D{{Key: "$unset", Value: "matched"}})
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
// every one whose family userID has favorited (returned in full, up to boostedFavoritesCap,
// only on the Offset == 0 call) and the remaining non-favorited matches
// (paginated per parameters.Offset/Limit). Offset addresses the non-favorited
// segment only: a caller pages through it by incrementing Offset over the
// count of non-favorited items each page actually returned (favorited items
// don't consume any of the offset budget), and never re-requests offset 0
// mid-session — that's what makes favoriting/unfavoriting mid-scroll shift at
// most the non-favorited tail once, instead of reshuffling everything above
// the caller's current position.
func (c *Client) GetRecipeDocumentsBoosted(ctx context.Context, userID string, parameters models.GetRecipesRequest) ([]models.Recipe, []models.Recipe, int64, error) {
	favoriteFamilyIDs, err := c.favoritedFamilyIDs(ctx, userID)
	if err != nil {
		return nil, nil, 0, err
	}

	base := buildRecipeFilterPipeline(parameters)
	isFamilyListing := isFamilyListingRequest(parameters)
	sortStages := buildRecipeSortStages(parameters)

	countMatching := func(extra bson.D) (int64, error) {
		pipeline := append(slices.Clone(base), extra, recipeCountStage(isFamilyListing))
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
		if isFamilyListing {
			pipeline = append(pipeline, bson.D{{Key: "$unset", Value: "matched"}})
		}
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

	inFavorites := bson.D{{Key: "$match", Value: bson.D{{Key: "family_id", Value: bson.D{{Key: "$in", Value: favoriteFamilyIDs}}}}}}
	notInFavorites := bson.D{{Key: "$match", Value: bson.D{{Key: "family_id", Value: bson.D{{Key: "$nin", Value: favoriteFamilyIDs}}}}}}

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
// oldRootID (across all recipes) to point at newRootID instead, with a
// filtered positional array update since recipe_ref lives inside an
// ingredients array, not a top-level scalar.
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

// GetRecipeTitles returns each id's current title, for cheaply resolving a
// reference ingredient's display title in bulk (one query regardless of how
// many recipes/ingredients reference the same or different targets). An id
// with no matching document (a dangling reference, e.g. a recipe deleted
// meanwhile, tolerated defensively) is simply absent from the
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
