package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	NutritionSuggestionPending  = "pending"
	NutritionSuggestionApproved = "approved"
	NutritionSuggestionRejected = "rejected"
)

// NutritionIngredient is one entry in the curated nutrition reference list,
// imported once from ANSES's Ciqual food-composition table (see
// docs/nutrition.md) - kcal plus five headline macros per 100g.
type NutritionIngredient struct {
	Id          *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name        string              `bson:"name" json:"name"`
	NameLower   string              `bson:"name_lower" json:"-"`
	Names       map[string]string   `bson:"names,omitempty" json:"names"`
	KcalPer100g float64             `bson:"kcal_per_100g" json:"kcal_per_100g"`
	ProteinG    float64             `bson:"protein_g_per_100g" json:"protein_g_per_100g"`
	CarbsG      float64             `bson:"carbs_g_per_100g" json:"carbs_g_per_100g"`
	FatG        float64             `bson:"fat_g_per_100g" json:"fat_g_per_100g"`
	SaltG       float64             `bson:"salt_g_per_100g" json:"salt_g_per_100g"`
	SugarG      float64             `bson:"sugar_g_per_100g" json:"sugar_g_per_100g"`
	CreatedAt   time.Time           `bson:"created_at" json:"created_at"`
}

// IngredientNutritionLink maps a normalized free-text recipe ingredient name
// to a NutritionIngredient, plus what's needed to get from "however a recipe
// quantifies it" to grams. See docs/nutrition.md "Resolving a quantity to
// grams".
type IngredientNutritionLink struct {
	Id           *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name         string              `bson:"name" json:"name"`
	NameLower    string              `bson:"name_lower" json:"-"`
	NutritionID  primitive.ObjectID  `bson:"nutrition_id" json:"nutrition_id"`
	GPer100ml    *float64            `bson:"g_per_100ml,omitempty" json:"g_per_100ml,omitempty"`
	GramsPerUnit *float64            `bson:"grams_per_unit,omitempty" json:"grams_per_unit,omitempty"`
	CreatedAt    time.Time           `bson:"created_at" json:"created_at"`
}

// UnitAlias maps a free-text recipe unit string ("grammes", "tasse") to one
// of the Toolbox's existing canonical units. Only consulted when a unit
// string doesn't already match a ToolboxUnit's own Name/Symbol directly.
type UnitAlias struct {
	Id         *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Alias      string              `bson:"alias" json:"alias"`
	AliasLower string              `bson:"alias_lower" json:"-"`
	UnitID     primitive.ObjectID  `bson:"unit_id" json:"unit_id"`
	CreatedAt  time.Time           `bson:"created_at" json:"created_at"`
}

// NutritionSuggestion is a user-submitted addition or correction to an
// IngredientNutritionLink, optionally bundled with a UnitAlias suggestion
// for the same ingredient's unit text - the inline authoring UI resolves
// both a name and a unit at once, so a single suggestion can touch both
// lookup tables. TargetID/UnitTargetID are set when the submission matched
// an existing entry case-insensitively, same "duplicate becomes a
// correction" treatment as Toolbox suggestions.
type NutritionSuggestion struct {
	Id          *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	TargetID    *primitive.ObjectID `bson:"target_id,omitempty" json:"target_id,omitempty"`
	SubmittedBy primitive.ObjectID  `bson:"submitted_by" json:"submitted_by"`
	Note        string              `bson:"note,omitempty" json:"note,omitempty"`
	Status      string              `bson:"status" json:"status"`
	CreatedAt   time.Time           `bson:"created_at" json:"created_at"`
	ReviewedAt  *time.Time          `bson:"reviewed_at,omitempty" json:"reviewed_at,omitempty"`
	ReviewedBy  *primitive.ObjectID `bson:"reviewed_by,omitempty" json:"reviewed_by,omitempty"`

	IngredientName string             `bson:"ingredient_name" json:"ingredient_name"`
	NutritionID    primitive.ObjectID `bson:"nutrition_id" json:"nutrition_id"`
	GPer100ml      *float64           `bson:"g_per_100ml,omitempty" json:"g_per_100ml,omitempty"`
	GramsPerUnit   *float64           `bson:"grams_per_unit,omitempty" json:"grams_per_unit,omitempty"`

	// Optional accompanying unit-alias suggestion.
	UnitAlias    string              `bson:"unit_alias,omitempty" json:"unit_alias,omitempty"`
	UnitID       *primitive.ObjectID `bson:"unit_id,omitempty" json:"unit_id,omitempty"`
	UnitTargetID *primitive.ObjectID `bson:"unit_target_id,omitempty" json:"unit_target_id,omitempty"`
}

// SubmitNutritionLinkRequest is POST /nutrition/ingredient-links' body.
// IngredientUnit is the current recipe ingredient's own unit text (may be
// blank for a bare count) - used only to decide whether GPer100ml or
// GramsPerUnit is required to make this link resolvable (see
// Service.validateIngredientUnitRequirement); it's never itself persisted.
type SubmitNutritionLinkRequest struct {
	IngredientName string   `json:"ingredient_name"`
	IngredientUnit string   `json:"ingredient_unit,omitempty"`
	NutritionID    string   `json:"nutrition_id"`
	GPer100ml      *float64 `json:"g_per_100ml,omitempty"`
	GramsPerUnit   *float64 `json:"grams_per_unit,omitempty"`
	UnitAlias      string   `json:"unit_alias,omitempty"`
	UnitID         string   `json:"unit_id,omitempty"`
	Note           string   `json:"note,omitempty"`
}

