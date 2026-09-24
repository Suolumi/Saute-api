package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	echojwt "github.com/labstack/echo-jwt/v4"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"golang.org/x/time/rate"
	"recipes/internal/config"
	"recipes/internal/database"
	"recipes/internal/database/mongo"
	"recipes/internal/images_manager"
	"recipes/internal/jwt_manager"
	"recipes/internal/mail_sender"
	"recipes/internal/mcp_server"
	"recipes/internal/models"
	"recipes/internal/recipe_service"
	"recipes/internal/translator"
	"recipes/internal/utils"
)

type Handlers struct {
	db      database.Database
	e       *echo.Echo
	jm      *jwt_manager.JwtManager
	cfg     *config.RuntimeConfig
	dbCfg   *config.DatabaseConfig
	ms      *mail_sender.MailSender
	recipes *recipe_service.Service
	mcpAuth *mcp_server.Authenticator
	mcp     *mcp_server.Server
}

func New(cfg *config.Config) (*Handlers, error) {
	db, err := mongo.New(cfg.Db)
	if err != nil {
		return nil, err
	}
	startupTimeout := cfg.Db.Timeout
	if startupTimeout < time.Minute {
		startupTimeout = time.Minute
	}
	startupCtx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	ready := false
	defer func() {
		if !ready {
			_ = db.Close(context.Background())
		}
	}()
	if err := db.EnsureIndexes(startupCtx); err != nil {
		_ = db.Close(context.Background())
		return nil, err
	}

	jm := jwt_manager.New(cfg.Jwt)

	// Create admin user
	adminUser, adminConflictErr := db.UserConflicts(models.UserDB{
		Username: cfg.Db.AdminUsername,
		Email:    cfg.Db.AdminMail,
	})
	switch {
	case adminConflictErr == nil:
		_, err = db.CreateUser(models.UserDB{
			Username: cfg.Db.AdminUsername,
			Email:    cfg.Db.AdminMail,
			Password: cfg.Db.AdminPassword,
			Admin:    true,
		})
		if err != nil {
			return nil, err
		}
	case errors.Is(adminConflictErr, mongo.UserConflictError) && adminUser.Admin:
		// The configured admin already exists.
	case errors.Is(adminConflictErr, mongo.UserConflictError):
		return nil, fmt.Errorf("username or email is already taken by a non-admin user")
	default:
		return nil, fmt.Errorf("check configured admin user: %w", adminConflictErr)
	}

	transl, err := translator.New(cfg.Translator)
	if err != nil {
		utils.LogError("Failed to create translator", err)
	}
	var recipeTranslator recipe_service.Translator
	if transl != nil {
		recipeTranslator = transl
	}

	recipeService, err := recipe_service.New(db, recipeTranslator, cfg.Cfg.RecipeImageDir, cfg.MCP.MaxDecodedPictureBytes, cfg.TranslationLocales)
	if err != nil {
		return nil, err
	}

	mcpAuth := mcp_server.NewAuthenticator(db, cfg.MCP.JWTSecret, cfg.MCP.TokenExpiration)
	mcpServer, err := mcp_server.New(cfg.MCP, recipeService, mcpAuth.Verifier())
	if err != nil {
		return nil, err
	}

	e := echo.New()
	e.HTTPErrorHandler = httpErrorHandler
	handlers := &Handlers{
		db:      db,
		e:       e,
		jm:      jm,
		cfg:     cfg.Cfg,
		dbCfg:   cfg.Db,
		ms:      mail_sender.New(cfg.Mails),
		recipes: recipeService,
		mcpAuth: mcpAuth,
		mcp:     mcpServer,
	}
	ready = true
	return handlers, nil
}

