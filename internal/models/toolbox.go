package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	ToolboxUnitWeight = "weight"
	ToolboxUnitVolume = "volume"
)

// ToolboxUnitTranslation is one locale's machine-translated copy of a
// ToolboxUnit's translatable fields - just Name; Symbol/Kind aren't
// language-specific text.
type ToolboxUnitTranslation struct {
	Name string `bson:"name" json:"name"`
}

// ToolboxUnit is one unit the quantity converter can convert to/from, e.g.
// grams or tablespoons. ToBase is the factor to the base unit for Kind
// (grams for "weight", milliliters for "volume"), so two amounts of the same
// Kind become comparable as amount*ToBase. SourceLocale is detected from Name on
// write; Translations holds a machine translation per configured locale,
// regenerated in full on every write.
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

type ListToolboxUnitsResponse struct {
	Items []ToolboxUnit `json:"items"`
}

// AdminUnitRequest is the body of POST /admin/units and PUT /admin/units/:id.
type AdminUnitRequest struct {
	Name   string  `json:"name"`
	Symbol string  `json:"symbol"`
	Kind   string  `json:"kind"`
	ToBase float64 `json:"to_base"`
}
