package mcp_server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/config"
	"recipes/internal/models"
	"recipes/internal/recipe_service"
)

const protocolVersion = "2026-07-28"

type ListInput struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by a previous call"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; defaults to 20"`
	Locale string `json:"locale,omitempty" jsonschema:"Requested BCP 47 locale; canonical recipe is returned if translation fails"`
}

type GetInput struct {
	RecipeID string `json:"recipe_id" jsonschema:"Recipe identifier"`
	Locale   string `json:"locale,omitempty" jsonschema:"Requested BCP 47 locale; canonical recipe is returned if translation fails"`
}

// SearchInput drives search_recipes, which - unlike every other tool here -
// looks across every user's recipes, not just the caller's own. Results are
// grouped by recipe family (one entry per root, with variation_count) unless
// VariationOf is set, which instead lists that one family's individual
// variations.
type SearchInput struct {
	Title           string                `json:"title,omitempty" jsonschema:"Case-insensitive title search, also matching against each candidate's own variations so a match surfaces its family root"`
	Kind            models.RecipeKind     `json:"kind,omitempty" jsonschema:"One of breakfast, starter, dish, side-dish, sauce, baking, snack, plate, dessert, or drink"`
	Category        models.RecipeCategory `json:"category,omitempty" jsonschema:"One of food or diy; omitted or food excludes diy, diy lists only diy"`
	Author          string                `json:"author,omitempty" jsonschema:"Case-insensitive username search"`
	Ingredients     []string              `json:"ingredients,omitempty" jsonschema:"Every listed ingredient name must be present, also matching across a family's variations"`
	PreparationTime int                   `json:"preparation_time,omitempty" jsonschema:"Target preparation time in minutes; results are sorted by closeness to it instead of newest-first"`
	TotalTime       int                   `json:"total_time,omitempty" jsonschema:"Target total time (preparation + cooking + resting) in minutes; results are sorted by closeness to it instead of newest-first"`
	VariationOf     string                `json:"variation_of,omitempty" jsonschema:"List only this recipe's family's variations (never the root itself) instead of the default family-collapsed listing"`
	Cursor          string                `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by a previous call"`
	Limit           int                   `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; defaults to 20"`
	Locale          string                `json:"locale,omitempty" jsonschema:"Requested BCP 47 locale; canonical recipe is returned if translation fails"`
}

// IngredientInput is what create_recipe/update_recipe accept: no recipe_ref
// field exists here, so a client cannot set a recipe reference through MCP
// in v1 - search_recipes exists to discover a fork target for
// create_recipe's variation_of, but there is still no way to discover a
// recipe id to use as an ingredient reference.
type IngredientInput struct {
	Name     string  `json:"name" jsonschema:"Ingredient name, e.g. 'Egg' or 'Thyme'"`
	Quantity float64 `json:"quantity" jsonschema:"Numeric amount, e.g. 3 or 0.5"`
	Unit     string  `json:"unit,omitempty" jsonschema:"Optional unit shown between quantity and name. Leave empty for a bare count, e.g. quantity 3 + name 'Egg' renders as '3 Egg'. Set it for a unit of measure or descriptor, e.g. quantity 3 + unit 'leaves' + name 'Thyme' renders as '3 leaves - Thyme'"`
	Label    string  `json:"label,omitempty" jsonschema:"Optional section heading grouping this ingredient with others that share the exact same label, e.g. 'For the dough' or 'For the filling'."`
}

func ingredientsFromInput(inputs []IngredientInput) []models.Ingredient {
	result := make([]models.Ingredient, 0, len(inputs))
	for _, in := range inputs {
		result = append(result, models.Ingredient{Name: in.Name, Quantity: in.Quantity, Unit: in.Unit, Label: in.Label})
	}
	return result
}

