package recipe_service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/text/language"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/images_manager"
	"recipes/internal/models"
	"recipes/internal/utils"
)

const (
	maxTitleLength           = 120
	maxDescriptionLength     = 5000
	maxIngredientFieldLength = 200
	maxStepTitleLength       = 200
	maxStepDescriptionLength = 5000

	// translateConcurrency caps how many recipes are being translated at once
	// across all detached write-path work.
	translateConcurrency = 4

	// PictureCapPerContributor bounds how many pictures one non-author user
	// can add to a single recipe.
	PictureCapPerContributor = 3
)

// translateBackoffs is the wait before each attempt when translating one locale;
// its length is the attempt count.
var translateBackoffs = []time.Duration{time.Second, 4 * time.Second, 10 * time.Second}

var (
	ErrInvalid           = errors.New("invalid recipe")
	ErrNotFound          = errors.New("recipe not found")
	ErrHasFavorites      = errors.New("recipe has favorites")
	ErrRecipeReferenced  = errors.New("recipe is referenced by other recipes")
	ErrPictureNotFound   = errors.New("picture not found")
	ErrPictureCapReached = errors.New("picture cap reached for this contributor")
	ErrForbidden         = errors.New("forbidden")
	// ErrAlreadyVariation is LinkVariation's guard against a source recipe
	// that's already a variation of something - only a standalone recipe
	// (VariationOf == nil) can be linked; re-parenting an existing variation
	// is out of scope (see DetachVariation for the one existing way back to
	// standalone).
	ErrAlreadyVariation = errors.New("recipe is already a variation")
	// ErrRecipeHasVariations is LinkVariation's guard against a source recipe
	// that already has its own variations - linking it would require
	// promoting/repointing a whole subtree, which this operation doesn't do.
	ErrRecipeHasVariations = errors.New("recipe already has its own variations")
	// ErrTargetIsVariation is LinkVariation's guard requiring the target to
	// be a genuine root - unlike Create's variation_of, this never
	// auto-flattens to the target's own root.
	ErrTargetIsVariation = errors.New("target recipe is itself a variation")
	// ErrCategoryMismatch is LinkVariation's guard requiring the source and
	// target to share the same category (food/diy).
	ErrCategoryMismatch = errors.New("recipe and target must share the same category")
	// ErrReferenceLoop is LinkVariation's guard against linking a recipe that
	// would make some family member's ingredient reference its own family -
	// either because the source already references the target, or because
	// something in the target's family already references the source.
	ErrReferenceLoop = errors.New("linking would create a recipe that references its own family")
	// ErrNotVariation is DetachVariation's guard: only an actual variation
	// (VariationOf != nil) can be detached back to standalone.
	ErrNotVariation = errors.New("recipe is not a variation")
)

// defaultCategory normalizes a possibly-legacy empty Category to Food,
// matching buildRecipeFilterPipeline's treatment of documents that predate
// the category field.
func defaultCategory(category models.RecipeCategory) models.RecipeCategory {
	if category == "" {
		return models.Food
	}
	return category
}

type PictureUpload struct {
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Data      []byte `json:"data"`
}

// Store is the slice of database.Database the service needs; database.Database
// satisfies it. Keeping it narrow lets tests fake the parts under exercise.
type Store interface {
	CreateRecipe(authorID string, infos *models.CreateRecipe) (models.Recipe, error)
	ReplaceRecipeById(ctx context.Context, id string, recipe models.RecipeDB) (models.Recipe, error)
	GetRecipeById(id string) (models.Recipe, error)
	GetRecipeByIdForAuthor(ctx context.Context, id, authorID string) (models.Recipe, error)
	GetRecipeByIdLocale(id string, locale string) (models.Recipe, error)
	GetRecipeDocuments(parameters models.GetRecipesRequest) ([]models.Recipe, int64, error)
	GetRecipeDocumentsBoosted(ctx context.Context, userID string, parameters models.GetRecipesRequest) (favorited, rest []models.Recipe, total int64, err error)
	GetRecipesByAuthor(ctx context.Context, authorID, cursor string, limit int) ([]models.Recipe, int64, error)
	GetTranslationsByRecipeIDs(ctx context.Context, ids []string, locale string) (map[string]models.Recipe, error)
	AddLocaleRecipe(recipe models.Recipe, locale string) (models.Recipe, error)
	DeleteRecipeById(id string) (models.RecipeDB, error)
	DeleteLocalizedRecipesByID(ctx context.Context, id string) error
	GetOldestVariationID(ctx context.Context, rootID string) (*primitive.ObjectID, error)
	RepointVariations(ctx context.Context, oldRootID, newRootID string) error
	PromoteRecipeToRoot(ctx context.Context, id string) error
	SetVariationOf(ctx context.Context, recipeID, rootID string) error
	GetVariationCounts(ctx context.Context, rootIDs []string) (map[string]int64, error)
	RepointRecipeReferences(ctx context.Context, oldRootID, newRootID string) error
	HasIncomingReferences(ctx context.Context, recipeID string) (bool, error)
	FamilyReferencesRecipe(ctx context.Context, familyRootID, targetID string) (bool, error)
	GetRecipeTitles(ctx context.Context, ids []string) (map[string]string, error)
	GetUsersByIDs(ctx context.Context, ids []string) (map[string]models.UserView, error)

	AddFavorite(ctx context.Context, userID, recipeID string) error
	RemoveFavorite(ctx context.Context, userID, recipeID string) error
	DeleteFavoritesByRecipeID(ctx context.Context, recipeID string) error
	GetFavoriteInfo(ctx context.Context, ids []string, userID string) (map[string]models.FavoriteInfo, error)
	GetFamilyFavoriteInfo(ctx context.Context, rootIDs []string, userID string) (map[string]models.FavoriteInfo, error)
}

// Translator is the slice of *translator.Translator the service needs. A nil
// Translator disables source-locale detection and translate-on-write.
type Translator interface {
	TranslateRecipe(recipe models.Recipe, to string) (models.Recipe, error)
	GetRecipeLocale(recipe models.Recipe) (string, error)
}

type Service struct {
	db              Store
	translator      Translator
	imageDir        string
	maxPictureBytes int
	targetLocales   []string
	translateSlots  chan struct{}
}

func New(db Store, translator Translator, imageDir string, maxPictureBytes int, targetLocales []string) (*Service, error) {
	return &Service{
		db:              db,
		translator:      translator,
		imageDir:        imageDir,
		maxPictureBytes: maxPictureBytes,
		targetLocales:   targetLocales,
		translateSlots:  make(chan struct{}, translateConcurrency),
	}, nil
}

// canTranslate reports whether translate-on-write is configured.
func (s *Service) canTranslate() bool {
	return s.translator != nil && len(s.targetLocales) > 0
}

func normalizeLocale(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	tag, err := language.Parse(value)
	if err != nil || tag == language.Und {
		return "", fmt.Errorf("%w: invalid locale", ErrInvalid)
	}
	return tag.String(), nil
}

