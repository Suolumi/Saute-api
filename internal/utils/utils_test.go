package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestBindQuerySlice(t *testing.T) {
	type request struct {
		Ingredients []string `query:"ingredients,omitempty"`
		Title       string   `query:"title,omitempty"`
	}

	req := httptest.NewRequest(http.MethodGet, "/?ingredients=garlic&ingredients=basil&title=curry", nil)
	e := echo.New()
	c := e.NewContext(req, httptest.NewRecorder())

	var body request
	if err := BindQuery(c, &body); err != nil {
		t.Fatalf("BindQuery returned error: %v", err)
	}

	if body.Title != "curry" {
		t.Errorf("Title = %q, want %q", body.Title, "curry")
	}
	want := []string{"garlic", "basil"}
	if len(body.Ingredients) != len(want) {
		t.Fatalf("Ingredients = %v, want %v", body.Ingredients, want)
	}
	for i, v := range want {
		if body.Ingredients[i] != v {
			t.Errorf("Ingredients[%d] = %q, want %q", i, body.Ingredients[i], v)
		}
	}
}

func TestBindQuerySliceAbsent(t *testing.T) {
	type request struct {
		Ingredients []string `query:"ingredients,omitempty"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	e := echo.New()
	c := e.NewContext(req, httptest.NewRecorder())

	var body request
	if err := BindQuery(c, &body); err != nil {
		t.Fatalf("BindQuery returned error: %v", err)
	}
	if body.Ingredients != nil {
		t.Errorf("Ingredients = %v, want nil", body.Ingredients)
	}
}

func TestIsPasswordValid(t *testing.T) {
	cases := map[string]bool{
		"abcdefghi1!":  false, // 11 characters
		"abcdefghij1!": true,
		"abcdefghijk!": false, // no number
		"abcdefghijk1": false, // no special character
		"ééééééééé1!":  false, // 11 characters, more bytes
	}
	for password, want := range cases {
		if got := IsPasswordValid(password); got != want {
			t.Errorf("IsPasswordValid(%q) = %v, want %v", password, got, want)
		}
	}
}
