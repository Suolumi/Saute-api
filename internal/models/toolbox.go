package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	ToolboxSuggestionPending  = "pending"
	ToolboxSuggestionApproved = "approved"
	ToolboxSuggestionRejected = "rejected"
)

const (
	ToolboxSuggestionIngredient   = "ingredient"
	ToolboxSuggestionUnit         = "unit"
	ToolboxSuggestionSubstitution = "substitution"
)

const (
	ToolboxUnitWeight = "weight"
	ToolboxUnitVolume = "volume"
)

// ToolboxIngredientTranslation is one locale's machine-translated copy of a
// ToolboxIngredient's translatable fields.
type ToolboxIngredientTranslation struct {
	Name string `bson:"name" json:"name"`
	Note string `bson:"note,omitempty" json:"note,omitempty"`
}

// ToolboxIngredient is one entry in the Toolbox's quantity-converter density
// list: how many grams 100ml of it weighs, so an amount can be converted
// between a weight unit and a volume unit for that ingredient. SourceLocale
// is detected from Name/Note on write; Translations holds a machine
// translation per configured target locale (see recipe_service.toolbox.go),
// regenerated in full on every write rather than versioned/overridable like
// a recipe's - the list is small and curated, so nothing here ever needs to
// survive a background retranslation the way an approved translation
// suggestion does.
type ToolboxIngredient struct {
	Id           *primitive.ObjectID                     `bson:"_id,omitempty" json:"id"`
	Name         string                                  `bson:"name" json:"name"`
	NameLower    string                                  `bson:"name_lower" json:"-"`
	GPer100ml    float64                                 `bson:"g_per_100ml" json:"g_per_100ml"`
	Note         string                                  `bson:"note,omitempty" json:"note,omitempty"`
	CreatedAt    time.Time                               `bson:"created_at" json:"created_at"`
	SourceLocale string                                  `bson:"source_locale,omitempty" json:"-"`
	Translations map[string]ToolboxIngredientTranslation `bson:"translations,omitempty" json:"-"`
}

// ToolboxUnitTranslation is one locale's machine-translated copy of a
// ToolboxUnit's translatable fields - just Name; Symbol/Kind aren't
// language-specific text.
type ToolboxUnitTranslation struct {
	Name string `bson:"name" json:"name"`
}

// ToolboxUnit is one unit the quantity converter can convert to/from, e.g.
// grams or tablespoons. ToBase is the factor to the base unit for Kind
// (grams for "weight", milliliters for "volume"), so two amounts of the same
// Kind become comparable as amount*ToBase. SourceLocale/Translations mirror
// ToolboxIngredient's.
type ToolboxUnit struct {
	Id           *primitive.ObjectID               `bson:"_id,omitempty" json:"id"`
	Name         string                            `bson:"name" json:"name"`
	NameLower    string                            `bson:"name_lower" json:"-"`
	Symbol       string                            `bson:"symbol" json:"symbol"`
	Kind         string                            `bson:"kind" json:"kind"`
	ToBase       float64                           `bson:"to_base" json:"to_base"`
	CreatedAt    time.Time                         `bson:"created_at" json:"created_at"`
	SourceLocale string                            `bson:"source_locale,omitempty" json:"-"`
	Translations map[string]ToolboxUnitTranslation `bson:"translations,omitempty" json:"-"`
}

// ToolboxSubstitutionTranslation is one locale's machine-translated copy of
// a ToolboxSubstitution's translatable fields.
type ToolboxSubstitutionTranslation struct {
	Problem  string `bson:"problem" json:"problem"`
	Solution string `bson:"solution" json:"solution"`
	Tag      string `bson:"tag,omitempty" json:"tag,omitempty"`
}

// ToolboxSubstitution is one ingredient-swap tip: Problem is the missing
// ingredient/situation (e.g. "No buttermilk"), Solution the swap to use, Tag
// a short category label shown as a badge. SourceLocale/Translations mirror
// ToolboxIngredient's.
type ToolboxSubstitution struct {
	Id           *primitive.ObjectID                       `bson:"_id,omitempty" json:"id"`
	Problem      string                                    `bson:"problem" json:"problem"`
	Solution     string                                    `bson:"solution" json:"solution"`
	Tag          string                                    `bson:"tag,omitempty" json:"tag,omitempty"`
	CreatedAt    time.Time                                 `bson:"created_at" json:"created_at"`
	SourceLocale string                                    `bson:"source_locale,omitempty" json:"-"`
	Translations map[string]ToolboxSubstitutionTranslation `bson:"translations,omitempty" json:"-"`
}

