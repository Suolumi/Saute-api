package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"recipes/internal/jwt_manager"
	"recipes/internal/models"
	"recipes/internal/recipe_service"
	"recipes/internal/utils"
)

const maxRecipeRequestBytes = 90 << 20

// stepPictureFieldPrefix names the multipart file field for a new picture on
// one step, e.g. "step_picture_2" for the step at index 2 in the submitted
// steps array.
const stepPictureFieldPrefix = "step_picture_"

func recipeServiceError(err error, c echo.Context) error {
	switch {
	case errors.Is(err, recipe_service.ErrInvalid):
		return errorResponse(http.StatusUnprocessableEntity, err.Error(), nil, c)
	case errors.Is(err, recipe_service.ErrNotFound):
		return errorResponse(http.StatusNotFound, "Recipe not found", nil, c)
	case errors.Is(err, recipe_service.ErrHasFavorites):
		return errorResponse(http.StatusConflict, "Recipe has favorites and cannot be deleted", nil, c)
	case errors.Is(err, recipe_service.ErrRecipeReferenced):
		return errorResponse(http.StatusConflict, "Recipe is referenced by other recipes and cannot be deleted", nil, c)
	case errors.Is(err, recipe_service.ErrPictureCapReached):
		return errorResponse(http.StatusConflict, "Picture limit reached for this recipe", nil, c)
	case errors.Is(err, recipe_service.ErrPictureNotFound):
		return errorResponse(http.StatusNotFound, "Picture not found", nil, c)
	case errors.Is(err, recipe_service.ErrForbidden):
		return errorResponse(http.StatusForbidden, "Forbidden", nil, c)
	case errors.Is(err, recipe_service.ErrAlreadyVariation):
		return errorResponse(http.StatusUnprocessableEntity, "Recipe is already a variation", nil, c)
	case errors.Is(err, recipe_service.ErrRecipeHasVariations):
		return errorResponse(http.StatusConflict, "Recipe already has its own variations", nil, c)
	case errors.Is(err, recipe_service.ErrTargetIsVariation):
		return errorResponse(http.StatusUnprocessableEntity, "Target recipe is itself a variation", nil, c)
	case errors.Is(err, recipe_service.ErrCategoryMismatch):
		return errorResponse(http.StatusUnprocessableEntity, "Recipe and target must share the same category", nil, c)
	case errors.Is(err, recipe_service.ErrReferenceLoop):
		return errorResponse(http.StatusConflict, "Linking would create a recipe that references its own family", nil, c)
	case errors.Is(err, recipe_service.ErrNotVariation):
		return errorResponse(http.StatusUnprocessableEntity, "Recipe is not a variation", nil, c)
	default:
		return errorResponse(http.StatusInternalServerError, "Could not process recipe", err, c)
	}
}

func readPictureParts(files []*multipart.FileHeader) ([]recipe_service.PictureUpload, error) {
	pictures := make([]recipe_service.PictureUpload, 0, len(files))
	total := 0
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxRecipeRequestBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		total += len(data)
		if total > maxRecipeRequestBytes {
			return nil, errors.New("pictures exceed the request size limit")
		}
		pictures = append(pictures, recipe_service.PictureUpload{
			Filename: header.Filename, MediaType: header.Header.Get(echo.HeaderContentType), Data: data,
		})
	}
	return pictures, nil
}

// readStepPictureParts collects the "step_picture_<index>" file fields into a
// map keyed by step index. Any other field name is ignored (it belongs to
// "pictures" or is a stray part).
func readStepPictureParts(files map[string][]*multipart.FileHeader) (map[int]recipe_service.PictureUpload, error) {
	result := make(map[int]recipe_service.PictureUpload)
	for name, headers := range files {
		suffix, ok := strings.CutPrefix(name, stepPictureFieldPrefix)
		if !ok {
			continue
		}
		if len(headers) != 1 {
			return nil, fmt.Errorf("field %q must carry exactly one file", name)
		}
		index, err := strconv.Atoi(suffix)
		if err != nil || index < 0 {
			return nil, fmt.Errorf("invalid step picture field %q", name)
		}
		pictures, err := readPictureParts(headers)
		if err != nil {
			return nil, err
		}
		result[index] = pictures[0]
	}
	return result, nil
}

