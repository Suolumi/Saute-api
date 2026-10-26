package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"recipes/internal/models"
)

// ListTags is GET /tags - public, every recipe tag in display order, with
// Label resolved when ?locale= is given.
func (h *Handlers) ListTags(c echo.Context) error {
	items, err := h.recipes.ListTags(c.Request().Context(), c.QueryParam("locale"))
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, models.ListRecipeTagsResponse{Items: items})
}

// AdminCreateTag is POST /admin/tags.
func (h *Handlers) AdminCreateTag(c echo.Context) error {
	var body models.RecipeTagRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	tag, err := h.recipes.CreateTag(c.Request().Context(), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, models.SaveRecipeTagResponse{Tag: tag})
}

// AdminUpdateTag is PUT /admin/tags/:slug.
func (h *Handlers) AdminUpdateTag(c echo.Context) error {
	var body models.RecipeTagRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	tag, backfilled, err := h.recipes.UpdateTag(c.Request().Context(), c.Param("slug"), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, models.SaveRecipeTagResponse{Tag: tag, Backfilled: backfilled})
}

// AdminDeleteTag is DELETE /admin/tags/:slug.
func (h *Handlers) AdminDeleteTag(c echo.Context) error {
	if err := h.recipes.DeleteTag(c.Request().Context(), c.Param("slug")); err != nil {
		return recipeServiceError(err, c)
	}
	return c.NoContent(http.StatusNoContent)
}