type CreateInput struct {
	Title           string                `json:"title"`
	Description     string                `json:"description,omitempty"`
	Quantity        int                   `json:"quantity"`
	Kind            models.RecipeKind     `json:"kind" jsonschema:"One of breakfast, starter, dish, side-dish, sauce, baking, snack, plate, dessert, or drink; required only when category is food, ignored for diy"`
	Category        models.RecipeCategory `json:"category,omitempty" jsonschema:"One of food or diy; defaults to food when omitted. diy skips the kind requirement (course concept doesn't apply) and the website shows DIY-flavored terminology (materials instead of ingredients, etc.)"`
	PreparationTime int                   `json:"preparation_time"`
	CookingTime     int                   `json:"cooking_time"`
	RestingTime     int                   `json:"resting_time"`
	Ingredients     []IngredientInput     `json:"ingredients"`
	Steps           []models.Step         `json:"steps"`
	Locale          string                `json:"locale,omitempty" jsonschema:"BCP 47 locale of the canonical recipe; detected when omitted"`
	VariationOf     string                `json:"variation_of,omitempty" jsonschema:"Id of another recipe - any recipe, not just one the caller owns - to submit this as a variation of instead of a new root recipe. Resolved and flattened to that recipe's family root server-side. category must match the root's category. Use search_recipes to find a fork target's id."`
}

// LinkVariationInput drives link_recipe_variation. RecipeID must be one of
// the caller's own recipes (like every tool but search_recipes); VariationOf
// may be any recipe's id, any author - found via search_recipes, same as
// create_recipe's variation_of.
type LinkVariationInput struct {
	RecipeID    string `json:"recipe_id" jsonschema:"Id of the caller's own standalone recipe to link"`
	VariationOf string `json:"variation_of" jsonschema:"Id of the recipe (any author, found via search_recipes) to link as a variation of; must be a root, not itself a variation"`
}

type UpdateInput struct {
	RecipeID        string                 `json:"recipe_id"`
	Title           *string                `json:"title,omitempty"`
	Description     *string                `json:"description,omitempty"`
	Quantity        *int                   `json:"quantity,omitempty"`
	Kind            *models.RecipeKind     `json:"kind,omitempty"`
	Category        *models.RecipeCategory `json:"category,omitempty"`
	PreparationTime *int                   `json:"preparation_time,omitempty"`
	CookingTime     *int                   `json:"cooking_time,omitempty"`
	RestingTime     *int                   `json:"resting_time,omitempty"`
	Ingredients     *[]IngredientInput     `json:"ingredients,omitempty"`
	Steps           *[]models.Step         `json:"steps,omitempty"`
	Locale          *string                `json:"locale,omitempty" jsonschema:"BCP 47 locale of the canonical recipe"`
	KeepPictureIDs  *[]string              `json:"keep_picture_ids,omitempty" jsonschema:"Ordered existing picture IDs to retain; omit to keep all, or pass an empty list to remove all"`
}

type PictureOutput struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// StepOutput mirrors models.Step but resolves Picture to a full URL (like
// PictureOutput does for recipe-level pictures) instead of a bare filename,
// which is meaningless to a caller without the server's picture base URL.
type StepOutput struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Picture     string `json:"picture,omitempty"`
}

// RecipeRefOutput is a reference ingredient's resolved target.
type RecipeRefOutput struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// IngredientOutput mirrors models.Ingredient for get_my_recipe/
// list_my_recipes, but resolves a reference ingredient's target into
// {id, title} alongside the existing top-level Quantity/Unit, so a
// reference ingredient reads as complete {id, title, quantity, unit} data
// without the client needing a second call.
type IngredientOutput struct {
	Name      string           `json:"name,omitempty"`
	Quantity  float64          `json:"quantity"`
	Unit      string           `json:"unit,omitempty"`
	Label     string           `json:"label,omitempty"`
	RecipeRef *RecipeRefOutput `json:"recipe_ref,omitempty"`
}

func ingredientOutputs(ingredients []models.Ingredient) []IngredientOutput {
	outputs := make([]IngredientOutput, 0, len(ingredients))
	for _, ingredient := range ingredients {
		output := IngredientOutput{Name: ingredient.Name, Quantity: ingredient.Quantity, Unit: ingredient.Unit, Label: ingredient.Label}
		if ingredient.RecipeRef != nil {
			output.RecipeRef = &RecipeRefOutput{ID: ingredient.RecipeRef.Hex(), Title: ingredient.ResolvedRefTitle}
		}
		outputs = append(outputs, output)
	}
	return outputs
}