func (h *Handlers) RegisterEndpoints() {
	h.e.Use(middleware.RequestID())
	h.e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {
			_, _ = fmt.Fprintf(os.Stderr, "[PANIC RECOVERED] error: %v\nstack: %s\n", err, stack)
			return nil
		},
	}))
	h.e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:  []string{h.cfg.WebappUrl},
		AllowHeaders:  []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization, "Idempotency-Key"},
		AllowMethods:  []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		ExposeHeaders: []string{echo.HeaderXRequestID},
	}))

	adminMiddleware := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			jwt := jwt_manager.GetJwt[*models.TokenClaims](c)
			if jwt.Admin {
				return next(c)
			}
			return errorResponse(http.StatusForbidden, "Forbidden", nil, c)
		}
	}

	unprotectedRouter := h.e.Group("/api/v1")
	accessClaims := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			claims, ok := c.Get("jwt").(*jwt.Token)
			if !ok || claims == nil {
				return errorResponse(http.StatusUnauthorized, "Invalid or expired token", nil, c)
			}
			access, ok := claims.Claims.(*models.TokenClaims)
			if !ok || access.Purpose != jwt_manager.PurposeAccess || access.UserId == "" {
				return errorResponse(http.StatusUnauthorized, "Invalid or expired token", nil, c)
			}
			return next(c)
		}
	}
	protectedRouter := h.e.Group("/api/v1",
		h.QueryJwt,
		echojwt.WithConfig(echojwt.Config{
			NewClaimsFunc: jwt_manager.NewJwtClaims[models.TokenClaims],
			SigningKey:    []byte(h.jm.AccessSecret),
			SigningMethod: "HS256",
			ContextKey:    "jwt",
		}),
		accessClaims,
	)
	authLimiter := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate: rate.Limit(0.5), Burst: 30, ExpiresIn: time.Minute,
		}),
	})

	h.e.Any("/mcp", echo.WrapHandler(h.mcp.Handler()))

	unprotectedRouter.POST("/login", h.Login, middleware.BodyLimit("1M"), authLimiter)
	unprotectedRouter.POST("/register", h.Register, middleware.BodyLimit("1M"), authLimiter)
	unprotectedRouter.POST("/refresh", h.Refresh, middleware.BodyLimit("1M"), authLimiter)
	unprotectedRouter.POST("/forgot-password", h.ForgotPassword, middleware.BodyLimit("1M"), authLimiter)
	unprotectedRouter.POST("/forgot-password/:token", h.ResetPassword, middleware.BodyLimit("1M"), authLimiter)

	unprotectedRouter.Static("/pictures", h.cfg.ImagesDir)

	// User self routes
	protectedRouter.GET("/users/me", h.GetMe)
	protectedRouter.PUT("/users/me", h.UpdateMe)
	protectedRouter.DELETE("/users/me", h.DeleteMe)
	protectedRouter.POST("/users/me/picture", h.UploadProfilePicture, middleware.BodyLimit("10M"))
	protectedRouter.DELETE("/users/me/picture", h.DeleteProfilePicture)

	// MCP token management
	protectedRouter.POST("/mcp/token", h.MintMCPToken, authLimiter)
	protectedRouter.DELETE("/mcp/token", h.RevokeMCPToken, authLimiter)

	// User routes
	unprotectedRouter.GET("/users", h.GetUsers)
	unprotectedRouter.GET("/users/:id", h.GetUser)
	protectedRouter.PUT("/users/:id", h.UpdateUser, adminMiddleware)
	protectedRouter.DELETE("/users/:id", h.DeleteUser, adminMiddleware)
	protectedRouter.POST("/users/:id/picture", h.UpdateUserPicture, adminMiddleware, middleware.BodyLimit("10M"))
	protectedRouter.DELETE("/users/:id/picture", h.DeleteUserPicture, adminMiddleware)

	// Admin back-office routes
	adminRouter := protectedRouter.Group("/admin", adminMiddleware)
	adminRouter.GET("/routes", h.AdminRoutesManifest)
	adminRouter.GET("/users", h.AdminListUsers)
	adminRouter.PATCH("/users/:id/admin-status", h.SetUserAdminStatus, authLimiter)
	adminRouter.POST("/users/:id/send-password-reset", h.AdminSendPasswordReset, authLimiter)
	adminRouter.DELETE("/users/:id/mcp-token", h.AdminRevokeMcpToken, authLimiter)
	adminRouter.GET("/recipes", h.AdminListRecipes)
	adminRouter.GET("/recipes/:id/favorites", h.AdminGetRecipeFavorites)
	adminRouter.DELETE("/recipes/:id/variation-of", h.AdminDetachRecipeVariation, authLimiter)
	adminRouter.GET("/system/stats", h.AdminStats)
	adminRouter.POST("/system/cleanup-images", h.AdminCleanupImages, authLimiter)
	adminRouter.GET("/translation-suggestions", h.AdminListTranslationSuggestions)
	adminRouter.POST("/translation-suggestions/:id/approve", h.AdminApproveTranslationSuggestion, authLimiter)
	adminRouter.POST("/translation-suggestions/:id/reject", h.AdminRejectTranslationSuggestion, authLimiter)
	adminRouter.GET("/translation-overrides", h.AdminListTranslationOverrides)
	adminRouter.DELETE("/translation-overrides/:id", h.AdminClearTranslationOverride, authLimiter)

	// Recipes routes
	unprotectedRouter.GET("/recipes", h.GetRecipes)
	protectedRouter.POST("/recipes", h.CreateRecipe, middleware.BodyLimit("90M"))
	unprotectedRouter.GET("/recipes/:id", h.GetRecipe)
	protectedRouter.PATCH("/recipes/:id", h.UpdateRecipe, h.RecipeAuthorMiddleware, middleware.BodyLimit("90M"))
	protectedRouter.DELETE("/recipes/:id", h.DeleteRecipe, h.RecipeAuthorMiddleware)
	protectedRouter.PATCH("/recipes/:id/variation-of", h.LinkRecipeVariation, h.RecipeAuthorMiddleware)
	protectedRouter.POST("/recipes/:id/retranslate", h.RetranslateRecipe, adminMiddleware, authLimiter)
	protectedRouter.POST("/recipes/:id/favorite", h.FavoriteRecipe)
	protectedRouter.DELETE("/recipes/:id/favorite", h.UnfavoriteRecipe)
	protectedRouter.POST("/recipes/:id/pictures", h.AddRecipePicture, h.RecipeLoaderMiddleware, middleware.BodyLimit("10M"))
	protectedRouter.DELETE("/recipes/:id/pictures/:filename", h.RemoveRecipePicture, h.RecipeLoaderMiddleware)
	protectedRouter.POST("/recipes/:id/translation-suggestions", h.SubmitTranslationSuggestion, authLimiter)

	// Recipes images
	unprotectedRouter.Static("/recipe-pictures", h.cfg.RecipeImageDir)
}