func validateRecipe(recipe *models.RecipeDB) error {
	recipe.Title = strings.TrimSpace(recipe.Title)
	recipe.Description = strings.TrimSpace(recipe.Description)
	if recipe.Title == "" || len([]rune(recipe.Title)) > maxTitleLength {
		return fmt.Errorf("%w: title is required and must not exceed %d characters", ErrInvalid, maxTitleLength)
	}
	if len([]rune(recipe.Description)) > maxDescriptionLength {
		return fmt.Errorf("%w: description must not exceed %d characters", ErrInvalid, maxDescriptionLength)
	}
	if recipe.Quantity < 1 {
		return fmt.Errorf("%w: quantity must be at least 1", ErrInvalid)
	}
	if recipe.Category == "" {
		recipe.Category = models.Food
	}
	if !slices.Contains(models.RecipeCategories, recipe.Category) {
		return fmt.Errorf("%w: invalid recipe category", ErrInvalid)
	}
	if recipe.Category != models.Diy && !slices.Contains(models.RecipeKinds, recipe.Kind) {
		return fmt.Errorf("%w: invalid recipe kind", ErrInvalid)
	}
	if recipe.PreparationTime < 0 || recipe.CookingTime < 0 || recipe.RestingTime < 0 {
		return fmt.Errorf("%w: recipe times cannot be negative", ErrInvalid)
	}
	if len(recipe.Ingredients) == 0 {
		return fmt.Errorf("%w: at least one ingredient is required", ErrInvalid)
	}
	seenLabels := make(map[string]string)
	for i := range recipe.Ingredients {
		ingredient := &recipe.Ingredients[i]
		ingredient.Name = strings.TrimSpace(ingredient.Name)
		ingredient.Unit = strings.TrimSpace(ingredient.Unit)
		ingredient.Label = strings.TrimSpace(ingredient.Label)
		ingredient.RefLabel = strings.TrimSpace(ingredient.RefLabel)
		if ingredient.RecipeRef != nil {
			// A reference ingredient carries no free-text name - see the
			// RecipeRef doc comment on models.Ingredient.
			ingredient.Name = ""
		} else {
			ingredient.RefLabel = ""
		}
		if (ingredient.RecipeRef == nil && ingredient.Name == "") ||
			len([]rune(ingredient.Name)) > maxIngredientFieldLength ||
			len([]rune(ingredient.Unit)) > maxIngredientFieldLength || len([]rune(ingredient.Label)) > maxIngredientFieldLength ||
			len([]rune(ingredient.RefLabel)) > maxIngredientFieldLength {
			return fmt.Errorf("%w: invalid ingredient at index %d", ErrInvalid, i)
		}
		if ingredient.Quantity < 0 {
			return fmt.Errorf("%w: ingredient quantity cannot be negative", ErrInvalid)
		}
		if ingredient.Label != "" {
			key := strings.ToLower(ingredient.Label)
			if canonical, ok := seenLabels[key]; ok {
				ingredient.Label = canonical
			} else {
				seenLabels[key] = ingredient.Label
			}
		}
	}
	if len(recipe.Steps) == 0 {
		return fmt.Errorf("%w: at least one step is required", ErrInvalid)
	}
	for i := range recipe.Steps {
		step := &recipe.Steps[i]
		step.Title = strings.TrimSpace(step.Title)
		step.Description = strings.TrimSpace(step.Description)
		if step.Description == "" || len([]rune(step.Title)) > maxStepTitleLength || len([]rune(step.Description)) > maxStepDescriptionLength {
			return fmt.Errorf("%w: invalid step at index %d", ErrInvalid, i)
		}
	}
	locale, err := normalizeLocale(recipe.SourceLocale)
	if err != nil {
		return err
	}
	recipe.SourceLocale = locale
	return nil
}

func sourceHash(recipe models.RecipeDB) string {
	recipe.Id = nil
	recipe.Author = nil
	recipe.Locale = ""
	recipe.SourceHash = ""
	recipe.VariationOf = nil
	// Pictures are structural, not translatable content (like VariationOf
	// above): excluding them keeps a picture add/remove from invalidating an
	// otherwise-current stored translation - see pickTranslation, which
	// likewise always takes Pictures from the canonical recipe.
	recipe.Pictures = nil
	data, _ := json.Marshal(recipe)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// normalizedPicture is an uploaded image after in-memory normalization, with the
// final filename it will take in the image directory. Nothing is written to disk
// until writePictures runs.
type normalizedPicture struct {
	filename string
	data     []byte
}

func (s *Service) normalizePictures(pictures []PictureUpload) ([]normalizedPicture, error) {
	total := 0
	normalized := make([]normalizedPicture, 0, len(pictures))
	for _, picture := range pictures {
		total += len(picture.Data)
		if total > s.maxPictureBytes {
			return nil, fmt.Errorf("%w: pictures exceed the %d byte request limit", ErrInvalid, s.maxPictureBytes)
		}
		image, err := images_manager.NormalizeRecipeImage(picture.Data, picture.Filename, picture.MediaType)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		normalized = append(normalized, normalizedPicture{
			filename: primitive.NewObjectID().Hex() + image.Extension,
			data:     image.Data,
		})
	}
	return normalized, nil
}

// writePictures writes the normalized bytes into the image directory. On the
// first failure it removes what it already wrote and returns the error.
func (s *Service) writePictures(pictures []normalizedPicture) ([]string, error) {
	written := make([]string, 0, len(pictures))
	for _, picture := range pictures {
		if err := images_manager.SaveBytes(s.imageDir, picture.filename, picture.data); err != nil {
			s.removePictures(written)
			return nil, err
		}
		written = append(written, picture.filename)
	}
	return written, nil
}

func (s *Service) removePictures(filenames []string) {
	for _, filename := range filenames {
		_ = images_manager.Remove(s.imageDir, filename)
	}
}

// stepPictureUploads validates that every upload's step index is within
// range and returns the uploads in deterministic (ascending index) order,
// paired with which step index each belongs to.
func (s *Service) stepPictureUploads(uploads map[int]PictureUpload, stepCount int) ([]int, []PictureUpload, error) {
	if len(uploads) == 0 {
		return nil, nil, nil
	}
	indices := make([]int, 0, len(uploads))
	for i := range uploads {
		if i < 0 || i >= stepCount {
			return nil, nil, fmt.Errorf("%w: step picture index %d is out of range", ErrInvalid, i)
		}
		indices = append(indices, i)
	}
	slices.Sort(indices)
	result := make([]PictureUpload, len(indices))
	for j, i := range indices {
		result[j] = uploads[i]
	}
	return indices, result, nil
}

// normalizeAndWriteAllPictures normalizes recipe-level pictures and step
// pictures together (so s.maxPictureBytes caps their combined size) and
// writes them all in one batch, so a failure partway through cleans up
// everything already written. It returns the recipe-level filenames and,
// separately, the step index/filename pairs to assign onto steps.
func (s *Service) normalizeAndWriteAllPictures(pictures []PictureUpload, stepUploads []PictureUpload, stepIndices []int) ([]string, map[int]string, error) {
	combined := make([]PictureUpload, 0, len(pictures)+len(stepUploads))
	combined = append(combined, pictures...)
	combined = append(combined, stepUploads...)
	normalized, err := s.normalizePictures(combined)
	if err != nil {
		return nil, nil, err
	}
	written, err := s.writePictures(normalized)
	if err != nil {
		return nil, nil, err
	}
	recipeFilenames := written[:len(pictures)]
	stepFilenames := make(map[int]string, len(stepIndices))
	for j, filename := range written[len(pictures):] {
		stepFilenames[stepIndices[j]] = filename
	}
	return recipeFilenames, stepFilenames, nil
}

// rootCategoryOf fetches rootID's Category, defaulted like defaultCategory.
// Used to enforce that a variation's category always matches its root's.
func (s *Service) rootCategoryOf(rootID *primitive.ObjectID) (models.RecipeCategory, error) {
	root, err := s.db.GetRecipeById(rootID.Hex())
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return "", ErrNotFound
		}
		return "", err
	}
	return defaultCategory(root.Category), nil
}

