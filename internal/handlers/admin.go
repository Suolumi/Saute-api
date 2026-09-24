package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/config"
	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/jwt_manager"
	"recipes/internal/models"
	"recipes/internal/utils"
)

var (
	ErrSelfDemotion           = errors.New("cannot remove your own admin status")
	ErrBootstrapAdminDemotion = errors.New("cannot remove admin status from the configured bootstrap admin account")
	ErrLastAdminDemotion      = errors.New("cannot remove admin status from the last remaining admin")
)

// evaluateAdminDemotion enforces the back-office's three safeguards against
// demoting a user out of admin: no self-demotion, the configured bootstrap
// admin account can never be demoted, and the last remaining admin can never
// be demoted. Promotions, and updates that leave Admin unchanged, never go
// through this check - see SetUserAdminStatus.
func evaluateAdminDemotion(callerID string, target models.UserDB, dbCfg *config.DatabaseConfig, adminCount int64) error {
	if target.Id != nil && target.Id.Hex() == callerID {
		return ErrSelfDemotion
	}
	if target.Username == dbCfg.AdminUsername || target.Email == dbCfg.AdminMail {
		return ErrBootstrapAdminDemotion
	}
	if adminCount <= 1 {
		return ErrLastAdminDemotion
	}
	return nil
}

// AdminListUsers lists users for the back-office. Unlike the public
// GET /users (which only reveals full user documents to a caller with an
// admin JWT already attached - dead code today, since that route runs with
// no JWT middleware at all), this route is admin-gated and always returns
// full UserDB documents.
func (h *Handlers) AdminListUsers(c echo.Context) error {
	var queryParams models.GetUsersRequest
	if err := utils.BindQuery(c, &queryParams); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), err, c)
	}
	if queryParams.Limit == 0 {
		queryParams.Limit = 10
	}
	if queryParams.Limit < 0 || queryParams.Limit > 100 || queryParams.Offset < 0 {
		return errorResponse(http.StatusBadRequest, "limit must be between 0 and 100 and offset must not be negative", nil, c)
	}
	users, nbUsers, err := h.db.GetUsers(queryParams.Username, queryParams.Limit, queryParams.Offset)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not get users", err, c)
	}
	if users == nil {
		users = []models.UserDB{}
	}
	return c.JSON(http.StatusOK, models.GetUsersResponse{Length: nbUsers, Items: users})
}

// SetUserAdminStatus promotes or demotes a user. Promotions are unrestricted;
// demotions go through evaluateAdminDemotion. This bypasses UpdateUser
// entirely (which accepts a raw Admin field with no safeguards, and whose
// struct-bind + bson `omitempty` update path can't reliably unset booleans
// anyway) via a direct $set, the same pattern DeleteUserPicture already uses
// to clear a field.
func (h *Handlers) SetUserAdminStatus(c echo.Context) error {
	userId := c.Param("id")
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)

	var body models.SetAdminStatusRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), err, c)
	}

	target, err := h.db.GetUserById(userId)
	if err != nil {
		return errorResponse(http.StatusNotFound, "User not found", err, c)
	}

	if !body.Admin && target.Admin {
		adminCount, err := h.db.CountAdmins(c.Request().Context())
		if err != nil {
			return errorResponse(http.StatusInternalServerError, "Could not check admin count", err, c)
		}
		if err := evaluateAdminDemotion(jwt.UserId, target, h.dbCfg, adminCount); err != nil {
			return errorResponse(http.StatusForbidden, err.Error(), nil, c)
		}
	}

	updated, err := h.db.UpdateUserInterfaceById(userId, struct {
		Admin bool `bson:"admin"`
	}{Admin: body.Admin})
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not update admin status", err, c)
	}
	return c.JSON(http.StatusOK, updated)
}