// SubmitNutritionLinkResponse is POST /nutrition/ingredient-links' response.
// Applied is true when the ingredient (and unit alias, if any) had no
// existing link/alias, so the submission went live immediately (Link is
// populated); false means it (or its bundled unit alias) already existed,
// so the whole submission became a pending suggestion instead (Suggestion
// is populated) - see docs/nutrition.md "Suggestions".
type SubmitNutritionLinkResponse struct {
	Applied    bool                     `json:"applied"`
	Link       *IngredientNutritionLink `json:"link,omitempty"`
	Suggestion *NutritionSuggestion     `json:"suggestion,omitempty"`
}

// AdminNutritionLinkRequest is the admin back-office's direct create/update
// body for an ingredient nutrition link - the same shape as
// SubmitNutritionLinkRequest minus the suggestion-only Note/UnitAlias/UnitID
// fields, since an admin edit is never a suggestion.
type AdminNutritionLinkRequest struct {
	IngredientName string   `json:"ingredient_name"`
	NutritionID    string   `json:"nutrition_id"`
	GPer100ml      *float64 `json:"g_per_100ml,omitempty"`
	GramsPerUnit   *float64 `json:"grams_per_unit,omitempty"`
}

// AdminUnitAliasRequest is the admin back-office's direct create/update body
// for a unit alias.
type AdminUnitAliasRequest struct {
	Alias  string `json:"alias"`
	UnitID string `json:"unit_id"`
}

type ListUnitAliasesResponse struct {
	Items []UnitAlias `json:"items"`
}

// NutritionSuggestionView is one row in the admin review list.
type NutritionSuggestionView struct {
	Id                  *primitive.ObjectID `json:"id"`
	TargetID            *primitive.ObjectID `json:"target_id,omitempty"`
	IsCorrection        bool                `json:"is_correction"`
	SubmittedBy         primitive.ObjectID  `json:"submitted_by"`
	SubmittedByUsername string              `json:"submitted_by_username"`
	Note                string              `json:"note,omitempty"`
	Status              string              `json:"status"`
	CreatedAt           time.Time           `json:"created_at"`
	ReviewedAt          *time.Time          `json:"reviewed_at,omitempty"`

	IngredientName   string             `json:"ingredient_name"`
	NutritionID      primitive.ObjectID `json:"nutrition_id"`
	NutritionName    string             `json:"nutrition_name,omitempty"`
	GPer100ml        *float64           `json:"g_per_100ml,omitempty"`
	GramsPerUnit     *float64           `json:"grams_per_unit,omitempty"`
	UnitAlias        string             `json:"unit_alias,omitempty"`
	UnitName         string             `json:"unit_name,omitempty"`
	IsUnitCorrection bool               `json:"is_unit_correction"`

	// CurrentName is the target link's current ingredient name, for a
	// correction only.
	CurrentName string `json:"current_name,omitempty"`
}

type ListNutritionSuggestionsResponse struct {
	Length int64                     `json:"length"`
	Items  []NutritionSuggestionView `json:"items"`
}

type ListNutritionIngredientsResponse struct {
	Items []NutritionIngredient `json:"items"`
}

type ListIngredientNutritionLinksResponse struct {
	Items []IngredientNutritionLink `json:"items"`
}

// IngredientNutrition is one recipe ingredient's own contribution to its
// recipe's total, at the same index as the recipe's (canonical or
// localized - translation preserves positional correspondence) Ingredients
// array. Matched is false when this line didn't contribute (no link, no
// quantity, no density, or - for a recipe_ref line - the referenced
// recipe itself had nothing matched); its Kcal/etc. are 0 in that case, not
// omitted, so a client can zip this array against the ingredient list by
// index without a presence check.
type IngredientNutrition struct {
	Matched  bool    `json:"matched"`
	Kcal     float64 `json:"kcal"`
	ProteinG float64 `json:"protein_g"`
	CarbsG   float64 `json:"carbs_g"`
	FatG     float64 `json:"fat_g"`
	SaltG    float64 `json:"salt_g"`
	SugarG   float64 `json:"sugar_g"`
}

// RecipeNutrition is GET /recipes/:id/nutrition's response - best-effort
// totals for the requested serving count, plus how much of the recipe's
// ingredient list actually contributed. Ingredients is the same totals
// broken out per ingredient line (see IngredientNutrition) - Kcal/etc.
// above are exactly the sum of Ingredients' own fields, not computed
// separately.
type RecipeNutrition struct {
	Servings     int                   `json:"servings"`
	Kcal         float64               `json:"kcal"`
	ProteinG     float64               `json:"protein_g"`
	CarbsG       float64               `json:"carbs_g"`
	FatG         float64               `json:"fat_g"`
	SaltG        float64               `json:"salt_g"`
	SugarG       float64               `json:"sugar_g"`
	MatchedCount int                   `json:"matched_count"`
	TotalCount   int                   `json:"total_count"`
	Ingredients  []IngredientNutrition `json:"ingredients"`
}
