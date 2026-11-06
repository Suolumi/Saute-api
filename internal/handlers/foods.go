package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"recipes/internal/models"
)

// AdminCreateFood is POST /admin/foods.
func (h *Handlers) AdminCreateFood(c echo.Context) error {
	var body models.AdminFoodRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	food, err := h.recipes.AdminCreateFood(c.Request().Context(), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, food)
}

// AdminUpdateFood is PUT /admin/foods/:id.
func (h *Handlers) AdminUpdateFood(c echo.Context) error {
	var body models.AdminFoodRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	food, err := h.recipes.AdminUpdateFood(c.Request().Context(), c.Param("id"), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, food)
}

// AdminDeleteFood is DELETE /admin/foods/:id.
func (h *Handlers) AdminDeleteFood(c echo.Context) error {
	if err := h.recipes.AdminDeleteFood(c.Request().Context(), c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return c.NoContent(http.StatusNoContent)
}

// AdminCreateUnit is POST /admin/units.
func (h *Handlers) AdminCreateUnit(c echo.Context) error {
	var body models.AdminUnitRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	unit, err := h.recipes.AdminCreateUnit(c.Request().Context(), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, unit)
}

// AdminUpdateUnit is PUT /admin/units/:id.
func (h *Handlers) AdminUpdateUnit(c echo.Context) error {
	var body models.AdminUnitRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	unit, err := h.recipes.AdminUpdateUnit(c.Request().Context(), c.Param("id"), body)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, unit)
}

// AdminDeleteUnit is DELETE /admin/units/:id.
func (h *Handlers) AdminDeleteUnit(c echo.Context) error {
	if err := h.recipes.AdminDeleteUnit(c.Request().Context(), c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return c.NoContent(http.StatusNoContent)
}
