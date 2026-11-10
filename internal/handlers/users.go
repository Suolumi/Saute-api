package handlers

import (
	"errors"
	"github.com/labstack/echo/v4"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net/http"
	"os"
	"path/filepath"
	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/images_manager"
	"recipes/internal/jwt_manager"
	"recipes/internal/models"
	"recipes/internal/utils"
)

// @Summary Get self
// @Description Get self
// @Tags Users
// @accept json
// @produce json
// @Success 200 {object} models.UserMe "User"
// @Failure 401 {object} models.ErrorResponse "Invalid or expired jwt"
// @Failure 500 {object} models.ErrorResponse "Failed to get self"
// @Security BearerAuth
// @Router /users/me [get]
func (h *Handlers) GetMe(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)

	user, err := h.db.GetUserById(jwt.UserId)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not get user", err, c)
	}

	return c.JSON(http.StatusOK, utils.DupStruct[models.UserMe](&user))
}

// @Summary Update self
// @Description Update self
// @Tags Users
// @accept json
// @produce json
// @Param request body models.UpdateUser true "User information to update"
// @Success 200 {object} models.UserMe "User"
// @Failure 400 {object} models.ErrorResponse "Bad request"
// @Failure 401 {object} models.ErrorResponse "Invalid or expired jwt"
// @Failure 406 {object} models.ErrorResponse "Username too long / invalid email / insecure password"
// @Failure 409 {object} models.ErrorResponse "User already exists"
// @Failure 500 {object} models.ErrorResponse "Failed to update self"
// @Security BearerAuth
// @Router /users/me [patch]
func (h *Handlers) UpdateMe(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)

	var user models.UpdateUser
	var err error

	err = c.Bind(&user)
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), err, c)
	}

	if len(user.Username) > 32 {
		return errorResponse(http.StatusNotAcceptable, "username cannot be more than 32 characters long", nil, c)
	}
	if user.Email != "" && !utils.IsEmailValid(user.Email) {
		return errorResponse(http.StatusNotAcceptable, "Must be a valid email", nil, c)
	}
	if user.Password != "" && !utils.IsPasswordValid(user.Password) {
		return errorResponse(http.StatusNotAcceptable, utils.PasswordRules, nil, c)
	}

	userdb := utils.DupStruct[models.UserDB](&user)
	if conflict, err := h.db.UserConflicts(models.UserDB{
		Username: user.Username,
		Email:    user.Email,
	}); err != nil {
		if errors.Is(err, mongorepo.UserConflictError) && conflict.Id != nil && conflict.Id.Hex() == jwt.UserId {
			// The current user may keep its own username/email.
		} else if errors.Is(err, mongorepo.UserConflictError) {
			return errorResponse(http.StatusConflict, "Username or email already taken", nil, c)
		} else {
			return errorResponse(http.StatusInternalServerError, "Could not check user conflicts", err, c)
		}
	}

	updatedUser, err := h.db.UpdateUserById(jwt.UserId, userdb)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not update user", err, c)
	}

	return c.JSON(http.StatusOK, utils.DupStruct[models.UserMe](&updatedUser))
}

// @Summary Delete self
// @Description Delete self
// @Tags Users
// @accept json
// @produce json
// @Param request body models.UpdateUser true "User information to update"
// @Success 200 {object} models.UserMe "User"
// @Failure 401 {object} models.ErrorResponse "Invalid or expired jwt"
// @Failure 500 {object} models.ErrorResponse "Failed to delete self / delete picture"
// @Security BearerAuth
// @Router /users/me [delete]
func (h *Handlers) DeleteMe(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)

	user, err := h.db.DeleteUserById(jwt.UserId)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not delete user", err, c)
	}
	fileErr := images_manager.Remove(h.cfg.ImagesDir, user.Picture)
	if fileErr != nil && !os.IsNotExist(fileErr) {
		return errorResponse(http.StatusInternalServerError, "Could not remove profile picture", fileErr, c)
	}

	return c.JSON(http.StatusOK, utils.DupStruct[models.UserMe](&user))
}

// @Summary Update self picture
// @Description Update your profile picture, must be under 8MB
// @Tags Users
// @accept mpfd
// @produce json
// @Param request formData file true "Picture, formData must be named 'file'"
// @Success 200 {object} models.UpdatePictureResponse "OK"
// @Failure 400 {object} models.ErrorResponse "Empty formData / not named correctly"
// @Failure 401 {object} models.ErrorResponse "Invalid or expired token"
// @Failure 406 {object} models.ErrorResponse "Wrong format (not png / jpeg / jpg) / File too heavy"
// @Failure 500 {object} models.ErrorResponse "Failed to: Update user, save the image"
// @Security BearerAuth
// @Router /users/me/picture [post]
func (h *Handlers) UploadProfilePicture(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)

	formFile, err := c.FormFile("file")
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), err, c)
	}

	normalized, err := images_manager.NormalizeMultipartImage(formFile, 8<<20)
	if err != nil {
		return errorResponse(http.StatusNotAcceptable, err.Error(), err, c)
	}

	user, err := h.db.GetUserById(jwt.UserId)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, err.Error(), err, c)
	}

	fileName := primitive.NewObjectID().Hex() + normalized.Extension
	temporaryName := "." + fileName + ".tmp"
	if err := images_manager.SaveBytes(h.cfg.ImagesDir, temporaryName, normalized.Data); err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not save the picture", err, c)
	}
	if err := os.Rename(filepath.Join(h.cfg.ImagesDir, temporaryName), filepath.Join(h.cfg.ImagesDir, fileName)); err != nil {
		_ = images_manager.Remove(h.cfg.ImagesDir, temporaryName)
		return errorResponse(http.StatusInternalServerError, "Could not save the picture", err, c)
	}
	_, err = h.db.UpdateUserById(jwt.UserId, models.UserDB{
		Picture: fileName,
	})
	if err != nil {
		_ = images_manager.Remove(h.cfg.ImagesDir, fileName)
		return errorResponse(http.StatusInternalServerError, "Could not update user picture", err, c)
	}
	if user.Picture != "" {
		_ = images_manager.Remove(h.cfg.ImagesDir, user.Picture)
	}
	return c.JSON(http.StatusOK, models.UpdatePictureResponse{Id: fileName})
}

// @Summary Delete own picture
// @Description Delete your profile picture
// @Tags Users
// @produce json
// @Success 200 {object} models.MessageResponse "Picture deleted"
// @Failure 401 {object} models.ErrorResponse "Invalid or expired token"
// @Failure 404 {object} models.ErrorResponse "Image not found"
// @Failure 422 {object} models.ErrorResponse "No picture for this user"
// @Failure 500 {object} models.ErrorResponse "Failed to: Update user / remove the picture"
// @Security BearerAuth
// @Router /users/me/picture [delete]
func (h *Handlers) DeleteProfilePicture(c echo.Context) error {
	jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
	user, err := h.db.GetUserById(jwt.UserId)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not get user", err, c)
	}

	if user.Picture == "" {
		return errorResponse(http.StatusUnprocessableEntity, "No picture for this user", nil, c)
	}

	_, err = h.db.UpdateUserInterfaceById(jwt.UserId, struct {
		Picture string `bson:"picture"`
	}{})
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not clear profile picture", err, c)
	}
	err = images_manager.Remove(h.cfg.ImagesDir, user.Picture)
	if err != nil && !os.IsNotExist(err) {
		return errorResponse(http.StatusInternalServerError, "Could not remove profile picture", err, c)
	}
	return messageResponse(http.StatusOK, "Image removed successfully", c)
}