func (s *Service) Create(ctx context.Context, authorID string, input models.CreateRecipe, pictures []PictureUpload, stepPictures map[int]PictureUpload) (models.Recipe, error) {
	locale, err := normalizeLocale(input.SourceLocale)
	if err != nil {
		return models.Recipe{}, err
	}
	input.SourceLocale = locale
	// Nothing exists yet on create: a step's picture can only come from a
	// fresh upload correlated by index below, never a client-supplied value.
	for i := range input.Steps {
		input.Steps[i].Picture = ""
	}
	recipeDB := models.RecipeDB{
		Title: input.Title, Description: input.Description, Quantity: input.Quantity, Kind: input.Kind, Category: input.Category,
		PreparationTime: input.PreparationTime, CookingTime: input.CookingTime, RestingTime: input.RestingTime,
		Ingredients: input.Ingredients, Steps: input.Steps, SourceLocale: input.SourceLocale,
	}
	var variationRootCategory models.RecipeCategory
	if input.VariationOf != nil {
		target, err := s.db.GetRecipeById(input.VariationOf.Hex())
		if err != nil {
			if errors.Is(err, mongorepo.NotFoundError) {
				return models.Recipe{}, ErrNotFound
			}
			return models.Recipe{}, err
		}
		root := target.Id
		variationRootCategory = defaultCategory(target.Category)
		// Always flatten to the target's own root - a variation never chains
		// off another variation.
		if target.VariationOf != nil {
			root = target.VariationOf
			variationRootCategory, err = s.rootCategoryOf(root)
			if err != nil {
				return models.Recipe{}, err
			}
		}
		recipeDB.VariationOf = root
	}
	if recipeDB.SourceLocale == "" && s.translator != nil {
		detected, detectErr := s.translator.GetRecipeLocale(models.Recipe{Ingredients: recipeDB.Ingredients})
		if detectErr == nil {
			recipeDB.SourceLocale, _ = normalizeLocale(detected)
		}
	}
	if err := validateRecipe(&recipeDB); err != nil {
		return models.Recipe{}, err
	}
	if recipeDB.VariationOf != nil && recipeDB.Category != variationRootCategory {
		return models.Recipe{}, fmt.Errorf("%w: a variation's category must match its root recipe's category", ErrInvalid)
	}
	stepIndices, stepUploads, err := s.stepPictureUploads(stepPictures, len(recipeDB.Steps))
	if err != nil {
		return models.Recipe{}, err
	}
	recipeFilenames, stepFilenames, err := s.normalizeAndWriteAllPictures(pictures, stepUploads, stepIndices)
	if err != nil {
		return models.Recipe{}, err
	}
	recipeDB.Pictures = append(recipeDB.Pictures, authorPictures(recipeFilenames)...)
	for index, filename := range stepFilenames {
		recipeDB.Steps[index].Picture = filename
	}
	recipeDB.SourceHash = sourceHash(recipeDB)
	create := utils.DupStruct[models.CreateRecipe](&recipeDB)
	created, err := s.db.CreateRecipe(authorID, &create)
	if err != nil {
		s.removePictures(append(recipeFilenames, mapValues(stepFilenames)...))
		return models.Recipe{}, err
	}
	s.scheduleTranslations(created)
	created.Locale = created.SourceLocale
	s.decoratePictureContributors(ctx, &created)
	return created, nil
}

