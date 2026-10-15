package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"recipes/internal/jwt_manager"
	"recipes/internal/models"
	"recipes/internal/recipe_service"
)

const maxFeedbackRequestBytes = 30 << 20

// bindFeedbackRequest reads a JSON body, or a multipart body with a JSON
// "feedback" field and up to three "photos" file fields.
func bindFeedbackRequest(c echo.Context, target *models.FeedbackRequest) ([]recipe_service.PictureUpload, error) {
	mediaType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusUnsupportedMediaType, "missing or invalid content type")
	}
	var raw io.Reader
	var photos []recipe_service.PictureUpload
	switch mediaType {
	case echo.MIMEApplicationJSON:
		raw = io.LimitReader(c.Request().Body, maxFeedbackRequestBytes)
	case echo.MIMEMultipartForm:
		if err := c.Request().ParseMultipartForm(maxFeedbackRequestBytes); err != nil {
			return nil, err
		}
		defer c.Request().MultipartForm.RemoveAll()
		if photos, err = readPictureParts(c.Request().MultipartForm.File["photos"]); err != nil {
			return nil, err
		}
		raw = strings.NewReader(c.FormValue("feedback"))
	default:
		return nil, echo.NewHTTPError(http.StatusUnsupportedMediaType, "use application/json or multipart/form-data")
	}
	decoder := json.NewDecoder(raw)
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("feedback must be one JSON object")
	}
	return photos, nil
}

func bindFeedbackError(err error, c echo.Context) error {
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

func parsePage(c echo.Context, defaultLimit int64) (limit, offset int64, err error) {
	limit = defaultLimit
	if v := c.QueryParam("limit"); v != "" {
		limit, err = strconv.ParseInt(v, 10, 64)
		if err != nil || limit < 0 || limit > 100 {
			return 0, 0, errors.New("limit must be between 0 and 100")
		}
	}
	if v := c.QueryParam("offset"); v != "" {
		offset, err = strconv.ParseInt(v, 10, 64)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must not be negative")
		}
	}
	return limit, offset, nil
}

// ListFeedback is GET /feedback - the public board. Query: status
// (open|closed|done|declined), type (bug|idea), q, mine=true (signed in
// only), sort (votes|new), limit, offset.
func (h *Handlers) ListFeedback(c echo.Context) error {
	limit, offset, err := parsePage(c, 20)
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	params := models.ListFeedbackParams{
		Status:      c.QueryParam("status"),
		Type:        c.QueryParam("type"),
		Query:       c.QueryParam("q"),
		SortByVotes: c.QueryParam("sort") != "new",
		Limit:       limit,
		Offset:      offset,
	}
	switch params.Status {
	case "", "closed", models.FeedbackOpen, models.FeedbackDone, models.FeedbackDeclined:
	default:
		return errorResponse(http.StatusBadRequest, "unknown status", nil, c)
	}
	if params.Type != "" && params.Type != models.FeedbackBug && params.Type != models.FeedbackIdea {
		return errorResponse(http.StatusBadRequest, "unknown type", nil, c)
	}
	viewerID := h.optionalUserID(c)
	if c.QueryParam("mine") == "true" {
		if viewerID == "" {
			return errorResponse(http.StatusUnauthorized, "Invalid or expired token", nil, c)
		}
		params.AuthorID = viewerID
	}
	result, err := h.recipes.ListFeedback(c.Request().Context(), viewerID, params)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list feedback", err, c)
	}
	return c.JSON(http.StatusOK, result)
}

// GetFeedback is GET /feedback/:id - public.
func (h *Handlers) GetFeedback(c echo.Context) error {
	feedback, err := h.recipes.GetFeedback(c.Request().Context(), c.Param("id"), h.optionalUserID(c))
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, feedback)
}

// CreateFeedback is POST /feedback.
func (h *Handlers) CreateFeedback(c echo.Context) error {
	var body models.FeedbackRequest
	photos, err := bindFeedbackRequest(c, &body)
	if err != nil {
		return bindFeedbackError(err, c)
	}
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	feedback, err := h.recipes.CreateFeedback(c.Request().Context(), jwt.UserId, body, photos)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusCreated, feedback)
}

// UpdateFeedback is PATCH /feedback/:id - the author, while it is open.
func (h *Handlers) UpdateFeedback(c echo.Context) error {
	var body models.FeedbackRequest
	photos, err := bindFeedbackRequest(c, &body)
	if err != nil {
		return bindFeedbackError(err, c)
	}
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	feedback, err := h.recipes.UpdateFeedback(c.Request().Context(), c.Param("id"), jwt.UserId, body, photos)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, feedback)
}

// DeleteFeedback is DELETE /feedback/:id - the author while it is open, or
// an admin.
func (h *Handlers) DeleteFeedback(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	if err := h.recipes.DeleteFeedback(c.Request().Context(), c.Param("id"), jwt.UserId, jwt.Admin); err != nil {
		return recipeServiceError(err, c)
	}
	return c.NoContent(http.StatusNoContent)
}

// VoteFeedback is POST /feedback/:id/vote.
func (h *Handlers) VoteFeedback(c echo.Context) error {
	return h.setFeedbackVote(c, true)
}

// UnvoteFeedback is DELETE /feedback/:id/vote.
func (h *Handlers) UnvoteFeedback(c echo.Context) error {
	return h.setFeedbackVote(c, false)
}

func (h *Handlers) setFeedbackVote(c echo.Context, vote bool) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	feedback, err := h.recipes.VoteFeedback(c.Request().Context(), c.Param("id"), jwt.UserId, vote)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, feedback)
}

// FeedbackUpdates is GET /feedback/updates - the caller's own feedback that
// an admin closed since they last looked.
func (h *Handlers) FeedbackUpdates(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	result, err := h.recipes.FeedbackUpdates(c.Request().Context(), jwt.UserId)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not list feedback updates", err, c)
	}
	return c.JSON(http.StatusOK, result)
}

// MarkFeedbackSeen is POST /feedback/:id/seen - the author acknowledges a
// status change.
func (h *Handlers) MarkFeedbackSeen(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	if err := h.recipes.MarkFeedbackSeen(c.Request().Context(), c.Param("id"), jwt.UserId); err != nil {
		return recipeServiceError(err, c)
	}
	return c.NoContent(http.StatusNoContent)
}

// AdminListFeedback is GET /admin/feedback?tab=new|open|done|declined|all.
func (h *Handlers) AdminListFeedback(c echo.Context) error {
	limit, offset, err := parsePage(c, 20)
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	result, err := h.recipes.ListFeedbackForAdmin(c.Request().Context(), c.QueryParam("tab"), limit, offset)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, result)
}

// AdminSetFeedbackStatus is POST /admin/feedback/:id/status.
func (h *Handlers) AdminSetFeedbackStatus(c echo.Context) error {
	var body models.SetFeedbackStatusRequest
	if err := c.Bind(&body); err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), nil, c)
	}
	feedback, err := h.recipes.SetFeedbackStatus(c.Request().Context(), c.Param("id"), body.Status)
	if err != nil {
		return recipeServiceError(err, c)
	}
	return c.JSON(http.StatusOK, feedback)
}

// AdminMarkFeedbackSeen is POST /admin/feedback/:id/seen - takes an item out
// of the "New" tab.
func (h *Handlers) AdminMarkFeedbackSeen(c echo.Context) error {
	if err := h.recipes.MarkFeedbackAdminSeen(c.Request().Context(), c.Param("id")); err != nil {
		return recipeServiceError(err, c)
	}
	return c.NoContent(http.StatusNoContent)
}