// ToolboxSuggestion is a user-submitted addition or correction to one of the
// Toolbox's three reference lists (see docs/toolbox.md). TargetID is set
// when the submission matched an existing entry by name/problem
// case-insensitively - approval then updates that entry in place instead of
// inserting a new one, so a "duplicate" is never simply rejected. Only the
// fields for Kind are populated.
type ToolboxSuggestion struct {
	Id          *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Kind        string              `bson:"kind" json:"kind"`
	TargetID    *primitive.ObjectID `bson:"target_id,omitempty" json:"target_id,omitempty"`
	SubmittedBy primitive.ObjectID  `bson:"submitted_by" json:"submitted_by"`
	Note        string              `bson:"note,omitempty" json:"note,omitempty"`
	Status      string              `bson:"status" json:"status"`
	CreatedAt   time.Time           `bson:"created_at" json:"created_at"`
	ReviewedAt  *time.Time          `bson:"reviewed_at,omitempty" json:"reviewed_at,omitempty"`
	ReviewedBy  *primitive.ObjectID `bson:"reviewed_by,omitempty" json:"reviewed_by,omitempty"`

	// Kind == ingredient
	IngredientName string  `bson:"ingredient_name,omitempty" json:"ingredient_name,omitempty"`
	GPer100ml      float64 `bson:"g_per_100ml,omitempty" json:"g_per_100ml,omitempty"`

	// Kind == unit
	UnitName   string  `bson:"unit_name,omitempty" json:"unit_name,omitempty"`
	UnitSymbol string  `bson:"unit_symbol,omitempty" json:"unit_symbol,omitempty"`
	UnitKind   string  `bson:"unit_kind,omitempty" json:"unit_kind,omitempty"`
	UnitToBase float64 `bson:"unit_to_base,omitempty" json:"unit_to_base,omitempty"`

	// Kind == substitution
	SubProblem  string `bson:"sub_problem,omitempty" json:"sub_problem,omitempty"`
	SubSolution string `bson:"sub_solution,omitempty" json:"sub_solution,omitempty"`
	SubTag      string `bson:"sub_tag,omitempty" json:"sub_tag,omitempty"`
}

// SubmitIngredientSuggestionRequest is POST
// /toolbox/ingredient-suggestions' body.
type SubmitIngredientSuggestionRequest struct {
	Name      string  `json:"name"`
	GPer100ml float64 `json:"g_per_100ml"`
	Note      string  `json:"note"`
}

// SubmitUnitSuggestionRequest is POST /toolbox/unit-suggestions' body.
type SubmitUnitSuggestionRequest struct {
	Name   string  `json:"name"`
	Symbol string  `json:"symbol"`
	Kind   string  `json:"kind"`
	ToBase float64 `json:"to_base"`
	Note   string  `json:"note"`
}

// SubmitSubstitutionSuggestionRequest is POST
// /toolbox/substitution-suggestions' body.
type SubmitSubstitutionSuggestionRequest struct {
	Problem  string `json:"problem"`
	Solution string `json:"solution"`
	Tag      string `json:"tag"`
	Note     string `json:"note"`
}

// ToolboxSuggestionView is one row in the admin review list: the
// suggestion's own content plus a resolved submitter username and, for a
// correction, the entry it would replace.
type ToolboxSuggestionView struct {
	Id                  *primitive.ObjectID `json:"id"`
	Kind                string              `json:"kind"`
	TargetID            *primitive.ObjectID `json:"target_id,omitempty"`
	IsCorrection        bool                `json:"is_correction"`
	SubmittedBy         primitive.ObjectID  `json:"submitted_by"`
	SubmittedByUsername string              `json:"submitted_by_username"`
	Note                string              `json:"note,omitempty"`
	Status              string              `json:"status"`
	CreatedAt           time.Time           `json:"created_at"`
	ReviewedAt          *time.Time          `json:"reviewed_at,omitempty"`

	IngredientName string  `json:"ingredient_name,omitempty"`
	GPer100ml      float64 `json:"g_per_100ml,omitempty"`
	UnitName       string  `json:"unit_name,omitempty"`
	UnitSymbol     string  `json:"unit_symbol,omitempty"`
	UnitKind       string  `json:"unit_kind,omitempty"`
	UnitToBase     float64 `json:"unit_to_base,omitempty"`
	SubProblem     string  `json:"sub_problem,omitempty"`
	SubSolution    string  `json:"sub_solution,omitempty"`
	SubTag         string  `json:"sub_tag,omitempty"`

	// CurrentName is the target entry's current display name/problem, for a
	// correction only - lets the admin see what would be overwritten.
	CurrentName string `json:"current_name,omitempty"`
}

type ListToolboxSuggestionsResponse struct {
	Length int64                   `json:"length"`
	Items  []ToolboxSuggestionView `json:"items"`
}

type ListToolboxIngredientsResponse struct {
	Items []ToolboxIngredient `json:"items"`
}

type ListToolboxUnitsResponse struct {
	Items []ToolboxUnit `json:"items"`
}

type ListToolboxSubstitutionsResponse struct {
	Items []ToolboxSubstitution `json:"items"`
}