// mapValues returns m's values in unspecified order.
func mapValues[K comparable, V any](m map[K]V) []V {
	values := make([]V, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}

// authorPictures wraps freshly-uploaded filenames as author-owned picture
// entries (AddedBy nil) - the only kind Create/Update ever add directly.
func authorPictures(filenames []string) []models.RecipePicture {
	pictures := make([]models.RecipePicture, 0, len(filenames))
	for _, filename := range filenames {
		pictures = append(pictures, models.RecipePicture{Filename: filename})
	}
	return pictures
}

// pictureFilenames extracts just the filenames, in order, for callers (like
// preview projection) that don't need attribution.
func pictureFilenames(pictures []models.RecipePicture) []string {
	if len(pictures) == 0 {
		return nil
	}
	filenames := make([]string, len(pictures))
	for i, picture := range pictures {
		filenames[i] = picture.Filename
	}
	return filenames
}

// regroupPictures stable-partitions pictures into the recipe author's own
// (AddedBy nil) first, followed by every contributor's, preserving each
// group's relative order - the invariant "the author's pictures are always
// first" that every write path (Create/Update/AddPicture/RemovePicture)
// re-establishes rather than relying on callers to maintain it.
func regroupPictures(pictures []models.RecipePicture) []models.RecipePicture {
	regrouped := make([]models.RecipePicture, 0, len(pictures))
	for _, picture := range pictures {
		if picture.AddedBy == nil {
			regrouped = append(regrouped, picture)
		}
	}
	for _, picture := range pictures {
		if picture.AddedBy != nil {
			regrouped = append(regrouped, picture)
		}
	}
	return regrouped
}

// mergePatch applies patch onto recipe. freshStepPictures holds the step
// indices that have a new picture upload pending in this request: their
// patch-supplied Picture value is ignored (it will be overwritten with the
// upload's filename once normalized) rather than validated as a keep.
// fullPictureAccess is false for the recipe's own author, editing through
// the normal author flow: patch.KeepPictureIDs may only reference the
// author's own pictures (AddedBy nil), and any contributor picture is
// preserved untouched regardless of whether it's listed - a stale save
// can never silently drop someone else's photo. fullPictureAccess is true
// only for an admin editing a recipe they don't own (see UpdateRecipe):
// patch.KeepPictureIDs may then reference and drop any picture, author's or
// contributor's, since that's a deliberate moderation action.
func mergePatch(recipe models.Recipe, patch models.UpdateRecipeRequest, freshStepPictures map[int]bool, fullPictureAccess bool) (models.RecipeDB, error) {
	merged := recipe.ToRecipeDB()
	merged.Locale = ""
	if patch.Title != nil {
		merged.Title = *patch.Title
	}
	if patch.Description != nil {
		merged.Description = *patch.Description
	}
	if patch.Quantity != nil {
		merged.Quantity = *patch.Quantity
	}
	if patch.Kind != nil {
		merged.Kind = *patch.Kind
	}
	if patch.Category != nil {
		merged.Category = *patch.Category
	}
	if patch.PreparationTime != nil {
		merged.PreparationTime = *patch.PreparationTime
	}
	if patch.CookingTime != nil {
		merged.CookingTime = *patch.CookingTime
	}
	if patch.RestingTime != nil {
		merged.RestingTime = *patch.RestingTime
	}
	if patch.Ingredients != nil {
		merged.Ingredients = slices.Clone(*patch.Ingredients)
	}
	if patch.Steps != nil {
		merged.Steps = slices.Clone(*patch.Steps)
		available := make(map[string]bool, len(recipe.Steps))
		for _, step := range recipe.Steps {
			if step.Picture != "" {
				available[step.Picture] = true
			}
		}
		for i := range merged.Steps {
			if freshStepPictures[i] {
				merged.Steps[i].Picture = ""
				continue
			}
			if merged.Steps[i].Picture != "" && !available[merged.Steps[i].Picture] {
				return models.RecipeDB{}, fmt.Errorf("%w: step %d references an unknown picture", ErrInvalid, i)
			}
		}
	}
	if patch.Locale != nil {
		merged.SourceLocale = *patch.Locale
	}
	if patch.KeepPictureIDs != nil {
		available := make(map[string]models.RecipePicture, len(recipe.Pictures))
		for _, picture := range recipe.Pictures {
			available[picture.Filename] = picture
		}
		seen := make(map[string]bool, len(*patch.KeepPictureIDs))
		merged.Pictures = nil
		for _, filename := range *patch.KeepPictureIDs {
			picture, ok := available[filename]
			if !ok || seen[filename] {
				return models.RecipeDB{}, fmt.Errorf("%w: invalid or duplicate keep_picture_ids entry", ErrInvalid)
			}
			if !fullPictureAccess && picture.AddedBy != nil {
				return models.RecipeDB{}, fmt.Errorf("%w: keep_picture_ids cannot reference a contributor's picture", ErrInvalid)
			}
			seen[filename] = true
			merged.Pictures = append(merged.Pictures, picture)
		}
		if !fullPictureAccess {
			// Every contributor picture survives untouched, regardless of
			// whether it was listed - see the fullPictureAccess doc comment.
			for _, picture := range recipe.Pictures {
				if picture.AddedBy != nil {
					merged.Pictures = append(merged.Pictures, picture)
				}
			}
		}
	}
	return merged, nil
}

func (s *Service) Update(ctx context.Context, recipe models.Recipe, patch models.UpdateRecipeRequest, pictures []PictureUpload, stepPictures map[int]PictureUpload, fullPictureAccess bool) (models.Recipe, error) {
	freshStepPictures := make(map[int]bool, len(stepPictures))
	for i := range stepPictures {
		freshStepPictures[i] = true
	}
	merged, err := mergePatch(recipe, patch, freshStepPictures, fullPictureAccess)
	if err != nil {
		return models.Recipe{}, err
	}
	if err := validateRecipe(&merged); err != nil {
		return models.Recipe{}, err
	}
	if merged.VariationOf != nil {
		rootCategory, err := s.rootCategoryOf(merged.VariationOf)
		if err != nil {
			return models.Recipe{}, err
		}
		if merged.Category != rootCategory {
			return models.Recipe{}, fmt.Errorf("%w: a variation's category must match its root recipe's category", ErrInvalid)
		}
	}
	stepIndices, stepUploads, err := s.stepPictureUploads(stepPictures, len(merged.Steps))
	if err != nil {
		return models.Recipe{}, err
	}
	recipeFilenames, stepFilenames, err := s.normalizeAndWriteAllPictures(pictures, stepUploads, stepIndices)
	if err != nil {
		return models.Recipe{}, err
	}
	merged.Pictures = regroupPictures(append(merged.Pictures, authorPictures(recipeFilenames)...))
	for index, filename := range stepFilenames {
		merged.Steps[index].Picture = filename
	}
	merged.SourceHash = sourceHash(merged)
	updated, err := s.db.ReplaceRecipeById(ctx, recipe.Id.Hex(), merged)
	if err != nil {
		s.removePictures(append(recipeFilenames, mapValues(stepFilenames)...))
		return models.Recipe{}, err
	}
	kept := make(map[string]bool, len(updated.Pictures))
	for _, picture := range updated.Pictures {
		kept[picture.Filename] = true
	}
	for _, picture := range recipe.Pictures {
		if !kept[picture.Filename] {
			_ = images_manager.Remove(s.imageDir, picture.Filename)
		}
	}
	keptSteps := make(map[string]bool, len(updated.Steps))
	for _, step := range updated.Steps {
		if step.Picture != "" {
			keptSteps[step.Picture] = true
		}
	}
	for _, step := range recipe.Steps {
		if step.Picture != "" && !keptSteps[step.Picture] {
			_ = images_manager.Remove(s.imageDir, step.Picture)
		}
	}
	_ = s.db.DeleteLocalizedRecipesByID(ctx, recipe.Id.Hex())
	s.scheduleTranslations(updated)
	updated.Locale = updated.SourceLocale
	s.decoratePictureContributors(ctx, &updated)
	return updated, nil
}

// decoratePictureContributors populates PictureOutputs from Pictures,
// resolving each contributor's AddedBy id to a full UserView in one batched
// lookup (an author-owned picture, AddedBy nil, needs no lookup). A lookup
// failure is logged and leaves that picture's AddedBy unresolved (nil, same
// as an author-owned picture) rather than failing the request.
func (s *Service) decoratePictureContributors(ctx context.Context, recipe *models.Recipe) {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, picture := range recipe.Pictures {
		if picture.AddedBy == nil {
			continue
		}
		hex := picture.AddedBy.Hex()
		if _, ok := seen[hex]; ok {
			continue
		}
		seen[hex] = struct{}{}
		ids = append(ids, hex)
	}
	var users map[string]models.UserView
	if len(ids) > 0 {
		var err error
		users, err = s.db.GetUsersByIDs(ctx, ids)
		if err != nil {
			utils.LogError("could not load picture contributors", err)
		}
	}
	outputs := make([]models.PictureOutput, 0, len(recipe.Pictures))
	for _, picture := range recipe.Pictures {
		output := models.PictureOutput{Filename: picture.Filename}
		if picture.AddedBy != nil {
			if user, ok := users[picture.AddedBy.Hex()]; ok {
				output.AddedBy = &user
			}
		}
		outputs = append(outputs, output)
	}
	recipe.PictureOutputs = outputs
}

// AddPicture adds one picture to recipe on behalf of userID. When userID is
// not the recipe's author, the picture is attributed to them (a
// contributor's picture) and counted against PictureCapPerContributor; the
// recipe's own author has no cap. Like RemovePicture, this bypasses
// SourceHash/translations entirely - pictures are structural, not
// translatable content (see sourceHash, pickTranslation).
func (s *Service) AddPicture(ctx context.Context, recipe models.Recipe, userID string, upload PictureUpload) (models.Recipe, error) {
	isAuthor := recipe.Author != nil && recipe.Author.Id != nil && recipe.Author.Id.Hex() == userID
	var addedBy *primitive.ObjectID
	if !isAuthor {
		count := 0
		for _, picture := range recipe.Pictures {
			if picture.AddedBy != nil && picture.AddedBy.Hex() == userID {
				count++
			}
		}
		if count >= PictureCapPerContributor {
			return models.Recipe{}, ErrPictureCapReached
		}
		objectID, err := primitive.ObjectIDFromHex(userID)
		if err != nil {
			return models.Recipe{}, fmt.Errorf("%w: invalid user id", ErrInvalid)
		}
		addedBy = &objectID
	}
	normalized, err := s.normalizePictures([]PictureUpload{upload})
	if err != nil {
		return models.Recipe{}, err
	}
	written, err := s.writePictures(normalized)
	if err != nil {
		return models.Recipe{}, err
	}
	merged := recipe.ToRecipeDB()
	merged.Locale = ""
	merged.Pictures = regroupPictures(append(slices.Clone(merged.Pictures), models.RecipePicture{Filename: written[0], AddedBy: addedBy}))
	updated, err := s.db.ReplaceRecipeById(ctx, recipe.Id.Hex(), merged)
	if err != nil {
		s.removePictures(written)
		return models.Recipe{}, err
	}
	updated.Locale = updated.SourceLocale
	s.decoratePictureContributors(ctx, &updated)
	return updated, nil
}

// RemovePicture removes one picture from recipe. Allowed when callerID is
// the recipe's author, callerIsAdmin is set (an admin moderating a recipe
// they don't own), or callerID is that picture's own contributor.
func (s *Service) RemovePicture(ctx context.Context, recipe models.Recipe, filename, callerID string, callerIsAdmin bool) (models.Recipe, error) {
	index := -1
	for i, picture := range recipe.Pictures {
		if picture.Filename == filename {
			index = i
			break
		}
	}
	if index == -1 {
		return models.Recipe{}, ErrPictureNotFound
	}
	picture := recipe.Pictures[index]
	isAuthor := recipe.Author != nil && recipe.Author.Id != nil && recipe.Author.Id.Hex() == callerID
	isOwnPicture := picture.AddedBy != nil && picture.AddedBy.Hex() == callerID
	if !callerIsAdmin && !isAuthor && !isOwnPicture {
		return models.Recipe{}, ErrForbidden
	}
	merged := recipe.ToRecipeDB()
	merged.Locale = ""
	remaining := make([]models.RecipePicture, 0, len(merged.Pictures))
	for _, p := range merged.Pictures {
		if p.Filename != filename {
			remaining = append(remaining, p)
		}
	}
	merged.Pictures = remaining
	updated, err := s.db.ReplaceRecipeById(ctx, recipe.Id.Hex(), merged)
	if err != nil {
		return models.Recipe{}, err
	}
	_ = images_manager.Remove(s.imageDir, filename)
	updated.Locale = updated.SourceLocale
	s.decoratePictureContributors(ctx, &updated)
	return updated, nil
}

func (s *Service) GetForUser(ctx context.Context, recipeID, userID, locale string) (models.Recipe, error) {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return models.Recipe{}, ErrNotFound
	}
	locale, err := normalizeLocale(locale)
	if err != nil {
		return models.Recipe{}, err
	}
	recipe, err := s.db.GetRecipeByIdForAuthor(ctx, recipeID, userID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return models.Recipe{}, ErrNotFound
		}
		return models.Recipe{}, err
	}
	localized := s.localize(recipe, locale)
	// GetForUser is an internal author-scoped fetch (MCP), not a
	// social/discovery view, so it deliberately skips favorite decoration
	// (like Get does not) - but variation_count is still cheap and useful
	// for an MCP client managing its own recipes, so it's decorated here.
	s.decorateVariationCount(ctx, &localized)
	s.decorateRefTitle(ctx, &localized)
	return localized, nil
}

