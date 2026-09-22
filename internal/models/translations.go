package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	TranslationSuggestionPending  = "pending"
	TranslationSuggestionApproved = "approved"
	TranslationSuggestionRejected = "rejected"
	// TranslationSuggestionStale marks a suggestion an admin tried to approve
	// after the canonical recipe changed underneath it (SourceHash mismatch)
	// or the recipe was deleted - see recipe_service.ApproveTranslationSuggestion.
	TranslationSuggestionStale = "stale"
)

// TranslationSuggestion is a user-submitted fix to one locale's machine
// translation of a recipe - see docs/translation-suggestions.md. It always
// carries the whole translatable subset (title/description/every ingredient/
// every step), pre-filled from the current translation by the submitter, who
// edits only what needs fixing; order must line up 1:1 with the canonical
// recipe's own Ingredients/Steps at submission time.
type TranslationSuggestion struct {
	Id          *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	RecipeID    primitive.ObjectID  `bson:"recipe_id" json:"recipe_id"`
	Locale      string              `bson:"locale" json:"locale"`
	SubmittedBy primitive.ObjectID  `bson:"submitted_by" json:"submitted_by"`
	// SourceHash is the canonical recipe's SourceHash at submission time -
	// compared against the canonical's current SourceHash on approval to
	// detect staleness (the recipe changed underneath the suggestion).
	SourceHash  string              `bson:"source_hash" json:"-"`
	Title       string              `bson:"title,omitempty" json:"title"`
	Description string              `bson:"description,omitempty" json:"description"`
	Ingredients []Ingredient        `bson:"ingredients,omitempty" json:"ingredients"`
	Steps       []Step              `bson:"steps,omitempty" json:"steps"`
	Status      string              `bson:"status" json:"status"`
	CreatedAt   time.Time           `bson:"created_at" json:"created_at"`
	ReviewedAt  *time.Time          `bson:"reviewed_at,omitempty" json:"reviewed_at,omitempty"`
	ReviewedBy  *primitive.ObjectID `bson:"reviewed_by,omitempty" json:"reviewed_by,omitempty"`
}

// SubmitTranslationSuggestionRequest is POST
// /recipes/:id/translation-suggestions' body.
type SubmitTranslationSuggestionRequest struct {
	Locale      string       `json:"locale"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Ingredients []Ingredient `json:"ingredients"`
	Steps       []Step       `json:"steps"`
}

// TranslationSuggestionView is one row in the admin review list: the
// suggestion's own content plus resolved display fields and the current live
// translation it would replace, so the client can render an old-vs-suggested
// comparison without a second round trip.
type TranslationSuggestionView struct {
	Id                   *primitive.ObjectID `json:"id"`
	RecipeID             primitive.ObjectID  `json:"recipe_id"`
	RecipeTitle          string              `json:"recipe_title"`
	Locale               string              `json:"locale"`
	SubmittedBy          primitive.ObjectID  `json:"submitted_by"`
	SubmittedByUsername  string              `json:"submitted_by_username"`
	Status               string              `json:"status"`
	CreatedAt            time.Time           `json:"created_at"`
	ReviewedAt           *time.Time          `json:"reviewed_at,omitempty"`
	SuggestedTitle       string              `json:"suggested_title"`
	SuggestedDescription string              `json:"suggested_description"`
	SuggestedIngredients []Ingredient        `json:"suggested_ingredients"`
	SuggestedSteps       []Step              `json:"suggested_steps"`
	CurrentTitle         string              `json:"current_title"`
	CurrentDescription   string              `json:"current_description"`
	CurrentIngredients   []Ingredient        `json:"current_ingredients"`
	CurrentSteps         []Step              `json:"current_steps"`
}

type ListTranslationSuggestionsResponse struct {
	Length int64                       `json:"length"`
	Items  []TranslationSuggestionView `json:"items"`
}

// TranslationOverride pins one translatable field's approved text against a
// snapshot of the source-language text it was corrected against, so an
// approved suggestion survives a future retranslation triggered by an
// unrelated recipe edit - see docs/translation-suggestions.md and
// recipe_service.applyTranslationOverrides. FieldPath is "title",
// "description", "ingredients.<i>.name/unit/label/ref_label", or
// "steps.<i>.title/description".
type TranslationOverride struct {
	Id        *primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	RecipeID  primitive.ObjectID  `bson:"recipe_id" json:"recipe_id"`
	Locale    string              `bson:"locale" json:"locale"`
	FieldPath string              `bson:"field_path" json:"field_path"`
	Value     string              `bson:"value" json:"value"`
	// SourceSnapshot is the canonical recipe's source-language text for this
	// same field at approval time - reapplying this override on a future
	// retranslation is skipped once the canonical's current text no longer
	// matches, so the fix silently reverts to fresh machine translation.
	SourceSnapshot string `bson:"source_snapshot" json:"-"`
	// IngredientsLen/StepsLen record the array length this override was
	// approved against, for an indexed field only - reapplication drops every
	// override on that list in one shot once the recipe's current length no
	// longer matches (see recipe_service.applyTranslationOverrides).
	IngredientsLen *int                `bson:"ingredients_len,omitempty" json:"-"`
	StepsLen       *int                `bson:"steps_len,omitempty" json:"-"`
	SuggestionID   *primitive.ObjectID `bson:"suggestion_id,omitempty" json:"suggestion_id,omitempty"`
	ApprovedAt     time.Time           `bson:"approved_at" json:"approved_at"`
	ApprovedBy     primitive.ObjectID  `bson:"approved_by" json:"approved_by"`
}

// TranslationOverrideView is one row in the admin override-management view
// (see the generic manifest console's admin.translations.listOverrides).
type TranslationOverrideView struct {
	Id          *primitive.ObjectID `json:"id"`
	RecipeID    primitive.ObjectID  `json:"recipe_id"`
	RecipeTitle string              `json:"recipe_title"`
	Locale      string              `json:"locale"`
	FieldPath   string              `json:"field_path"`
	Value       string              `json:"value"`
	ApprovedAt  time.Time           `json:"approved_at"`
}

type ListTranslationOverridesResponse struct {
	Length int64                     `json:"length"`
	Items  []TranslationOverrideView `json:"items"`
}
