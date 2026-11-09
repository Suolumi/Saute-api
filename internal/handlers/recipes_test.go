package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestGetRecipesRejectsInvalidFamilyIDs(t *testing.T) {
	for _, query := range []string{"family=nope", "exclude_family=nope"} {
		t.Run(query, func(t *testing.T) {
			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/v1/recipes?"+query, nil), rec)

			assert.NoError(t, (&Handlers{}).GetRecipes(c))
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}