func (s *Service) Get(ctx context.Context, recipeID, locale, userID string) (models.Recipe, error) {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return models.Recipe{}, ErrNotFound
	}
	locale, err := normalizeLocale(locale)
	if err != nil {
		return models.Recipe{}, err
	}
	recipe, err := s.db.GetRecipeById(recipeID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return models.Recipe{}, ErrNotFound
		}
		return models.Recipe{}, err
	}
	localized := s.localize(recipe, locale)
	// A detail page always represents one family, whichever member is being
	// viewed, so favorites are always decorated family-wide here (unlike
	// List, which has an "own recipes" per-recipe mode - see List).
	s.decorateFamilyFavorite(ctx, &localized, userID)
	s.decorateVariationCount(ctx, &localized)
	s.decorateRefTitle(ctx, &localized)
	s.decoratePictureContributors(ctx, &localized)
	return localized, nil
}

// AddFavorite marks recipeID as favorited by userID; a no-op if it already is.
func (s *Service) AddFavorite(ctx context.Context, userID, recipeID string) error {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return ErrNotFound
	}
	if _, err := s.db.GetRecipeById(recipeID); err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return ErrNotFound
		}
		return err
	}
	return s.db.AddFavorite(ctx, userID, recipeID)
}

// RemoveFavorite un-favorites recipeID for userID; a no-op if it wasn't
// favorited (or no longer exists).
func (s *Service) RemoveFavorite(ctx context.Context, userID, recipeID string) error {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return ErrNotFound
	}
	return s.db.RemoveFavorite(ctx, userID, recipeID)
}

// decorateFavorite stamps Favorite/FavoriteCount onto recipe. userID may be
// empty (anonymous request); lookup failures are logged and leave the
// zero-value (not favorited, count 0) rather than failing the request.
func (s *Service) decorateFavorite(ctx context.Context, recipe *models.Recipe, userID string) {
	if recipe.Id == nil {
		return
	}
	info, err := s.db.GetFavoriteInfo(ctx, []string{recipe.Id.Hex()}, userID)
	if err != nil {
		utils.LogError("could not load favorite info", err)
		return
	}
	if fav, ok := info[recipe.Id.Hex()]; ok {
		recipe.Favorite = fav.Favorited
		recipe.FavoriteCount = fav.Count
	}
}

// decorateFavoritePreviews is decorateFavorite for a page of previews, fetched
// in one batched lookup.
func (s *Service) decorateFavoritePreviews(ctx context.Context, previews []models.RecipePreview, userID string) {
	ids := make([]string, 0, len(previews))
	for _, preview := range previews {
		if preview.Id != nil {
			ids = append(ids, preview.Id.Hex())
		}
	}
	if len(ids) == 0 {
		return
	}
	info, err := s.db.GetFavoriteInfo(ctx, ids, userID)
	if err != nil {
		utils.LogError("could not load favorite info", err)
		return
	}
	for i := range previews {
		if previews[i].Id == nil {
			continue
		}
		if fav, ok := info[previews[i].Id.Hex()]; ok {
			previews[i].Favorite = fav.Favorited
			previews[i].FavoriteCount = fav.Count
		}
	}
}

// familyRootHex returns id's own hex when it's a root, or its VariationOf's
// hex when it's a variation - the family root every family-wide decoration
// (favorites, variation count) is keyed by.
func familyRootHex(id, variationOf *primitive.ObjectID) string {
	if variationOf != nil {
		return variationOf.Hex()
	}
	if id != nil {
		return id.Hex()
	}
	return ""
}

