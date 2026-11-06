package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"recipes/internal/models"
)

// ListToolboxUnits is GET /toolbox/units - public, the quantity converter's
// unit list.
func (h *Handlers) ListToolboxUnits(c echo.Context) error {
	items, err := h.recipes.ListToolboxUnits(c.Request().Context(), c.QueryParam("locale"))
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list units", err, c)
	}
	return c.JSON(http.StatusOK, models.ListToolboxUnitsResponse{Items: items})
}

// ListToolboxSubstitutions is GET /toolbox/substitutions - public, the
// substitutions list.
func (h *Handlers) ListToolboxSubstitutions(c echo.Context) error {
	items, err := h.recipes.ListToolboxSubstitutions(c.Request().Context(), c.QueryParam("locale"))
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list substitutions", err, c)
	}
	return c.JSON(http.StatusOK, models.ListToolboxSubstitutionsResponse{Items: items})
}