// AuthorOutput identifies who a recipe belongs to. Only search_recipes'
// results populate it - the other tools are all scoped to the caller's own
// recipes, whose author is implicit.
type AuthorOutput struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type RecipeOutput struct {
	ID              string                `json:"id"`
	Author          *AuthorOutput         `json:"author,omitempty"`
	Title           string                `json:"title"`
	Description     string                `json:"description"`
	Quantity        int                   `json:"quantity"`
	Kind            models.RecipeKind     `json:"kind"`
	Category        models.RecipeCategory `json:"category"`
	PreparationTime int                   `json:"preparation_time"`
	CookingTime     int                   `json:"cooking_time"`
	RestingTime     int                   `json:"resting_time"`
	Ingredients     []IngredientOutput    `json:"ingredients"`
	Steps           []StepOutput          `json:"steps"`
	Pictures        []PictureOutput       `json:"pictures"`
	SourceLocale    string                `json:"source_locale,omitempty"`
	Locale          string                `json:"locale,omitempty"`
	// VariationOf is the id of the recipe this one is a variation of, empty
	// for a root recipe. VariationCount is how many variations a root has
	// (always 0 for a variation - it describes the root's family size, not
	// "siblings of this variation").
	VariationOf    string `json:"variation_of,omitempty"`
	VariationCount int64  `json:"variation_count"`
}

