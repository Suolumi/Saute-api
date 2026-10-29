package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TagGroup is one of the fixed groups a RecipeTag belongs to. The order of
// TagGroups is the display order.
type TagGroup string

const (
	TagGroupDiet      = TagGroup("diet")
	TagGroupPractical = TagGroup("practical")
)

var TagGroups = []TagGroup{TagGroupDiet, TagGroupPractical}

// RecipeTag is one admin-managed tag recipes can carry, e.g. "vegan". Slug is
// immutable and is what recipes store. Categories lists the recipe categories
// the tag applies to. Implies lists tags a recipe carrying this one always
// carries too (vegan implies vegetarian); recipes store the expanded set.
// Labels holds one display label per locale; locales left blank are filled by
// machine translation of the SourceLocale label. Icon is an optional Lucide
// icon name the site shows next to the label.
type RecipeTag struct {
	Slug         string            `bson:"_id" json:"slug"`
	Group        TagGroup          `bson:"group" json:"group"`
	Categories   []RecipeCategory  `bson:"categories" json:"categories"`
	Implies      []string          `bson:"implies" json:"implies"`
	Labels       map[string]string `bson:"labels" json:"labels"`
	SourceLocale string            `bson:"source_locale" json:"source_locale"`
	Icon         string            `bson:"icon,omitempty" json:"icon,omitempty"`
	CreatedAt    time.Time         `bson:"created_at" json:"created_at"`
	// Label is Labels resolved for the request's locale, set only when one
	// was asked for.
	Label string `bson:"-" json:"label,omitempty"`
}

// RecipeTagRequest is POST /admin/tags' and PUT /admin/tags/:slug's body.
// Slug is ignored on update.
type RecipeTagRequest struct {
	Slug         string            `json:"slug"`
	Group        TagGroup          `json:"group"`
	Categories   []RecipeCategory  `json:"categories"`
	Implies      []string          `json:"implies"`
	Labels       map[string]string `json:"labels"`
	SourceLocale string            `json:"source_locale"`
	Icon         string            `json:"icon"`
}

// SaveRecipeTagResponse is the admin create/update response. Backfilled counts
// the recipes that gained a tag through a newly added implication.
type SaveRecipeTagResponse struct {
	Tag        RecipeTag `json:"tag"`
	Backfilled int64     `json:"backfilled"`
}

type ListRecipeTagsResponse struct {
	Items []RecipeTag `json:"items"`
}

// TagMatch is a variation that satisfies a tag filter its family root doesn't,
// shown as a hint on the root's listing card.
type TagMatch struct {
	Id           *primitive.ObjectID `bson:"_id" json:"id"`
	Title        string              `bson:"title" json:"title"`
	SourceLocale string              `bson:"source_locale,omitempty" json:"-"`
	SourceHash   string              `bson:"source_hash,omitempty" json:"-"`
}
