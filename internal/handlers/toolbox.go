package handlers

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"recipes/internal/jwt_manager"
	"recipes/internal/models"
)

// ListToolboxIngredients is GET /toolbox/ingredients - public, the quantity
// converter's density list.
func (h *Handlers) ListToolboxIngredients(c echo.Context) error {
	items, err := h.recipes.ListToolboxIngredients(c.Request().Context(), c.QueryParam("locale"))
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list ingredients", err, c)
	}
	return c.JSON(http.StatusOK, models.ListToolboxIngredientsResponse{Items: items})
}

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

// SubmitIngredientSuggestion is POST /toolbox/ingredient-suggestions - any
// authenticated user may propose a new ingredient or a correction to an
// existing one.
func (h *Handlers) SubmitIngredientSuggestion(c echo.Context) error {
	var body models.SubmitIngredientSuggestionRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.SubmitIngredientSuggestion(c.Request().Context(), jwt.UserId, body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, suggestion)
}

// SubmitUnitSuggestion is POST /toolbox/unit-suggestions - any authenticated
// user may propose a new unit or a correction to an existing one.
func (h *Handlers) SubmitUnitSuggestion(c echo.Context) error {
	var body models.SubmitUnitSuggestionRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.SubmitUnitSuggestion(c.Request().Context(), jwt.UserId, body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, suggestion)
}

// SubmitSubstitutionSuggestion is POST /toolbox/substitution-suggestions -
// any authenticated user may propose a new substitution or a correction to
// an existing one.
func (h *Handlers) SubmitSubstitutionSuggestion(c echo.Context) error {
	var body models.SubmitSubstitutionSuggestionRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.SubmitSubstitutionSuggestion(c.Request().Context(), jwt.UserId, body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, suggestion)
}

// AdminListToolboxSuggestions lists user-submitted toolbox additions/fixes
// for review, defaulting to pending ones.
func (h *Handlers) AdminListToolboxSuggestions(c echo.Context) error {
	status := c.QueryParam("status")
	if status == "" {
		status = models.ToolboxSuggestionPending
	}
	limit := int64(20)
	if v := c.QueryParam("limit"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil || parsed < 0 || parsed > 100 {
			return errorResponse(http.StatusBadRequest, "limit must be between 0 and 100", nil, c)
		}
		limit = parsed
	}
	var offset int64
	if v := c.QueryParam("offset"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil || parsed < 0 {
			return errorResponse(http.StatusBadRequest, "offset must not be negative", nil, c)
		}
		offset = parsed
	}
	result, err := h.recipes.ListToolboxSuggestionsForAdmin(c.Request().Context(), status, c.QueryParam("kind"), limit, offset)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list toolbox suggestions", err, c)
	}
	return c.JSON(http.StatusOK, result)
}

// AdminApproveToolboxSuggestion applies a submitted addition/correction to
// the live reference list.
func (h *Handlers) AdminApproveToolboxSuggestion(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.ApproveToolboxSuggestion(c.Request().Context(), c.Param("id"), jwt.UserId)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, suggestion)
}

// AdminRejectToolboxSuggestion dismisses a submitted addition/correction
// with no effect.
func (h *Handlers) AdminRejectToolboxSuggestion(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.RejectToolboxSuggestion(c.Request().Context(), c.Param("id"), jwt.UserId)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, suggestion)
}