type ListOutput struct {
	Items      []RecipeOutput `json:"items"`
	Total      int64          `json:"total"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type Server struct {
	handler     http.Handler
	recipes     *recipe_service.Service
	pictureBase string
}

func New(cfg *config.MCPConfig, recipes *recipe_service.Service, verifier auth.TokenVerifier) (*Server, error) {
	parsed, err := url.Parse(cfg.PublicURL)
	if err != nil {
		return nil, err
	}
	result := &Server{recipes: recipes, pictureBase: parsed.Scheme + "://" + parsed.Host + "/api/v1/recipe-pictures/"}
	server := mcp.NewServer(&mcp.Implementation{Name: "recipes", Version: "1.0.0"}, &mcp.ServerOptions{
		Instructions: "list_my_recipes/get_my_recipe/create_recipe/update_recipe/link_recipe_variation manage only the authenticated user's own recipes. search_recipes is the one exception: it looks across every user's recipes, read-only, so a fork target's id can be found and passed as create_recipe's variation_of or link_recipe_variation's variation_of. Pictures cannot be uploaded through this MCP server; attach photos via the website.",
		Capabilities: &mcp.ServerCapabilities{},
	})
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "initialize" {
				return nil, fmt.Errorf("legacy MCP initialization is unsupported; use protocol %s", protocolVersion)
			}
			result, err := next(ctx, method, req)
			if err == nil && method == "server/discover" {
				if discovery, ok := result.(*mcp.DiscoverResult); ok {
					discovery.SupportedVersions = []string{protocolVersion}
				}
			}
			return result, err
		}
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_my_recipes", Description: "List the authenticated user's recipes with cursor pagination and optional localization."}, result.list)
	mcp.AddTool(server, &mcp.Tool{Name: "get_my_recipe", Description: "Get one recipe owned by the authenticated user, optionally localized."}, result.get)
	mcp.AddTool(server, &mcp.Tool{Name: "search_recipes", Description: "Search every user's recipes (not just the authenticated user's own) by title, kind, category, author, ingredients, or closeness to a target preparation/total time. Results are grouped by recipe family (one entry per root, with variation_count) unless variation_of is set, which instead lists that family's individual variations. Use this to find a recipe id to pass as create_recipe's variation_of."}, result.search)
	mcp.AddTool(server, &mcp.Tool{Name: "create_recipe", Description: "Create a complete recipe owned by the authenticated user. Pictures are not supported here; attach them via the website. Set variation_of (found via search_recipes) to submit this as a variation of an existing recipe instead of a new root."}, result.create)
	mcp.AddTool(server, &mcp.Tool{Name: "update_recipe", Description: "Patch a recipe owned by the authenticated user and optionally reorder or remove existing pictures via keep_picture_ids. New pictures cannot be uploaded here; attach them via the website."}, result.update)
	mcp.AddTool(server, &mcp.Tool{Name: "link_recipe_variation", Description: "Turn one of the authenticated user's own standalone recipes into a variation of another recipe (any author), found via search_recipes. Fails if the recipe is already a variation, already has its own variations, the target is itself a variation, categories don't match, or it would create a recipe that references its own family. Irreversible through this API."}, result.linkVariation)
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 90 << 20, PropagateRequestCancellation: true,
	})
	protected := auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{})(stream)
	result.handler = cors(protocolOnly(protected))
	return result, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Protocol-Version")
		w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func protocolOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if version := r.Header.Get("Mcp-Protocol-Version"); version != "" && version != protocolVersion {
			http.Error(w, "unsupported MCP protocol version", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func userWithScope(req *mcp.CallToolRequest, scope string) (string, error) {
	var info *auth.TokenInfo
	if req != nil && req.Extra != nil {
		info = req.Extra.TokenInfo
	}
	if info == nil || info.UserID == "" {
		return "", errors.New("authentication context is missing")
	}
	if !slices.Contains(info.Scopes, scope) {
		return "", fmt.Errorf("the token does not grant %s", scope)
	}
	return info.UserID, nil
}

func decodeCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(decoded) != 12 {
		return "", errors.New("invalid cursor")
	}
	return primitive.ObjectID(decoded).Hex(), nil
}

func encodeCursor(cursor string) string {
	id, err := primitive.ObjectIDFromHex(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// decodeOffsetCursor/encodeOffsetCursor are decodeCursor/encodeCursor's
// counterpart for search_recipes: an opaque cursor wrapping a plain offset,
// since search reuses the offset-paginated GetRecipeDocuments pipeline
// (which also drives its closeness-to-target time sort - not expressible as
// an id-based keyset cursor) instead of GetRecipesByAuthor's id-based one.
func decodeOffsetCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, errors.New("invalid cursor")
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, errors.New("invalid cursor")
	}
	return offset, nil
}

func encodeOffsetCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func (s *Server) stepOutputs(steps []models.Step) []StepOutput {
	outputs := make([]StepOutput, 0, len(steps))
	for _, step := range steps {
		output := StepOutput{Title: step.Title, Description: step.Description}
		if step.Picture != "" {
			output.Picture = s.pictureBase + step.Picture
		}
		outputs = append(outputs, output)
	}
	return outputs
}

func (s *Server) recipeOutput(recipe models.Recipe) RecipeOutput {
	output := RecipeOutput{
		Title: recipe.Title, Description: recipe.Description, Quantity: recipe.Quantity, Kind: recipe.Kind, Category: recipe.Category,
		PreparationTime: recipe.PreparationTime, CookingTime: recipe.CookingTime, RestingTime: recipe.RestingTime,
		Ingredients: ingredientOutputs(recipe.Ingredients), Steps: s.stepOutputs(recipe.Steps), SourceLocale: recipe.SourceLocale, Locale: recipe.Locale,
		Pictures: []PictureOutput{}, VariationCount: recipe.VariationCount,
	}
	if recipe.Id != nil {
		output.ID = recipe.Id.Hex()
	}
	if recipe.Author != nil && recipe.Author.Id != nil {
		output.Author = &AuthorOutput{ID: recipe.Author.Id.Hex(), Username: recipe.Author.Username}
	}
	if recipe.VariationOf != nil {
		output.VariationOf = recipe.VariationOf.Hex()
	}
	for _, picture := range recipe.Pictures {
		output.Pictures = append(output.Pictures, PictureOutput{ID: picture.Filename, URL: s.pictureBase + picture.Filename})
	}
	return output
}

func (s *Server) list(ctx context.Context, req *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, ListOutput, error) {
	userID, err := userWithScope(req, "recipes:read")
	if err != nil {
		return nil, ListOutput{}, err
	}
	cursor, err := decodeCursor(input.Cursor)
	if err != nil {
		return nil, ListOutput{}, err
	}
	items, total, next, err := s.recipes.ListDetailedForUser(ctx, userID, cursor, input.Limit, input.Locale)
	if err != nil {
		return nil, ListOutput{}, err
	}
	output := ListOutput{Total: total, NextCursor: encodeCursor(next)}
	for _, item := range items {
		output.Items = append(output.Items, s.recipeOutput(item))
	}
	return nil, output, nil
}

func (s *Server) get(ctx context.Context, req *mcp.CallToolRequest, input GetInput) (*mcp.CallToolResult, RecipeOutput, error) {
	userID, err := userWithScope(req, "recipes:read")
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	recipe, err := s.recipes.GetForUser(ctx, input.RecipeID, userID, input.Locale)
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	output := s.recipeOutput(recipe)
	data, _ := json.Marshal(output)
	content := make([]mcp.Content, 0, len(output.Pictures)+1)
	content = append(content, &mcp.TextContent{Text: string(data)})
	for _, picture := range output.Pictures {
		content = append(content, &mcp.ResourceLink{URI: picture.URL, Name: picture.ID, Title: recipe.Title + " picture", MIMEType: mediaTypeFromID(picture.ID)})
	}
	return &mcp.CallToolResult{Content: content}, output, nil
}

func mediaTypeFromID(id string) string {
	if strings.HasSuffix(strings.ToLower(id), ".png") {
		return "image/png"
	}
	return "image/jpeg"
}

func (s *Server) create(ctx context.Context, req *mcp.CallToolRequest, input CreateInput) (*mcp.CallToolResult, RecipeOutput, error) {
	userID, err := userWithScope(req, "recipes:write")
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	var variationOf *primitive.ObjectID
	if input.VariationOf != "" {
		id, err := primitive.ObjectIDFromHex(input.VariationOf)
		if err != nil {
			return nil, RecipeOutput{}, fmt.Errorf("invalid variation_of: %w", err)
		}
		variationOf = &id
	}
	recipe, err := s.recipes.Create(ctx, userID, models.CreateRecipe{
		Title: input.Title, Description: input.Description, Quantity: input.Quantity, Kind: input.Kind, Category: input.Category,
		PreparationTime: input.PreparationTime, CookingTime: input.CookingTime, RestingTime: input.RestingTime,
		Ingredients: ingredientsFromInput(input.Ingredients), Steps: input.Steps, SourceLocale: input.Locale,
		VariationOf: variationOf,
	}, nil, nil)
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	return nil, s.recipeOutput(recipe), nil
}

func (s *Server) search(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, ListOutput, error) {
	if _, err := userWithScope(req, "recipes:read"); err != nil {
		return nil, ListOutput{}, err
	}
	offset, err := decodeOffsetCursor(input.Cursor)
	if err != nil {
		return nil, ListOutput{}, err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	items, total, nextOffset, hasNext, err := s.recipes.Search(ctx, models.GetRecipesRequest{
		Title: input.Title, Kind: input.Kind, Category: input.Category, Author: input.Author,
		Ingredients: input.Ingredients, PreparationTime: input.PreparationTime, TotalTime: input.TotalTime,
		VariationOf: input.VariationOf, Locale: input.Locale,
	}, offset, limit)
	if err != nil {
		return nil, ListOutput{}, err
	}
	output := ListOutput{Total: total}
	if hasNext {
		output.NextCursor = encodeOffsetCursor(nextOffset)
	}
	for _, item := range items {
		output.Items = append(output.Items, s.recipeOutput(item))
	}
	return nil, output, nil
}

func (s *Server) update(ctx context.Context, req *mcp.CallToolRequest, input UpdateInput) (*mcp.CallToolResult, RecipeOutput, error) {
	userID, err := userWithScope(req, "recipes:write")
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	canonical, err := s.recipes.GetForUser(ctx, input.RecipeID, userID, "")
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	var ingredients *[]models.Ingredient
	if input.Ingredients != nil {
		converted := ingredientsFromInput(*input.Ingredients)
		ingredients = &converted
	}
	updated, err := s.recipes.Update(ctx, canonical, models.UpdateRecipeRequest{
		Title: input.Title, Description: input.Description, Quantity: input.Quantity, Kind: input.Kind, Category: input.Category,
		PreparationTime: input.PreparationTime, CookingTime: input.CookingTime, RestingTime: input.RestingTime,
		Ingredients: ingredients, Steps: input.Steps, Locale: input.Locale, KeepPictureIDs: input.KeepPictureIDs,
	}, nil, nil, false)
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	return nil, s.recipeOutput(updated), nil
}

// linkVariation backs link_recipe_variation. RecipeID's ownership is checked
// via GetForUser first (mirroring update's canonical fetch) since
// recipe_service.LinkVariation itself, like Get, doesn't scope by caller -
// REST enforces that through RecipeAuthorMiddleware instead, which MCP has
// no equivalent of.
func (s *Server) linkVariation(ctx context.Context, req *mcp.CallToolRequest, input LinkVariationInput) (*mcp.CallToolResult, RecipeOutput, error) {
	userID, err := userWithScope(req, "recipes:write")
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	if _, err := s.recipes.GetForUser(ctx, input.RecipeID, userID, ""); err != nil {
		return nil, RecipeOutput{}, err
	}
	updated, err := s.recipes.LinkVariation(ctx, input.RecipeID, input.VariationOf)
	if err != nil {
		return nil, RecipeOutput{}, err
	}
	return nil, s.recipeOutput(updated), nil
}