// AdminSendPasswordReset triggers the same reset-password email as the
// self-service ForgotPassword flow (auth.go), on an admin-chosen user
// instead of one identified by the requester.
func (h *Handlers) AdminSendPasswordReset(c echo.Context) error {
	userId := c.Param("id")
	user, err := h.db.GetUserById(userId)
	if err != nil {
		return errorResponse(http.StatusNotFound, "User not found", err, c)
	}

	_, token, err := h.jm.Generate(jwt_manager.PurposeReset, user.Id.Hex(), false)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not create reset token", err, c)
	}

	locale := c.QueryParam("locale")
	if locale == "" {
		locale = "en"
	}
	err = h.ms.SendMail("Password reinitialization",
		[]string{user.Email},
		h.ms.ResetPasswordMail, map[string]string{
			"USER_NAME":  user.Username,
			"RESET_LINK": fmt.Sprintf("%s/%s/forgot-password/%s", strings.TrimRight(h.cfg.WebappUrl, "/"), locale, token),
			"USER_EMAIL": user.Email,
		})
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not send the reset email", err, c)
	}
	return messageResponse(http.StatusOK, "Password reset email sent", c)
}

// AdminRevokeMcpToken invalidates every MCP token currently issued to a user
// (bumping their auth-version counter, the same mechanism the user's own
// self-revoke uses). The token scheme is stateless - there is no way to know
// whether one existed beforehand.
func (h *Handlers) AdminRevokeMcpToken(c echo.Context) error {
	userId := c.Param("id")
	if err := h.db.BumpMCPAuthVersion(c.Request().Context(), userId); err != nil {
		if errors.Is(err, mongorepo.UserNotFoundError) {
			return errorResponse(http.StatusNotFound, "User not found", err, c)
		}
		return errorResponse(http.StatusInternalServerError, "Could not revoke MCP token", err, c)
	}
	return messageResponse(http.StatusOK, "MCP token revoked", c)
}

// AdminListRecipes is the moderation browse view: every recipe and variation
// across every user, individually - never family-collapsed. Reuses the
// existing List/GetRecipesRequest machinery with OwnRecipes forced true,
// which already produces exactly this flat shape (see
// buildRecipeFilterPipeline) when Author is left blank.
func (h *Handlers) AdminListRecipes(c echo.Context) error {
	for _, name := range []string{"locale", "search_locale"} {
		if len(c.QueryParams()[name]) > 1 {
			return errorResponse(http.StatusBadRequest, name+" may be specified only once", nil, c)
		}
	}
	var body models.GetRecipesRequest
	if err := utils.BindQuery(c, &body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	if body.Limit == 0 {
		body.Limit = 20
	}
	if body.Limit < 0 || body.Limit > 100 || body.Offset < 0 {
		return errorResponse(http.StatusBadRequest, "limit must be between 0 and 100 and offset must not be negative", nil, c)
	}
	body.OwnRecipes = true

	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	recipes, count, err := h.recipes.List(c.Request().Context(), body, jwt.UserId)
	if err != nil {
		return recipeServiceError(err, c)
	}
	if recipes == nil {
		recipes = []models.RecipePreview{}
	}
	return c.JSON(http.StatusOK, models.GetRecipesResponse{Length: count, Items: recipes})
}

// AdminGetRecipeFavorites lists the users who favorited a recipe - the
// reverse lookup of the self-service favorite/favorites_only surface
// (recipes.go) - paginated and sorted alphabetically by username, for
// moderation visibility.
func (h *Handlers) AdminGetRecipeFavorites(c echo.Context) error {
	recipeID := c.Param("id")
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return errorResponse(http.StatusNotFound, "Recipe not found", err, c)
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

	users, total, err := h.db.GetRecipeFavoriters(c.Request().Context(), recipeID, limit, offset)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not get recipe favorites", err, c)
	}
	return c.JSON(http.StatusOK, models.GetUsersResponse{Length: total, Items: users})
}