// decorateFamilyFavorite is decorateFavorite, but counting distinct people
// who favorited any member of recipe's family (root or any variation), not
// just recipe itself - see decision #6. Used everywhere except "My Recipes"
// (List with OwnRecipes set), which wants each recipe's own individual count.
func (s *Service) decorateFamilyFavorite(ctx context.Context, recipe *models.Recipe, userID string) {
	rootHex := familyRootHex(recipe.Id, recipe.VariationOf)
	if rootHex == "" {
		return
	}
	info, err := s.db.GetFamilyFavoriteInfo(ctx, []string{rootHex}, userID)
	if err != nil {
		utils.LogError("could not load family favorite info", err)
		return
	}
	if fav, ok := info[rootHex]; ok {
		recipe.Favorite = fav.Favorited
		recipe.FavoriteCount = fav.Count
	}
}

// decorateFamilyFavoritePreviews is decorateFamilyFavorite for a page of
// previews, fetched in one batched lookup keyed by family root.
func (s *Service) decorateFamilyFavoritePreviews(ctx context.Context, previews []models.RecipePreview, userID string) {
	rootHexes := make([]string, 0, len(previews))
	seen := make(map[string]struct{}, len(previews))
	for _, preview := range previews {
		rootHex := familyRootHex(preview.Id, preview.VariationOf)
		if rootHex == "" {
			continue
		}
		if _, ok := seen[rootHex]; ok {
			continue
		}
		seen[rootHex] = struct{}{}
		rootHexes = append(rootHexes, rootHex)
	}
	if len(rootHexes) == 0 {
		return
	}
	info, err := s.db.GetFamilyFavoriteInfo(ctx, rootHexes, userID)
	if err != nil {
		utils.LogError("could not load family favorite info", err)
		return
	}
	for i := range previews {
		rootHex := familyRootHex(previews[i].Id, previews[i].VariationOf)
		if fav, ok := info[rootHex]; ok {
			previews[i].Favorite = fav.Favorited
			previews[i].FavoriteCount = fav.Count
		}
	}
}

// decorateVariationCount stamps VariationCount onto recipe; a no-op (count 0)
// when recipe is itself a variation, since VariationCount describes a root's
// family size, not "siblings of this variation".
func (s *Service) decorateVariationCount(ctx context.Context, recipe *models.Recipe) {
	if recipe.Id == nil || recipe.VariationOf != nil {
		return
	}
	counts, err := s.db.GetVariationCounts(ctx, []string{recipe.Id.Hex()})
	if err != nil {
		utils.LogError("could not load variation count", err)
		return
	}
	recipe.VariationCount = counts[recipe.Id.Hex()]
}

// decorateVariationCountsPreviews is decorateVariationCount for a page of
// previews, fetched in one batched lookup.
func (s *Service) decorateVariationCountsPreviews(ctx context.Context, previews []models.RecipePreview) {
	ids := make([]string, 0, len(previews))
	for _, preview := range previews {
		if preview.Id != nil && preview.VariationOf == nil {
			ids = append(ids, preview.Id.Hex())
		}
	}
	if len(ids) == 0 {
		return
	}
	counts, err := s.db.GetVariationCounts(ctx, ids)
	if err != nil {
		utils.LogError("could not load variation counts", err)
		return
	}
	for i := range previews {
		if previews[i].Id != nil && previews[i].VariationOf == nil {
			previews[i].VariationCount = counts[previews[i].Id.Hex()]
		}
	}
}

// decorateVariationCounts is decorateVariationCountsPreviews for a page of
// full recipes rather than previews.
func (s *Service) decorateVariationCounts(ctx context.Context, recipes []models.Recipe) {
	ids := make([]string, 0, len(recipes))
	for _, recipe := range recipes {
		if recipe.Id != nil && recipe.VariationOf == nil {
			ids = append(ids, recipe.Id.Hex())
		}
	}
	if len(ids) == 0 {
		return
	}
	counts, err := s.db.GetVariationCounts(ctx, ids)
	if err != nil {
		utils.LogError("could not load variation counts", err)
		return
	}
	for i := range recipes {
		if recipes[i].Id != nil && recipes[i].VariationOf == nil {
			recipes[i].VariationCount = counts[recipes[i].Id.Hex()]
		}
	}
}

// decorateRefTitles resolves ResolvedRefTitle on every recipe_ref ingredient
// across recipes, fetching every referenced recipe's current title in one
// batched lookup: RefLabel wins when the author set one, else the live title
// (per Ingredient.ResolvedRefTitle's contract). Recipes are mutated in place.
func (s *Service) decorateRefTitles(ctx context.Context, recipes []*models.Recipe) {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, recipe := range recipes {
		for _, ingredient := range recipe.Ingredients {
			if ingredient.RecipeRef == nil || ingredient.RefLabel != "" {
				continue
			}
			hex := ingredient.RecipeRef.Hex()
			if _, ok := seen[hex]; ok {
				continue
			}
			seen[hex] = struct{}{}
			ids = append(ids, hex)
		}
	}
	var titles map[string]string
	if len(ids) > 0 {
		var err error
		titles, err = s.db.GetRecipeTitles(ctx, ids)
		if err != nil {
			utils.LogError("could not load reference titles", err)
		}
	}
	for _, recipe := range recipes {
		for i := range recipe.Ingredients {
			ingredient := &recipe.Ingredients[i]
			if ingredient.RecipeRef == nil {
				continue
			}
			if ingredient.RefLabel != "" {
				ingredient.ResolvedRefTitle = ingredient.RefLabel
				continue
			}
			ingredient.ResolvedRefTitle = titles[ingredient.RecipeRef.Hex()]
		}
	}
}

// decorateRefTitle is decorateRefTitles for a single recipe.
func (s *Service) decorateRefTitle(ctx context.Context, recipe *models.Recipe) {
	s.decorateRefTitles(ctx, []*models.Recipe{recipe})
}

func sameBaseLocale(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	l, _ := language.Parse(left)
	r, _ := language.Parse(right)
	lb, _ := l.Base()
	rb, _ := r.Base()
	return lb == rb
}

// wantsTranslation reports whether a translation row is worth looking up for the
// requested locale: it must be a valid, non-empty locale that differs from the
// recipe's source, and the recipe must carry the id and hash a row is keyed on.
func wantsTranslation(canonical models.Recipe, requested string) bool {
	return requested != "" && canonical.Id != nil && canonical.SourceHash != "" &&
		!sameBaseLocale(canonical.SourceLocale, requested)
}

// pickTranslation returns the stored translation when it matches the canonical's
// source hash, otherwise the canonical recipe. Either way Locale is stamped with
// what is actually being served. VariationOf always comes from canonical: it's a
// structural relationship, not translatable content, and recipe_translations rows
// never store it.
func pickTranslation(canonical, translation models.Recipe, requested string, found bool) models.Recipe {
	if found && translation.SourceHash != "" && translation.SourceHash == canonical.SourceHash {
		translation.Locale = requested
		translation.VariationOf = canonical.VariationOf
		translation.Pictures = canonical.Pictures
		return translation
	}
	canonical.Locale = canonical.SourceLocale
	return canonical
}

