package recipe_service

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/models"
)

func validFoodRequest() models.AdminFoodRequest {
	return models.AdminFoodRequest{
		Id: "quince", Names: map[string]string{"en": " Quince ", "fr": "Coing"},
		Aliases:     map[string][]string{"en": {"quinces", " quinces", ""}},
		KcalPer100g: 57, CarbsG: 15.3,
	}
}

func adminFoodStore() *fakeStore {
	return &fakeStore{listFoodsFn: func() ([]models.FoodItem, error) {
		return []models.FoodItem{{Id: "egg", Names: map[string]string{"en": "Egg", "fr": "Œuf"}, Aliases: map[string][]string{"fr": {"oeufs"}}}}, nil
	}}
}

func TestAdminCreateFoodNormalizesAndSaves(t *testing.T) {
	store := adminFoodStore()
	var saved models.FoodItem
	store.createFoodFn = func(food models.FoodItem) error { saved = food; return nil }
	s := &Service{db: store}

	food, err := s.AdminCreateFood(context.Background(), validFoodRequest())
	if err != nil {
		t.Fatalf("AdminCreateFood: %v", err)
	}
	if saved.Id != "quince" || saved.Names["en"] != "Quince" || len(saved.Aliases["en"]) != 1 || food.KcalPer100g != 57 {
		t.Fatalf("saved %+v", saved)
	}
}

func TestAdminCreateFoodValidates(t *testing.T) {
	cases := map[string]func(*models.AdminFoodRequest){
		"bad slug":       func(r *models.AdminFoodRequest) { r.Id = "Quince!" },
		"no en name":     func(r *models.AdminFoodRequest) { r.Names["en"] = " " },
		"kcal too high":  func(r *models.AdminFoodRequest) { r.KcalPer100g = 950 },
		"macro too high": func(r *models.AdminFoodRequest) { r.FatG = 101 },
		"negative":       func(r *models.AdminFoodRequest) { r.SaltG = -1 },
		"density":        func(r *models.AdminFoodRequest) { v := 5.0; r.GPer100ml = &v },
		"piece weight":   func(r *models.AdminFoodRequest) { v := 0.0; r.GramsPerPiece = &v },
		"locale":         func(r *models.AdminFoodRequest) { r.Names["de"] = "Quitte" },
	}
	for name, mutate := range cases {
		req := validFoodRequest()
		mutate(&req)
		_, err := (&Service{db: adminFoodStore()}).AdminCreateFood(context.Background(), req)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestAdminSaveFoodRefusesTakenName(t *testing.T) {
	req := validFoodRequest()
	req.Aliases["fr"] = []string{"OEUFS"}
	_, err := (&Service{db: adminFoodStore()}).AdminCreateFood(context.Background(), req)
	if !errors.Is(err, ErrFoodExists) {
		t.Fatalf("err = %v, want ErrFoodExists", err)
	}

	// A food keeps its own names on update.
	store := adminFoodStore()
	store.updateFoodFn = func(food models.FoodItem) (models.FoodItem, error) { return food, nil }
	egg := models.AdminFoodRequest{Names: map[string]string{"en": "Egg", "fr": "Œuf"}, Aliases: map[string][]string{"fr": {"oeufs"}}}
	if _, err := (&Service{db: store}).AdminUpdateFood(context.Background(), "egg", egg); err != nil {
		t.Fatalf("AdminUpdateFood: %v", err)
	}
}

func TestAdminDeleteFoodRefusedWhileLinked(t *testing.T) {
	store := adminFoodStore()
	store.countLinksForFoodFn = func(string) (int64, error) { return 2, nil }
	if err := (&Service{db: store}).AdminDeleteFood(context.Background(), "egg"); !errors.Is(err, ErrFoodInUse) {
		t.Fatalf("err = %v, want ErrFoodInUse", err)
	}
}

func TestAdminUnitValidatesAndRefusesDeleteWhileAliased(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	bad := []models.AdminUnitRequest{
		{Name: "", Symbol: "g", Kind: "weight", ToBase: 1},
		{Name: "Gram", Symbol: "g", Kind: "length", ToBase: 1},
		{Name: "Gram", Symbol: "g", Kind: "weight", ToBase: 0},
	}
	for _, req := range bad {
		if _, err := s.AdminCreateUnit(context.Background(), req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", req, err)
		}
	}

	store := &fakeStore{countUnitAliasesForUnitFn: func(primitive.ObjectID) (int64, error) { return 1, nil }}
	if err := (&Service{db: store}).AdminDeleteUnit(context.Background(), primitive.NewObjectID().Hex()); !errors.Is(err, ErrUnitInUse) {
		t.Fatalf("err = %v, want ErrUnitInUse", err)
	}
}

func TestAdminCreateFoodCleansSubstitutes(t *testing.T) {
	store := adminFoodStore()
	var saved models.FoodItem
	store.createFoodFn = func(food models.FoodItem) error { saved = food; return nil }
	req := validFoodRequest()
	req.Substitutes = map[string][]string{"en": {" Pear ", "", "pear", "Apple"}}

	if _, err := (&Service{db: store}).AdminCreateFood(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := saved.Substitutes["en"]; len(got) != 2 || got[0] != "Pear" || got[1] != "Apple" {
		t.Fatalf("substitutes = %q", got)
	}
}
