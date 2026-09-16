package mcp_server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/models"
)

func TestIngredientOutputsResolvesRecipeRef(t *testing.T) {
	ref := primitive.NewObjectID()
	ingredients := []models.Ingredient{
		{Name: "Flour", Quantity: 250, Unit: "g"},
		{Quantity: 1, Unit: "batch", RecipeRef: &ref, ResolvedRefTitle: "Tarte Dough"},
	}

	outputs := ingredientOutputs(ingredients)
	if len(outputs) != 2 {
		t.Fatalf("len(outputs) = %d, want 2", len(outputs))
	}
	if outputs[0].RecipeRef != nil {
		t.Fatalf("outputs[0].RecipeRef = %+v, want nil for a free-text ingredient", outputs[0].RecipeRef)
	}
	if outputs[1].RecipeRef == nil {
		t.Fatal("outputs[1].RecipeRef = nil, want resolved")
	}
	if outputs[1].RecipeRef.ID != ref.Hex() || outputs[1].RecipeRef.Title != "Tarte Dough" {
		t.Fatalf("outputs[1].RecipeRef = %+v, want {ID: %q, Title: %q}", outputs[1].RecipeRef, ref.Hex(), "Tarte Dough")
	}
	if outputs[1].Quantity != 1 || outputs[1].Unit != "batch" {
		t.Fatalf("outputs[1] quantity/unit = %v/%q, want 1/batch", outputs[1].Quantity, outputs[1].Unit)
	}
}

func TestIngredientsFromInputHasNoRecipeRefField(t *testing.T) {
	// IngredientInput structurally has no recipe_ref field, so this is really
	// a compile-time guarantee - this test just documents/locks the
	// conversion's behavior for a plain ingredient.
	inputs := []IngredientInput{{Name: "Sugar", Quantity: 2, Unit: "tbsp", Label: "For the glaze"}}
	got := ingredientsFromInput(inputs)
	if len(got) != 1 || got[0].RecipeRef != nil {
		t.Fatalf("ingredientsFromInput(%+v) = %+v, want one ingredient with RecipeRef nil", inputs, got)
	}
	if got[0].Name != "Sugar" || got[0].Quantity != 2 || got[0].Unit != "tbsp" || got[0].Label != "For the glaze" {
		t.Fatalf("ingredientsFromInput mapped fields incorrectly: %+v", got[0])
	}
}

func TestCursorRoundTrip(t *testing.T) {
	id := primitive.NewObjectID().Hex()
	encoded := encodeCursor(id)
	if encoded == "" || encoded == id {
		t.Fatalf("cursor was not made opaque: %q", encoded)
	}
	decoded, err := decodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != id {
		t.Fatalf("decoded cursor = %q, want %q", decoded, id)
	}
	if _, err := decodeCursor("not-a-cursor"); err == nil {
		t.Fatal("expected malformed cursor to fail")
	}
}

func TestOffsetCursorRoundTrip(t *testing.T) {
	encoded := encodeOffsetCursor(40)
	if encoded == "" || encoded == "40" {
		t.Fatalf("cursor was not made opaque: %q", encoded)
	}
	decoded, err := decodeOffsetCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != 40 {
		t.Fatalf("decoded cursor = %d, want 40", decoded)
	}
	if decoded, err := decodeOffsetCursor(""); err != nil || decoded != 0 {
		t.Fatalf("decodeOffsetCursor(\"\") = %d, %v, want 0, nil", decoded, err)
	}
	if _, err := decodeOffsetCursor("not-a-cursor"); err == nil {
		t.Fatal("expected malformed cursor to fail")
	}
	if _, err := decodeOffsetCursor(encodeOffsetCursor(-1)); err == nil {
		t.Fatal("expected a negative offset to fail")
	}
}

func TestUserWithScope(t *testing.T) {
	req := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "user-id", Scopes: []string{"recipes:read"}}}}
	userID, err := userWithScope(req, "recipes:read")
	if err != nil || userID != "user-id" {
		t.Fatalf("userWithScope() = %q, %v", userID, err)
	}
	if _, err := userWithScope(req, "recipes:write"); err == nil {
		t.Fatal("expected missing scope to fail")
	}
}

func TestProtocolOnly(t *testing.T) {
	handler := protocolOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Mcp-Protocol-Version", protocolVersion)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("current protocol status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("legacy protocol status = %d", response.Code)
	}
}
