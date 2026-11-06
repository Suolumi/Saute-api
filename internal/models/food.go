package models

import "time"

// FoodItem is one generic food of the curated list: its name and the aliases a
// recipe may use for it, per locale, nutrition per 100 g, the defaults
// that turn a count or a volume into grams, and its substitutes. Id is an immutable slug.
type FoodItem struct {
	Id            string              `bson:"_id" json:"id"`
	Names         map[string]string   `bson:"names" json:"names"`
	Aliases       map[string][]string `bson:"aliases" json:"aliases"`
	GramsPerPiece *float64            `bson:"grams_per_piece,omitempty" json:"grams_per_piece,omitempty"`
	GPer100ml     *float64            `bson:"g_per_100ml,omitempty" json:"g_per_100ml,omitempty"`
	// Substitutes are what to use instead, one line each, per locale.
	Substitutes map[string][]string `bson:"substitutes,omitempty" json:"substitutes,omitempty"`
	KcalPer100g float64             `bson:"kcal_per_100g" json:"kcal_per_100g"`
	ProteinG    float64             `bson:"protein_g_per_100g" json:"protein_g_per_100g"`
	CarbsG      float64             `bson:"carbs_g_per_100g" json:"carbs_g_per_100g"`
	FatG        float64             `bson:"fat_g_per_100g" json:"fat_g_per_100g"`
	SaltG       float64             `bson:"salt_g_per_100g" json:"salt_g_per_100g"`
	SugarG      float64             `bson:"sugar_g_per_100g" json:"sugar_g_per_100g"`
	CreatedAt   time.Time           `bson:"created_at" json:"created_at"`
}

// FoodLocales are the locales every food has a name in, in the order a
// recipe with no usable source locale tries them.
var FoodLocales = []string{"en", "fr", "fi", "zh"}

type ListFoodsResponse struct {
	Items []FoodItem `json:"items"`
}

// AdminFoodRequest is the body of POST /admin/foods and PUT
// /admin/foods/:id; Id is read on create only.
type AdminFoodRequest struct {
	Id            string              `json:"id"`
	Names         map[string]string   `json:"names"`
	Aliases       map[string][]string `json:"aliases"`
	GramsPerPiece *float64            `json:"grams_per_piece"`
	GPer100ml     *float64            `json:"g_per_100ml"`
	Substitutes   map[string][]string `json:"substitutes"`
	KcalPer100g   float64             `json:"kcal_per_100g"`
	ProteinG      float64             `json:"protein_g_per_100g"`
	CarbsG        float64             `json:"carbs_g_per_100g"`
	FatG          float64             `json:"fat_g_per_100g"`
	SaltG         float64             `json:"salt_g_per_100g"`
	SugarG        float64             `json:"sugar_g_per_100g"`
}