func bindRecipeRequest(c echo.Context, target any) ([]recipe_service.PictureUpload, map[int]recipe_service.PictureUpload, error) {
	mediaType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil {
		return nil, nil, echo.NewHTTPError(http.StatusUnsupportedMediaType, "missing or invalid content type")
	}
	switch mediaType {
	case echo.MIMEApplicationJSON:
		decoder := json.NewDecoder(io.LimitReader(c.Request().Body, maxRecipeRequestBytes+1))
		if err := decoder.Decode(target); err != nil {
			return nil, nil, err
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, nil, errors.New("request body must contain one JSON object")
		}
		return nil, nil, nil
	case echo.MIMEMultipartForm:
		if err := c.Request().ParseMultipartForm(maxRecipeRequestBytes); err != nil {
			return nil, nil, err
		}
		defer c.Request().MultipartForm.RemoveAll()
		recipeJSON := c.FormValue("recipe")
		if strings.TrimSpace(recipeJSON) == "" {
			return nil, nil, errors.New("multipart field recipe is required")
		}
		decoder := json.NewDecoder(strings.NewReader(recipeJSON))
		if err := decoder.Decode(target); err != nil {
			return nil, nil, err
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, nil, errors.New("recipe field must contain one JSON object")
		}
		pictures, err := readPictureParts(c.Request().MultipartForm.File["pictures"])
		if err != nil {
			return nil, nil, err
		}
		stepPictures, err := readStepPictureParts(c.Request().MultipartForm.File)
		if err != nil {
			return nil, nil, err
		}
		return pictures, stepPictures, nil
	default:
		return nil, nil, echo.NewHTTPError(http.StatusUnsupportedMediaType, "use application/json or multipart/form-data")
	}
}

// CreateRecipe creates a complete recipe. Multipart requests contain a JSON
// `recipe` field and zero or more `pictures` file fields.
func (h *Handlers) CreateRecipe(c echo.Context) error {
	var body models.CreateRecipe
	pictures, stepPictures, err := bindRecipeRequest(c, &body)
	if err != nil {
		var httpErr *echo.HTTPError
		if errors.As(err, &httpErr) {
			message, ok := httpErr.Message.(string)
			if !ok {
				message = http.StatusText(httpErr.Code)
			}
			return errorResponse(httpErr.Code, message, nil, c)
		}
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	recipe, err := h.recipes.Create(c.Request().Context(), jwt.UserId, body, pictures, stepPictures)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, recipe)
}

// optionalUserID returns the authenticated user's id from a bearer access
// token, or "" when absent or invalid. Used on routes that stay open to
// anonymous requests but decorate the response when a user is known.
func (h *Handlers) optionalUserID(c echo.Context) string {
	token, ok := strings.CutPrefix(c.Request().Header.Get(echo.HeaderAuthorization), "Bearer ")
	if !ok || token == "" {
		return ""
	}
	claims, err := jwt_manager.DecodeJWT[models.TokenClaims](h.jm.AccessSecret, token, jwt_manager.PurposeAccess)
	if err != nil {
		return ""
	}
	return claims.UserId
}

func (h *Handlers) GetRecipes(c echo.Context) error {
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
		body.Limit = 10
	}
	if body.Limit < 0 || body.Limit > 100 || body.Offset < 0 {
		return errorResponse(http.StatusBadRequest, "limit must be between 0 and 100 and offset must not be negative", nil, c)
	}
	recipes, count, err := h.recipes.List(c.Request().Context(), body, h.optionalUserID(c))
	if err != nil {
		return recipeServiceError(err, c)
	}
	if recipes == nil {
		recipes = []models.RecipePreview{}
	}
	return c.JSON(http.StatusOK, models.GetRecipesResponse{Length: count, Items: recipes})
}

func (h *Handlers) GetRecipe(c echo.Context) error {
	if len(c.QueryParams()["locale"]) > 1 {
		return errorResponse(http.StatusBadRequest, "locale may be specified only once", nil, c)
	}
	recipe, err := h.recipes.Get(c.Request().Context(), c.Param("id"), c.QueryParam("locale"), h.optionalUserID(c))
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, recipe)
}

