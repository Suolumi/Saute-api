package models

import (
	"encoding/json"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RecipePicture is one picture attached to a recipe. AddedBy is nil for a
// picture the recipe's own author added, including every picture written
// before per-picture attribution existed - so no data migration is needed;
// a non-nil AddedBy names the contributor who added a picture to a recipe
// they don't author.
type RecipePicture struct {
	Filename string              `bson:"filename" json:"filename"`
	AddedBy  *primitive.ObjectID `bson:"added_by,omitempty" json:"added_by,omitempty"`
}

// UnmarshalBSONValue accepts either a legacy bare-filename array entry
// (every recipe document written before this field existed) or the current
// {filename, added_by} document, so existing data reads correctly with no
// migration - a legacy entry decodes as an author-owned (AddedBy nil)
// picture, same as any other author-added one. Writing a recipe back always
// produces the current document shape (the default struct encoding), so a
// document upgrades to the new shape the next time it's saved.
func (p *RecipePicture) UnmarshalBSONValue(t bsontype.Type, data []byte) error {
	switch t {
	case bsontype.String:
		raw := bson.RawValue{Type: t, Value: data}
		p.Filename = raw.StringValue()
		p.AddedBy = nil
		return nil
	case bsontype.EmbeddedDocument:
		var doc struct {
			Filename string              `bson:"filename"`
			AddedBy  *primitive.ObjectID `bson:"added_by,omitempty"`
		}
		if err := bson.Unmarshal(data, &doc); err != nil {
			return err
		}
		p.Filename = doc.Filename
		p.AddedBy = doc.AddedBy
		return nil
	default:
		return fmt.Errorf("recipe picture: unsupported bson type %s", t)
	}
}

type GetRecipesRequest struct {
	Limit           int        `query:"limit,omitempty"`
	Offset          int        `query:"offset,omitempty"`
	Author          string     `query:"author,omitempty"`
	Title           string     `query:"title,omitempty"`
	PreparationTime int        `query:"preparation_time,omitempty"`
	TotalTime       int        `query:"total_time"`
	Ingredients     []string   `query:"ingredients,omitempty"`
	Kind            RecipeKind `query:"kind,omitempty"`
	// Category, when "diy", lists only DIY recipes on the default/family-
	// collapsed listing; when empty or "food", that listing excludes diy
	// (see buildRecipeFilterPipeline - this is not a generic equality
	// filter, existing recipes predate this field and must still show up
	// under "food"). Ignored by variation_of listings (a variation always
	// shares its root's category, so filtering is moot there). For
	// own_recipes listings, only an explicit "diy" narrows the results (to
	// just DIY - the admin back-office's DIY moderation tab); left empty
	// (Settings' own-recipes fetch, and the admin Recipes tab) it stays
	// category-agnostic, same as before.
	Category     RecipeCategory `query:"category,omitempty"`
	Locale       string         `query:"locale,omitempty"`
	SearchLocale string         `query:"search_locale,omitempty"`
	Favorite     bool           `query:"favorite,omitempty"`
	// VariationOf, when set, lists only the variations of that recipe id
	// (never the root itself) instead of the default root-only listing.
	VariationOf string `query:"variation_of,omitempty"`
	// OwnRecipes switches List into "My Recipes" mode: every recipe matching
	// Author is returned flatly (roots and variations alike, no collapsing),
	// and favorite decoration is per-recipe instead of family-aggregate. Set
	// only by the Settings page's own-recipes fetch.
	OwnRecipes bool `query:"own_recipes,omitempty"`
	// FavoritesOnly switches List into "My Favorites" mode: only recipes the
	// caller has exactly favorited are returned - flat, not family-collapsed
	// (the exact variation favorited is what's listed, not its root), sorted
	// alphabetically by default rather than newest-first. Unlike Favorite's
	// boost-to-front behavior, this excludes everything else instead of just
	// reordering. Unlike OwnRecipes' category-agnostic default, Category here
	// always narrows (food excludes diy, same as the default discovery
	// listing) since My Favorites is split into separate Recipes/DIY
	// sub-tabs. Set only by the Settings page's My Favorites fetch.
	FavoritesOnly bool `query:"favorites_only,omitempty"`
	// FavoritedByUserID is never bound from the query string (BindQuery skips
	// fields whose tag has no name, and this one's is deliberately "-") - it's
	// set server-side by Service.List to the caller's own user id when
	// FavoritesOnly is set, and drives the favorites $lookup in
	// buildRecipeFilterPipeline.
	FavoritedByUserID string `query:"-"`
	// ExcludeFamily hides that root id from a listing - used by the "add
	// recipe as ingredient" picker so a recipe can't offer itself or its own
	// family as a reference target. UX nicety only; the authoritative
	// enforcement is recipe_service.validateIngredientRefs.
	ExcludeFamily string `query:"exclude_family,omitempty"`
}

type GetRecipesResponse struct {
	Length int64           `json:"length"`
	Items  []RecipePreview `json:"items"`
}

// LinkVariationRequest is PATCH /recipes/:id/variation-of's body: the id of
// the (root) recipe to link the path recipe as a variation of. See
// recipe_service.LinkVariation.
type LinkVariationRequest struct {
	VariationOf string `json:"variation_of"`
}

type UpdateRecipeRequest struct {
	Title           *string         `bson:"title,omitempty" json:"title,omitempty"`
	Description     *string         `bson:"description,omitempty" json:"description,omitempty"`
	Quantity        *int            `bson:"quantity,omitempty" json:"quantity,omitempty"`
	Kind            *RecipeKind     `bson:"kind,omitempty" json:"kind,omitempty"`
	Category        *RecipeCategory `bson:"category,omitempty" json:"category,omitempty"`
	PreparationTime *int            `bson:"preparation_time,omitempty" json:"preparation_time,omitempty"`
	CookingTime     *int            `bson:"cooking_time,omitempty" json:"cooking_time,omitempty"`
	RestingTime     *int            `bson:"resting_time,omitempty" json:"resting_time,omitempty"`
	Ingredients     *[]Ingredient   `bson:"ingredients,omitempty" json:"ingredients,omitempty"`
	Steps           *[]Step         `bson:"steps,omitempty" json:"steps,omitempty"`
	Locale          *string         `bson:"source_locale,omitempty" json:"locale,omitempty"`
	KeepPictureIDs  *[]string       `bson:"-" json:"keep_picture_ids,omitempty"`
}

type RecipeKind string

var RecipeKinds = []RecipeKind{
	Breakfast,
	Starter,
	Dish,
	SideDish,
	Sauce,
	Baking,
	Snack,
	Plate,
	Dessert,
	Drink,
}

const Breakfast = RecipeKind("breakfast")
const Starter = RecipeKind("starter")
const Dish = RecipeKind("dish")
const SideDish = RecipeKind("side-dish")
const Sauce = RecipeKind("sauce")
const Baking = RecipeKind("baking")
const Snack = RecipeKind("snack")
const Plate = RecipeKind("plate")
const Dessert = RecipeKind("dessert")
const Drink = RecipeKind("drink")

type RecipeCategory string

var RecipeCategories = []RecipeCategory{Food, Diy}

const Food = RecipeCategory("food")
const Diy = RecipeCategory("diy")

type CreateRecipe struct {
	Title           string          `bson:"title,omitempty" json:"title"`
	Description     string          `bson:"description,omitempty" json:"description"`
	Quantity        int             `bson:"quantity,omitempty" json:"quantity"`
	Kind            RecipeKind      `bson:"kind,omitempty" json:"kind"`
	Category        RecipeCategory  `bson:"category,omitempty" json:"category"`
	PreparationTime int             `bson:"preparation_time,omitempty" json:"preparation_time"`
	CookingTime     int             `bson:"cooking_time,omitempty" json:"cooking_time"`
	RestingTime     int             `bson:"resting_time,omitempty" json:"resting_time"`
	Ingredients     []Ingredient    `bson:"ingredients,omitempty" json:"ingredients"`
	Steps           []Step          `bson:"steps,omitempty" json:"steps"`
	Pictures        []RecipePicture `bson:"pictures,omitempty" json:"-"`
	SourceLocale    string          `bson:"source_locale,omitempty" json:"locale,omitempty"`
	SourceHash      string          `bson:"source_hash,omitempty" json:"-"`
	// VariationOf, on the way in, is the id of the recipe being forked; the
	// service resolves it to that recipe's root (flattening a
	// variation-of-a-variation) before it's ever persisted. Must stay the
	// same *primitive.ObjectID type as RecipeDB.VariationOf: Create() passes
	// values through utils.DupStruct in both directions between these two
	// structs (service.go, mongo/recipes.go), which copies same-named fields
	// with a raw reflect.Value.Set that panics on a type mismatch. A plain
	// hex string unmarshals into this directly, same as any other id field.
	VariationOf *primitive.ObjectID `bson:"variation_of,omitempty" json:"variation_of,omitempty"`
}

type RecipeDB struct {
	Author          *primitive.ObjectID `bson:"author,omitempty" json:"author"`
	Id              *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title           string              `bson:"title,omitempty" json:"title"`
	Description     string              `bson:"description,omitempty" json:"description"`
	Quantity        int                 `bson:"quantity,omitempty" json:"quantity"`
	Kind            RecipeKind          `bson:"kind,omitempty" json:"kind"`
	Category        RecipeCategory      `bson:"category,omitempty" json:"category"`
	PreparationTime int                 `bson:"preparation_time,omitempty" json:"preparation_time"`
	CookingTime     int                 `bson:"cooking_time,omitempty" json:"cooking_time"`
	RestingTime     int                 `bson:"resting_time,omitempty" json:"resting_time"`
	Ingredients     []Ingredient        `bson:"ingredients,omitempty" json:"ingredients"`
	Steps           []Step              `bson:"steps,omitempty" json:"steps"`
	Pictures        []RecipePicture     `bson:"pictures,omitempty" json:"pictures"`
	SourceLocale    string              `bson:"source_locale,omitempty" json:"source_locale,omitempty"`
	Locale          string              `bson:"locale,omitempty" json:"locale,omitempty"`
	SourceHash      string              `bson:"source_hash,omitempty" json:"-"`
	// VariationOf is nil for a root/original recipe, or the root recipe's id
	// for a variation - always flattened, never chained.
	VariationOf *primitive.ObjectID `bson:"variation_of,omitempty" json:"variation_of,omitempty"`
}

// Recipe has bson fields to unfold the author when getting the document
type Recipe struct {
	Author          *UserView           `bson:"author,omitempty" json:"author"`
	Id              *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title           string              `bson:"title,omitempty" json:"title"`
	Description     string              `bson:"description,omitempty" json:"description"`
	Quantity        int                 `bson:"quantity,omitempty" json:"quantity"`
	Kind            RecipeKind          `bson:"kind,omitempty" json:"kind"`
	Category        RecipeCategory      `bson:"category,omitempty" json:"category"`
	PreparationTime int                 `bson:"preparation_time,omitempty" json:"preparation_time"`
	CookingTime     int                 `bson:"cooking_time,omitempty" json:"cooking_time"`
	RestingTime     int                 `bson:"resting_time,omitempty" json:"resting_time"`
	Ingredients     []Ingredient        `bson:"ingredients,omitempty" json:"ingredients"`
	Steps           []Step              `bson:"steps,omitempty" json:"steps"`
	// Pictures is the raw, unresolved picture list (as stored/written); it's
	// not serialized directly (json:"-") - AddedBy is a bare user id, not
	// display data. PictureOutputs, populated post-fetch, is what clients
	// actually receive under the "pictures" key.
	Pictures     []RecipePicture     `bson:"pictures,omitempty" json:"-"`
	SourceLocale string              `bson:"source_locale,omitempty" json:"source_locale,omitempty"`
	Locale       string              `bson:"locale,omitempty" json:"locale,omitempty"`
	SourceHash   string              `bson:"source_hash,omitempty" json:"-"`
	VariationOf  *primitive.ObjectID `bson:"variation_of,omitempty" json:"variation_of,omitempty"`
	// Favorite, FavoriteCount, VariationCount and PictureOutputs are stamped
	// on after fetch (see recipe_service.decorateFamilyFavorite/
	// decorateVariationCount/decoratePictureContributors); they never come
	// from the recipe or translation document itself, hence bson:"-".
	Favorite       bool            `bson:"-" json:"favorite"`
	FavoriteCount  int64           `bson:"-" json:"favorite_count"`
	VariationCount int64           `bson:"-" json:"variation_count"`
	PictureOutputs []PictureOutput `bson:"-" json:"pictures"`
}

// PictureOutput is one picture as served to clients: a filename plus, for a
// contributor's picture, who added it (nil/absent means the recipe's own
// author). AddedBy is only resolved to a full UserView when the fetch path
// decorates it (recipe_service.decoratePictureContributors, currently just
// Service.Get, the recipe detail fetch) - other paths leave it nil even for
// a contributor picture, since nothing renders attribution there.
type PictureOutput struct {
	Filename string    `json:"filename"`
	AddedBy  *UserView `json:"added_by,omitempty"`
}

// MarshalJSON ensures a recipe with no pictures serializes `pictures` as `[]`
// rather than `null`: a nil slice is a valid, common state (see docs/mcp.md,
// "Zero pictures is valid"), but clients that assume an array (e.g. the
// website's `formData.pictures.length` check) crash on `null`.
func (r Recipe) MarshalJSON() ([]byte, error) {
	type alias Recipe
	a := alias(r)
	if a.PictureOutputs == nil {
		a.PictureOutputs = []PictureOutput{}
	}
	return json.Marshal(a)
}

func (r *Recipe) ToRecipeDB() RecipeDB {
	return RecipeDB{
		Author:          r.Author.Id,
		Id:              r.Id,
		Title:           r.Title,
		Description:     r.Description,
		Quantity:        r.Quantity,
		Kind:            r.Kind,
		Category:        r.Category,
		PreparationTime: r.PreparationTime,
		CookingTime:     r.CookingTime,
		RestingTime:     r.RestingTime,
		Ingredients:     r.Ingredients,
		Steps:           r.Steps,
		Pictures:        r.Pictures,
		SourceLocale:    r.SourceLocale,
		Locale:          r.Locale,
		SourceHash:      r.SourceHash,
		VariationOf:     r.VariationOf,
	}
}

type RecipePreview struct {
	Id              *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title           string              `bson:"title,omitempty" json:"title"`
	Description     string              `bson:"description,omitempty" json:"description"`
	Author          *UserView           `bson:"author,omitempty" json:"author"`
	PreparationTime int                 `bson:"preparation_time,omitempty" json:"preparation_time"`
	CookingTime     int                 `bson:"cooking_time,omitempty" json:"cooking_time"`
	RestingTime     int                 `bson:"resting_time,omitempty" json:"resting_time"`
	Kind            RecipeKind          `bson:"kind,omitempty" json:"kind"`
	Category        RecipeCategory      `bson:"category,omitempty" json:"category"`
	Quantity        int                 `bson:"quantity,omitempty" json:"quantity"`
	Pictures        []string            `bson:"pictures,omitempty" json:"pictures"`
	SourceLocale    string              `bson:"source_locale,omitempty" json:"source_locale,omitempty"`
	Locale          string              `bson:"locale,omitempty" json:"locale,omitempty"`
	VariationOf     *primitive.ObjectID `bson:"variation_of,omitempty" json:"variation_of,omitempty"`
	Favorite        bool                `bson:"-" json:"favorite"`
	FavoriteCount   int64               `bson:"-" json:"favorite_count"`
	VariationCount  int64               `bson:"-" json:"variation_count"`
}

// MarshalJSON ensures `pictures` serializes as `[]` rather than `null`; see
// Recipe.MarshalJSON for why.
func (r RecipePreview) MarshalJSON() ([]byte, error) {
	type alias RecipePreview
	a := alias(r)
	if a.Pictures == nil {
		a.Pictures = []string{}
	}
	return json.Marshal(a)
}

type Ingredient struct {
	Name     string  `bson:"name,omitempty" json:"name" jsonschema:"Ingredient name, e.g. 'Egg' or 'Thyme'"`
	Quantity float64 `bson:"quantity,omitempty" json:"quantity" jsonschema:"Numeric amount, e.g. 3 or 0.5"`
	Unit     string  `bson:"unit,omitempty" json:"unit" jsonschema:"Optional unit shown between quantity and name. Leave empty for a bare count, e.g. quantity 3 + name 'Egg' renders as '3 Egg'. Set it for a unit of measure or descriptor, e.g. quantity 3 + unit 'leaves' + name 'Thyme' renders as '3 leaves - Thyme'"`
	Label    string  `bson:"label,omitempty" json:"label" jsonschema:"Optional section heading grouping this ingredient with others that share the exact same label, e.g. 'For the dough' or 'For the filling'. Leave empty for ingredients that don't belong to a named section. Reuse the identical label text (same wording and case) on every ingredient meant to share a section - matching is normalized server-side but exact reuse is still the reliable way to keep a group together."`

	// RecipeRef, when set, makes this ingredient a reference to another
	// recipe's root/family instead of free text - Quantity/Unit still mean
	// "how much of that sub-recipe", scaling with the parent's serving
	// scaler like any other ingredient. Always a root id, normalized (never
	// a specific variation) server-side on write, mirroring
	// CreateRecipe.VariationOf's one-hop flatten. Mutually exclusive with
	// Name, which is cleared server-side when this is set.
	RecipeRef *primitive.ObjectID `bson:"recipe_ref,omitempty" json:"recipe_ref,omitempty"`
	// RefLabel is an optional custom display label for a reference
	// ingredient (e.g. "Tarte Dough"); blank falls back to the referenced
	// recipe's live current title. Cleared server-side when RecipeRef is
	// nil. Translated on write through the same pipeline as Name/Unit/Label.
	RefLabel string `bson:"ref_label,omitempty" json:"ref_label,omitempty"`
	// ResolvedRefTitle is decorated post-fetch only (never persisted) -
	// RefLabel if the author set one, else the referenced recipe's live
	// current title, resolved fresh on every read (same bson:"-" pattern as
	// Recipe.VariationCount) so a renamed sub-recipe shows correctly
	// everywhere with no migration. Empty when RecipeRef is nil.
	ResolvedRefTitle string `bson:"-" json:"resolved_ref_title,omitempty"`
}

type Step struct {
	Title       string `bson:"title,omitempty" json:"title"`
	Description string `bson:"description,omitempty" json:"description"`
	Picture     string `bson:"picture,omitempty" json:"picture,omitempty" jsonschema:"Filename of an existing picture already attached to one of this recipe's steps, or empty. New pictures cannot be uploaded through MCP; attach photos via the website."`
}
