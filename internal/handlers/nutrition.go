package handlers

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"recipes/internal/jwt_manager"
	"recipes/internal/models"
)

// ListNutritionIngredients is GET /nutrition/ingredients - public, the full
// curated Ciqual-derived reference list for client-side matching.
func (h *Handlers) ListNutritionIngredients(c echo.Context) error {
	items, err := h.recipes.ListNutritionIngredients(c.Request().Context())
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list nutrition ingredients", err, c)
	}
	return c.JSON(http.StatusOK, models.ListNutritionIngredientsResponse{Items: items})
}

// ListIngredientNutritionLinks is GET /nutrition/ingredient-links - public,
// every approved ingredient-name -> nutrition link, so the authoring UI can
// tell an already-linked ingredient from one still needing a suggestion.
func (h *Handlers) ListIngredientNutritionLinks(c echo.Context) error {
	items, err := h.recipes.ListIngredientNutritionLinks(c.Request().Context())
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list ingredient nutrition links", err, c)
	}
	return c.JSON(http.StatusOK, models.ListIngredientNutritionLinksResponse{Items: items})
}

// SubmitNutritionLinkSuggestion is POST /nutrition/ingredient-links - any
// authenticated user may link a not-yet-linked ingredient (optionally
// bundled with a unit alias), applied immediately, or propose a correction
// to an already-linked one, which becomes a pending suggestion instead - see
// Service.SubmitNutritionLinkSuggestion.
func (h *Handlers) SubmitNutritionLinkSuggestion(c echo.Context) error {
	var body models.SubmitNutritionLinkRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	result, err := h.recipes.SubmitNutritionLinkSuggestion(c.Request().Context(), jwt.UserId, body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, result)
}

// GetRecipeNutrition is GET /recipes/:id/nutrition?servings=N - public,
// computed totals for one recipe at the given serving count (defaults to
// the recipe's own base servings when omitted).
func (h *Handlers) GetRecipeNutrition(c echo.Context) error {
	servings := 0
	if v := c.QueryParam("servings"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 0 {
			return errorResponse(http.StatusBadRequest, "servings must be a non-negative integer", nil, c)
		}
		servings = parsed
	}
	nutrition, err := h.recipes.GetRecipeNutrition(c.Request().Context(), c.Param("id"), servings)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, nutrition)
}

// AdminListNutritionSuggestions lists user-submitted nutrition link
// additions/fixes for review, defaulting to pending ones.
func (h *Handlers) AdminListNutritionSuggestions(c echo.Context) error {
	status := c.QueryParam("status")
	if status == "" {
		status = models.NutritionSuggestionPending
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
	result, err := h.recipes.ListNutritionSuggestionsForAdmin(c.Request().Context(), status, limit, offset)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list nutrition suggestions", err, c)
	}
	return c.JSON(http.StatusOK, result)
}

// AdminApproveNutritionSuggestion applies a submitted link addition/
// correction to the live link tables.
func (h *Handlers) AdminApproveNutritionSuggestion(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.ApproveNutritionSuggestion(c.Request().Context(), c.Param("id"), jwt.UserId)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, suggestion)
}

// AdminRejectNutritionSuggestion dismisses a submitted link addition/
// correction with no effect.
func (h *Handlers) AdminRejectNutritionSuggestion(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.RejectNutritionSuggestion(c.Request().Context(), c.Param("id"), jwt.UserId)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, suggestion)
}

// AdminCreateNutritionLink is POST /admin/nutrition-links - a direct create,
// no suggestion/review involved.
func (h *Handlers) AdminCreateNutritionLink(c echo.Context) error {
	var body models.AdminNutritionLinkRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	link, err := h.recipes.AdminCreateNutritionLink(c.Request().Context(), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, link)
}

// AdminUpdateNutritionLink is PUT /admin/nutrition-links/:id - a direct
// overwrite, no suggestion/review involved.
func (h *Handlers) AdminUpdateNutritionLink(c echo.Context) error {
	var body models.AdminNutritionLinkRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	link, err := h.recipes.AdminUpdateNutritionLink(c.Request().Context(), c.Param("id"), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, link)
}

// AdminDeleteNutritionLink is DELETE /admin/nutrition-links/:id.
func (h *Handlers) AdminDeleteNutritionLink(c echo.Context) error {
	if err := h.recipes.AdminDeleteNutritionLink(c.Request().Context(), c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return c.NoContent(http.StatusNoContent)
}

// ListUnitAliases is GET /nutrition/unit-aliases - public, every alias, same
// posture as GET /nutrition/ingredient-links.
func (h *Handlers) ListUnitAliases(c echo.Context) error {
	items, err := h.recipes.ListUnitAliases(c.Request().Context())
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list unit aliases", err, c)
	}
	return c.JSON(http.StatusOK, models.ListUnitAliasesResponse{Items: items})
}

// AdminCreateUnitAlias is POST /admin/unit-aliases - a direct create.
func (h *Handlers) AdminCreateUnitAlias(c echo.Context) error {
	var body models.AdminUnitAliasRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	alias, err := h.recipes.AdminCreateUnitAlias(c.Request().Context(), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, alias)
}

// AdminUpdateUnitAlias is PUT /admin/unit-aliases/:id - a direct overwrite.
func (h *Handlers) AdminUpdateUnitAlias(c echo.Context) error {
	var body models.AdminUnitAliasRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	alias, err := h.recipes.AdminUpdateUnitAlias(c.Request().Context(), c.Param("id"), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, alias)
}

// AdminDeleteUnitAlias is DELETE /admin/unit-aliases/:id.
func (h *Handlers) AdminDeleteUnitAlias(c echo.Context) error {
	if err := h.recipes.AdminDeleteUnitAlias(c.Request().Context(), c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return c.NoContent(http.StatusNoContent)
}