// cleanupImages removes unreferenced picture files older than 24h from both
// image directories, returning how many were removed from each (keyed by
// directory). Shared by the startup background sweep and the on-demand admin
// route.
func (h *Handlers) cleanupImages(ctx context.Context) (map[string]int, error) {
	references, err := h.db.ReferencedPictures(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not collect image references: %w", err)
	}
	removed := make(map[string]int, 2)
	dirs := map[string]string{"pictures": h.cfg.ImagesDir, "recipe_pictures": h.cfg.RecipeImageDir}
	for label, directory := range dirs {
		count, err := images_manager.CleanupUnreferenced(directory, references, time.Now(), 24*time.Hour)
		if err != nil {
			return removed, fmt.Errorf("could not clean image directory %s: %w", directory, err)
		}
		removed[label] = count
	}
	return removed, nil
}

func (h *Handlers) StartBackgroundMaintenance(ctx context.Context) {
	go func() {
		removed, err := h.cleanupImages(ctx)
		if err != nil {
			utils.LogError("could not clean image directories", err)
			return
		}
		for directory, count := range removed {
			if count > 0 {
				log.Printf("removed %d unreferenced images from %s", count, directory)
			}
		}
	}()
}

func (h *Handlers) Run(ctx context.Context, port int) error {
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.db.Close(closeCtx)
	}()
	server := h.e.Server
	server.Addr = fmt.Sprintf(":%d", port)
	server.ReadHeaderTimeout = 10 * time.Second
	server.ReadTimeout = 2 * time.Minute
	server.WriteTimeout = 2 * time.Minute
	server.IdleTimeout = 2 * time.Minute
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.e.Shutdown(shutdownCtx)
	}()
	err := h.e.StartServer(server)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// TODO: Limiter le nombre de caractères par titre de recette