// localize serves the stored translation for one recipe, or the canonical recipe
// when none is current. It never calls the translator and never writes.
func (s *Service) localize(canonical models.Recipe, requested string) models.Recipe {
	requested, err := normalizeLocale(requested)
	if err != nil || !wantsTranslation(canonical, requested) {
		canonical.Locale = canonical.SourceLocale
		return canonical
	}
	translation, err := s.db.GetRecipeByIdLocale(canonical.Id.Hex(), requested)
	return pickTranslation(canonical, translation, requested, err == nil)
}

// List returns a page of recipes matching parameters. userID may be empty for
// an anonymous request; when non-empty, results are decorated with per-user
// favorite status, and if parameters.Favorite is set, every recipe userID has
// favorited (matching the filters) is returned first, unpaginated, ahead of a
// normal paginated page of the non-favorited remainder — see
// Store.GetRecipeDocumentsBoosted. If parameters.FavoritesOnly is set instead,
// only recipes userID has favorited are returned (see
// buildRecipeFilterPipeline) — an anonymous request in that mode gets an
// empty page rather than leaking every recipe as "favorited".
func (s *Service) List(ctx context.Context, parameters models.GetRecipesRequest, userID string) ([]models.RecipePreview, int64, error) {
	locale, err := normalizeLocale(parameters.Locale)
	if err != nil {
		return nil, 0, err
	}
	searchLocale, err := normalizeLocale(parameters.SearchLocale)
	if err != nil {
		return nil, 0, err
	}
	parameters.Locale = locale
	parameters.SearchLocale = searchLocale

	var documents []models.Recipe
	var count int64
	if parameters.FavoritesOnly {
		if userID == "" {
			return []models.RecipePreview{}, 0, nil
		}
		parameters.FavoritedByUserID = userID
		documents, count, err = s.db.GetRecipeDocuments(parameters)
		if err != nil {
			return nil, 0, err
		}
	} else if parameters.Favorite && userID != "" {
		favorited, rest, total, err := s.db.GetRecipeDocumentsBoosted(ctx, userID, parameters)
		if err != nil {
			return nil, 0, err
		}
		documents = append(favorited, rest...)
		count = total
	} else {
		documents, count, err = s.db.GetRecipeDocuments(parameters)
		if err != nil {
			return nil, 0, err
		}
	}

	previews := s.localizePreviews(ctx, documents, parameters.Locale)
	if parameters.OwnRecipes || parameters.FavoritesOnly {
		// "My Recipes"/"My Favorites": each entry's own individual favorite
		// count, not the family aggregate (decision #4).
		s.decorateFavoritePreviews(ctx, previews, userID)
	} else {
		s.decorateFamilyFavoritePreviews(ctx, previews, userID)
	}
	s.decorateVariationCountsPreviews(ctx, previews)
	return previews, count, nil
}

func (s *Service) ListForUser(ctx context.Context, userID, cursor string, limit int, locale string) ([]models.RecipePreview, int64, string, error) {
	locale, err := normalizeLocale(locale)
	if err != nil {
		return nil, 0, "", err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	documents, count, err := s.db.GetRecipesByAuthor(ctx, userID, cursor, limit+1)
	if err != nil {
		return nil, 0, "", err
	}
	next := ""
	if len(documents) > limit {
		next = documents[limit-1].Id.Hex()
		documents = documents[:limit]
	}
	previews := s.localizePreviews(ctx, documents, locale)
	s.decorateVariationCountsPreviews(ctx, previews)
	return previews, count, next, nil
}

// ListDetailedForUser is ListForUser but returns full recipes (ingredients,
// steps, ...) instead of previews. MCP's list_my_recipes renders each item
// through the same recipeOutput() get_my_recipe uses - including resolving
// recipe_ref ingredients - which needs the full document, not the preview
// projection.
func (s *Service) ListDetailedForUser(ctx context.Context, userID, cursor string, limit int, locale string) ([]models.Recipe, int64, string, error) {
	locale, err := normalizeLocale(locale)
	if err != nil {
		return nil, 0, "", err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	documents, count, err := s.db.GetRecipesByAuthor(ctx, userID, cursor, limit+1)
	if err != nil {
		return nil, 0, "", err
	}
	next := ""
	if len(documents) > limit {
		next = documents[limit-1].Id.Hex()
		documents = documents[:limit]
	}
	recipes := s.localizeBatch(ctx, documents, locale)
	s.decorateVariationCounts(ctx, recipes)
	refs := make([]*models.Recipe, len(recipes))
	for i := range recipes {
		refs[i] = &recipes[i]
	}
	s.decorateRefTitles(ctx, refs)
	return recipes, count, next, nil
}

// Search returns a page of full recipes (not previews) matching parameters,
// across every user - not just an author-scoped listing - for MCP's
// search_recipes tool to discover a fork target for create_recipe's
// variation_of. It reuses GetRecipeDocuments' family-collapsed
// filter/sort pipeline (the same one the public REST listing uses), driven
// by an offset rather than parameters.Offset/Limit directly, so the MCP tool
// can expose an opaque cursor instead of raw pagination numbers. hasNext
// reports whether another page exists; nextOffset is only meaningful when it
// does.
func (s *Service) Search(ctx context.Context, parameters models.GetRecipesRequest, offset, limit int) (recipes []models.Recipe, total int64, nextOffset int, hasNext bool, err error) {
	locale, err := normalizeLocale(parameters.Locale)
	if err != nil {
		return nil, 0, 0, false, err
	}
	parameters.Locale = locale
	parameters.Offset = offset
	parameters.Limit = limit + 1
	documents, count, err := s.db.GetRecipeDocuments(parameters)
	if err != nil {
		return nil, 0, 0, false, err
	}
	if hasNext = len(documents) > limit; hasNext {
		documents = documents[:limit]
		nextOffset = offset + limit
	}
	recipes = s.localizeBatch(ctx, documents, parameters.Locale)
	s.decorateVariationCounts(ctx, recipes)
	refs := make([]*models.Recipe, len(recipes))
	for i := range recipes {
		refs[i] = &recipes[i]
	}
	s.decorateRefTitles(ctx, refs)
	return recipes, count, nextOffset, hasNext, nil
}

// localizeBatch resolves each document's best-matching translation (or
// falls back to canonical) in one batched translations lookup - the shared
// core of localizePreviews and ListDetailedForUser.
func (s *Service) localizeBatch(ctx context.Context, documents []models.Recipe, requested string) []models.Recipe {
	requested, err := normalizeLocale(requested)
	translations := map[string]models.Recipe{}
	if err == nil && requested != "" {
		ids := make([]string, 0, len(documents))
		for _, document := range documents {
			if wantsTranslation(document, requested) {
				ids = append(ids, document.Id.Hex())
			}
		}
		if len(ids) > 0 {
			if fetched, fetchErr := s.db.GetTranslationsByRecipeIDs(ctx, ids, requested); fetchErr == nil {
				translations = fetched
			}
		}
	}
	localized := make([]models.Recipe, 0, len(documents))
	for _, document := range documents {
		recipe := document
		recipe.Locale = document.SourceLocale
		if document.Id != nil {
			translation, found := translations[document.Id.Hex()]
			recipe = pickTranslation(document, translation, requested, found)
		}
		localized = append(localized, recipe)
	}
	return localized
}

func (s *Service) localizePreviews(ctx context.Context, documents []models.Recipe, requested string) []models.RecipePreview {
	localized := s.localizeBatch(ctx, documents, requested)
	previews := make([]models.RecipePreview, 0, len(localized))
	for _, recipe := range localized {
		previews = append(previews, models.RecipePreview{
			Id: recipe.Id, Title: recipe.Title, Description: recipe.Description, Author: recipe.Author,
			PreparationTime: recipe.PreparationTime, CookingTime: recipe.CookingTime, RestingTime: recipe.RestingTime,
			Kind: recipe.Kind, Category: recipe.Category, Quantity: recipe.Quantity, Pictures: pictureFilenames(recipe.Pictures),
			SourceLocale: recipe.SourceLocale, Locale: recipe.Locale, VariationOf: recipe.VariationOf,
		})
	}
	return previews
}

func (s *Service) Delete(ctx context.Context, recipeID string) (models.RecipeDB, error) {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return models.RecipeDB{}, ErrNotFound
	}
	favorites, err := s.db.GetFavoriteInfo(ctx, []string{recipeID}, "")
	if err != nil {
		return models.RecipeDB{}, err
	}
	if favorites[recipeID].Count > 0 {
		return models.RecipeDB{}, ErrHasFavorites
	}
	// If recipeID has variations, promote the oldest one to be the new root
	// and re-point the rest to it before deleting recipeID itself (decision
	// #2). No DB transactions are available, so this relies on ordering, not
	// atomicity, to stay crash-safe: a crash here either leaves recipeID
	// fully intact (safe retry from scratch) or leaves promotion already
	// applied with recipeID merely not-yet-deleted (a harmless, retry-safe
	// duplicate root until the retry finishes).
	oldestID, err := s.db.GetOldestVariationID(ctx, recipeID)
	if err != nil {
		return models.RecipeDB{}, err
	}
	if oldestID != nil {
		if err := s.db.RepointVariations(ctx, recipeID, oldestID.Hex()); err != nil {
			return models.RecipeDB{}, err
		}
		if err := s.db.PromoteRecipeToRoot(ctx, oldestID.Hex()); err != nil {
			return models.RecipeDB{}, err
		}
		if err := s.db.RepointRecipeReferences(ctx, recipeID, oldestID.Hex()); err != nil {
			return models.RecipeDB{}, err
		}
	} else {
		// No variation to promote in recipeID's place, so any incoming
		// reference would be orphaned by deleting it - block instead.
		referenced, err := s.db.HasIncomingReferences(ctx, recipeID)
		if err != nil {
			return models.RecipeDB{}, err
		}
		if referenced {
			return models.RecipeDB{}, ErrRecipeReferenced
		}
	}
	deleted, err := s.db.DeleteRecipeById(recipeID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return models.RecipeDB{}, ErrNotFound
		}
		return models.RecipeDB{}, err
	}
	for _, picture := range deleted.Pictures {
		_ = images_manager.Remove(s.imageDir, picture.Filename)
	}
	for _, step := range deleted.Steps {
		if step.Picture != "" {
			_ = images_manager.Remove(s.imageDir, step.Picture)
		}
	}
	_ = s.db.DeleteLocalizedRecipesByID(ctx, recipeID)
	_ = s.db.DeleteFavoritesByRecipeID(ctx, recipeID)
	return deleted, nil
}