// FavoriteRecipe marks the recipe as favorited by the authenticated user.
// Idempotent: favoriting an already-favorited recipe succeeds without effect.
func (h *Handlers) FavoriteRecipe(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	if err := h.recipes.AddFavorite(c.Request().Context(), jwt.UserId, c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return messageResponse(http.StatusOK, "Recipe favorited", c)
}

// UnfavoriteRecipe removes the authenticated user's favorite on the recipe.
// Idempotent: un-favoriting a recipe that wasn't favorited succeeds without
// effect.
func (h *Handlers) UnfavoriteRecipe(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	if err := h.recipes.RemoveFavorite(c.Request().Context(), jwt.UserId, c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return messageResponse(http.StatusOK, "Recipe unfavorited", c)
}

func (h *Handlers) UpdateRecipe(c echo.Context) error {
	var body models.UpdateRecipeRequest
	pictures, stepPictures, err := bindRecipeRequest(c, &body)
	if err != nil {
		var httpErr *echo.HTTPError
		if errors.As(err, &httpErr) {
			message, _ := httpErr.Message.(string)
			return errorResponse(httpErr.Code, message, nil, c)
		}
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	recipe := c.Get("recipe").(models.Recipe)
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	// fullPictureAccess: an admin editing a recipe they don't own is a
	// deliberate moderation action (see the admin recipes table) that may
	// touch any picture, author's or contributor's; the recipe's own author
	// (admin or not) only ever controls their own pictures - see mergePatch.
	fullPictureAccess := jwt.Admin && recipe.Author != nil && recipe.Author.Id != nil && recipe.Author.Id.Hex() != jwt.UserId
	updated, err := h.recipes.Update(c.Request().Context(), recipe, body, pictures, stepPictures, fullPictureAccess)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, updated)
}

// AddRecipePicture adds one picture to a recipe on behalf of the
// authenticated user - the recipe's author or any other user, who becomes a
// contributor. Multipart, a single "picture" file field.
func (h *Handlers) AddRecipePicture(c echo.Context) error {
	fileHeader, err := c.FormFile("picture")
	if err != nil {
		return errorResponse(http.StatusBadRequest, "picture file is required", nil, c)
	}
	pictures, err := readPictureParts([]*multipart.FileHeader{fileHeader})
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	recipe := c.Get("recipe").(models.Recipe)
	updated, err := h.recipes.AddPicture(c.Request().Context(), recipe, jwt.UserId, pictures[0])
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, updated)
}

// RemoveRecipePicture removes one picture from a recipe. Permission
// (contributor removing their own, author removing any, or admin) is
// enforced by recipe_service.RemovePicture.
func (h *Handlers) RemoveRecipePicture(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	recipe := c.Get("recipe").(models.Recipe)
	updated, err := h.recipes.RemovePicture(c.Request().Context(), recipe, c.Param("filename"), jwt.UserId, jwt.Admin)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, updated)
}

// RecipeLoaderMiddleware loads the recipe named by :id into context without
// any author/admin gate - for routes any authenticated user may call, where
// the handler or service enforces whatever finer-grained permission applies
// (see AddRecipePicture/RemoveRecipePicture).
func (h *Handlers) RecipeLoaderMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		recipe, err := h.db.GetRecipeById(c.Param("id"))
		if err != nil {
			return errorResponse(http.StatusNotFound, "Recipe not found", nil, c)
		}
		c.Set("recipe", recipe)
		return next(c)
	}
}

func (h *Handlers) DeleteRecipe(c echo.Context) error {
	recipe, err := h.recipes.Delete(c.Request().Context(), c.Param("id"))
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, recipe)
}

// LinkRecipeVariation turns the path recipe into a variation of the recipe
// named in the body - reachable by the recipe's own author or an admin (see
// RecipeAuthorMiddleware, which also loads and gates on "recipe"). See
// recipe_service.LinkVariation for the validation this goes through.
func (h *Handlers) LinkRecipeVariation(c echo.Context) error {
	var body models.LinkVariationRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	updated, err := h.recipes.LinkVariation(c.Request().Context(), c.Param("id"), body.VariationOf)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, updated)
}

// RetranslateRecipe schedules a fresh translation of one recipe into every
// configured target locale. Admin only; returns once the work is queued.
func (h *Handlers) RetranslateRecipe(c echo.Context) error {
	if err := h.recipes.Retranslate(c.Request().Context(), c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return messageResponse(http.StatusAccepted, "Retranslation scheduled", c)
}

func (h *Handlers) RecipeAuthorMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
		recipe, err := h.db.GetRecipeById(c.Param("id"))
		if err != nil {
			return errorResponse(http.StatusNotFound, "Recipe not found", nil, c)
		}
		c.Set("recipe", recipe)
		if recipe.Author.Id.Hex() == jwt.UserId || jwt.Admin {
			return next(c)
		}
		return errorResponse(http.StatusUnauthorized, "Unauthorized", nil, c)
	}
}