// AdminDetachRecipeVariation removes a recipe from its family, turning it
// back into a standalone root (recipe_service.DetachVariation). Admin only -
// unlike LinkRecipeVariation, there is no self-service counterpart and no
// MCP tool for this: it's a moderation action, and it's irreversible from
// the app's own UI once done.
func (h *Handlers) AdminDetachRecipeVariation(c echo.Context) error {
	updated, err := h.recipes.DetachVariation(c.Request().Context(), c.Param("id"))
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, updated)
}

// AdminStats backs the back-office dashboard.
func (h *Handlers) AdminStats(c echo.Context) error {
	ctx := c.Request().Context()

	totalUsers, err := h.db.CountUsers(ctx)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not count users", err, c)
	}
	totalAdmins, err := h.db.CountAdmins(ctx)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not count admins", err, c)
	}
	byCategory, err := h.db.RecipeCountsByCategory(ctx)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not count recipes", err, c)
	}
	var totalRecipes int64
	for _, n := range byCategory {
		totalRecipes += n
	}

	return c.JSON(http.StatusOK, models.AdminStats{
		TotalUsers:        totalUsers,
		TotalAdmins:       totalAdmins,
		TotalRecipes:      totalRecipes,
		RecipesByCategory: byCategory,
	})
}

// AdminCleanupImages synchronously runs the same unreferenced-image sweep as
// the startup background goroutine (see Handlers.cleanupImages), on demand.
func (h *Handlers) AdminCleanupImages(c echo.Context) error {
	removed, err := h.cleanupImages(c.Request().Context())
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not run image cleanup", err, c)
	}
	return c.JSON(http.StatusOK, models.CleanupImagesResult{Removed: removed})
}

// AdminRoutesManifest backs the back-office's generic "call any admin route"
// console - see admin_manifest.go.
func (h *Handlers) AdminRoutesManifest(c echo.Context) error {
	return c.JSON(http.StatusOK, adminRouteManifest)
}

// AdminListTranslationSuggestions lists user-submitted translation fixes for
// review, defaulting to pending ones.
func (h *Handlers) AdminListTranslationSuggestions(c echo.Context) error {
	status := c.QueryParam("status")
	if status == "" {
		status = models.TranslationSuggestionPending
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
	result, err := h.recipes.ListTranslationSuggestionsForAdmin(c.Request().Context(), status, limit, offset)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list translation suggestions", err, c)
	}
	return c.JSON(http.StatusOK, result)
}

// AdminApproveTranslationSuggestion applies a submitted fix to the live
// translation and pins the fields it actually changed as durable overrides.
func (h *Handlers) AdminApproveTranslationSuggestion(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.ApproveTranslationSuggestion(c.Request().Context(), c.Param("id"), jwt.UserId)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, suggestion)
}

// AdminRejectTranslationSuggestion dismisses a submitted fix with no effect.
func (h *Handlers) AdminRejectTranslationSuggestion(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	suggestion, err := h.recipes.RejectTranslationSuggestion(c.Request().Context(), c.Param("id"), jwt.UserId)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, suggestion)
}

// AdminListTranslationOverrides lists which fields of a recipe's translation
// are currently pinned by an approved fix.
func (h *Handlers) AdminListTranslationOverrides(c echo.Context) error {
	recipeID := c.QueryParam("recipe_id")
	if recipeID == "" {
		return errorResponse(http.StatusBadRequest, "recipe_id is required", nil, c)
	}
	result, err := h.recipes.ListTranslationOverridesForAdmin(c.Request().Context(), recipeID, c.QueryParam("locale"))
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, result)
}

// AdminClearTranslationOverride reverts one pinned field back to machine
// translation immediately (re-translating that one locale synchronously).
func (h *Handlers) AdminClearTranslationOverride(c echo.Context) error {
	if err := h.recipes.ClearTranslationOverride(c.Request().Context(), c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return messageResponse(http.StatusOK, "Translation override cleared", c)
}