// LinkVariation turns recipeID, a standalone recipe (VariationOf == nil, no
// variations of its own), into a variation of targetID, a genuine root
// (never auto-flattened, unlike Create's variation_of - the caller must pass
// the actual root). Reachable by the recipe's own author or an admin (see
// handlers.RecipeAuthorMiddleware). Guards, in order: not the same recipe,
// source isn't already a variation, source has no variations of its own,
// target exists and is a root, categories match, and neither side's family
// already references the other (see ErrReferenceLoop) - the reference-loop
// check that Create's variation_of never needed, since linking (unlike
// submitting a new recipe) can retroactively join two families that already
// carry independent RecipeRef ingredients.
func (s *Service) LinkVariation(ctx context.Context, recipeID, targetID string) (models.Recipe, error) {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return models.Recipe{}, ErrNotFound
	}
	if _, err := primitive.ObjectIDFromHex(targetID); err != nil {
		return models.Recipe{}, fmt.Errorf("%w: invalid variation_of", ErrInvalid)
	}
	if recipeID == targetID {
		return models.Recipe{}, fmt.Errorf("%w: a recipe cannot be a variation of itself", ErrInvalid)
	}
	source, err := s.db.GetRecipeById(recipeID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return models.Recipe{}, ErrNotFound
		}
		return models.Recipe{}, err
	}
	if source.VariationOf != nil {
		return models.Recipe{}, ErrAlreadyVariation
	}
	counts, err := s.db.GetVariationCounts(ctx, []string{recipeID})
	if err != nil {
		return models.Recipe{}, err
	}
	if counts[recipeID] > 0 {
		return models.Recipe{}, ErrRecipeHasVariations
	}
	target, err := s.db.GetRecipeById(targetID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return models.Recipe{}, ErrNotFound
		}
		return models.Recipe{}, err
	}
	if target.VariationOf != nil {
		return models.Recipe{}, ErrTargetIsVariation
	}
	if defaultCategory(source.Category) != defaultCategory(target.Category) {
		return models.Recipe{}, ErrCategoryMismatch
	}
	for _, ingredient := range source.Ingredients {
		if ingredient.RecipeRef != nil && ingredient.RecipeRef.Hex() == targetID {
			return models.Recipe{}, ErrReferenceLoop
		}
	}
	referencesSource, err := s.db.FamilyReferencesRecipe(ctx, targetID, recipeID)
	if err != nil {
		return models.Recipe{}, err
	}
	if referencesSource {
		return models.Recipe{}, ErrReferenceLoop
	}
	if err := s.db.SetVariationOf(ctx, recipeID, targetID); err != nil {
		return models.Recipe{}, err
	}
	return s.Get(ctx, recipeID, "", "")
}

// DetachVariation clears recipeID's VariationOf, turning a variation back
// into its own standalone root - the one way back once LinkVariation (or
// submitting a variation at creation time) has joined a family. Admin only
// (see handlers.AdminDetachRecipeVariation); irreversible from the app's own
// UI once done, same as any other admin action here has no undo.
func (s *Service) DetachVariation(ctx context.Context, recipeID string) (models.Recipe, error) {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return models.Recipe{}, ErrNotFound
	}
	recipe, err := s.db.GetRecipeById(recipeID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return models.Recipe{}, ErrNotFound
		}
		return models.Recipe{}, err
	}
	if recipe.VariationOf == nil {
		return models.Recipe{}, ErrNotVariation
	}
	if err := s.db.PromoteRecipeToRoot(ctx, recipeID); err != nil {
		return models.Recipe{}, err
	}
	return s.Get(ctx, recipeID, "", "")
}
