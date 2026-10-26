package recipe_service

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

func (f *fakeStore) ListRecipeTags(context.Context) ([]models.RecipeTag, error) {
	tags := make([]models.RecipeTag, 0, len(f.tags))
	for _, tag := range f.tags {
		tags = append(tags, tag)
	}
	return tags, nil
}

func (f *fakeStore) GetRecipeTag(_ context.Context, slug string) (models.RecipeTag, error) {
	tag, ok := f.tags[slug]
	if !ok {
		return models.RecipeTag{}, mongorepo.TagNotFoundError
	}
	return tag, nil
}

func (f *fakeStore) CreateRecipeTag(_ context.Context, tag models.RecipeTag) error {
	if _, ok := f.tags[tag.Slug]; ok {
		return mongorepo.TagExistsError
	}
	if f.tags == nil {
		f.tags = map[string]models.RecipeTag{}
	}
	f.tags[tag.Slug] = tag
	return nil
}

func (f *fakeStore) UpdateRecipeTag(_ context.Context, tag models.RecipeTag) (models.RecipeTag, error) {
	if _, ok := f.tags[tag.Slug]; !ok {
		return models.RecipeTag{}, mongorepo.TagNotFoundError
	}
	f.tags[tag.Slug] = tag
	return tag, nil
}

func (f *fakeStore) DeleteRecipeTag(_ context.Context, slug string) error {
	if _, ok := f.tags[slug]; !ok {
		return mongorepo.TagNotFoundError
	}
	delete(f.tags, slug)
	return nil
}

func (f *fakeStore) CountRecipesWithTag(_ context.Context, slug string, _ []models.RecipeCategory) (int64, error) {
	return f.tagCounts[slug], nil
}

func (f *fakeStore) AddTagsToRecipesWithTag(_ context.Context, slug string, add []string) (int64, error) {
	if f.addedTags == nil {
		f.addedTags = map[string][]string{}
	}
	f.addedTags[slug] = add
	return f.tagCounts[slug], nil
}

func (f *fakeStore) FillRecipeTagLabel(context.Context, string, string, string, string, string) error {
	return nil
}

func testTag(slug string, group models.TagGroup, categories []models.RecipeCategory, implies ...string) models.RecipeTag {
	return models.RecipeTag{
		Slug: slug, Group: group, Categories: categories, Implies: implies,
		Labels: map[string]string{"en": slug}, SourceLocale: "en",
	}
}

var (
	foodOnly = []models.RecipeCategory{models.Food}
	bothCats = []models.RecipeCategory{models.Food, models.Diy}
)

// dietTags is vegan -> vegetarian -> pescatarian, plus gluten-free and a
// practical budget tag valid for both categories.
func dietTags() map[string]models.RecipeTag {
	return map[string]models.RecipeTag{
		"vegan":       testTag("vegan", models.TagGroupDiet, foodOnly, "vegetarian"),
		"vegetarian":  testTag("vegetarian", models.TagGroupDiet, foodOnly, "pescatarian"),
		"pescatarian": testTag("pescatarian", models.TagGroupDiet, foodOnly),
		"gluten-free": testTag("gluten-free", models.TagGroupDiet, foodOnly),
		"budget":      testTag("budget", models.TagGroupPractical, bothCats),
	}
}

func TestResolveRecipeTagsExpandsImplications(t *testing.T) {
	s := &Service{db: &fakeStore{tags: dietTags()}}
	recipe := models.RecipeDB{Category: models.Food, Tags: []string{"vegan", "budget"}}
	require.NoError(t, s.resolveRecipeTags(context.Background(), &recipe))
	assert.Equal(t, []string{"budget", "pescatarian", "vegan", "vegetarian"}, recipe.Tags)
}

func TestResolveRecipeTagsRejectsUnknownAndOutOfScope(t *testing.T) {
	s := &Service{db: &fakeStore{tags: dietTags()}}

	unknown := models.RecipeDB{Category: models.Food, Tags: []string{"keto"}}
	err := s.resolveRecipeTags(context.Background(), &unknown)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "valid: budget, gluten-free, pescatarian, vegan, vegetarian")

	diy := models.RecipeDB{Category: models.Diy, Tags: []string{"vegan"}}
	err = s.resolveRecipeTags(context.Background(), &diy)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "valid: budget")
}

func TestResolveRecipeTagsLegacyCategoryIsFood(t *testing.T) {
	s := &Service{db: &fakeStore{tags: dietTags()}}
	recipe := models.RecipeDB{Tags: []string{"gluten-free"}}
	require.NoError(t, s.resolveRecipeTags(context.Background(), &recipe))
	assert.Equal(t, []string{"gluten-free"}, recipe.Tags)
}

func TestCreateTagValidation(t *testing.T) {
	valid := func() models.RecipeTagRequest {
		return models.RecipeTagRequest{
			Slug: "dairy-free", Group: models.TagGroupDiet, Categories: foodOnly,
			Labels: map[string]string{"en": "Dairy-free"}, SourceLocale: "en",
		}
	}
	cases := map[string]func(*models.RecipeTagRequest){
		"bad slug":         func(r *models.RecipeTagRequest) { r.Slug = "Dairy Free" },
		"double hyphen":    func(r *models.RecipeTagRequest) { r.Slug = "dairy--free" },
		"bad group":        func(r *models.RecipeTagRequest) { r.Group = "cuisine" },
		"no category":      func(r *models.RecipeTagRequest) { r.Categories = nil },
		"no label":         func(r *models.RecipeTagRequest) { r.Labels = map[string]string{"en": "  "} },
		"unknown implies":  func(r *models.RecipeTagRequest) { r.Implies = []string{"keto"} },
		"implies narrower": func(r *models.RecipeTagRequest) { r.Categories = bothCats; r.Implies = []string{"vegan"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := &Service{db: &fakeStore{tags: dietTags()}}
			req := valid()
			mutate(&req)
			_, err := s.CreateTag(context.Background(), req)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}

	t.Run("duplicate slug", func(t *testing.T) {
		s := &Service{db: &fakeStore{tags: dietTags()}}
		req := valid()
		req.Slug = "vegan"
		_, err := s.CreateTag(context.Background(), req)
		assert.ErrorIs(t, err, ErrTagExists)
	})

	t.Run("source locale falls back to a labelled one", func(t *testing.T) {
		s := &Service{db: &fakeStore{tags: dietTags()}, targetLocales: []string{"en", "fr", "fi"}}
		req := valid()
		req.Labels = map[string]string{"fr": "Sans lactose", "fi": ""}
		req.SourceLocale = "en"
		tag, err := s.CreateTag(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, "fr", tag.SourceLocale)
		assert.Equal(t, map[string]string{"fr": "Sans lactose"}, tag.Labels)
	})
}

func TestUpdateTagRejectsCycle(t *testing.T) {
	s := &Service{db: &fakeStore{tags: dietTags()}}
	_, _, err := s.UpdateTag(context.Background(), "pescatarian", models.RecipeTagRequest{
		Group: models.TagGroupDiet, Categories: foodOnly, Labels: map[string]string{"en": "Pescatarian"},
		Implies: []string{"vegan"},
	})
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestUpdateTagBackfillsOnlyNewImplications(t *testing.T) {
	tags := dietTags()
	tags["dairy-free"] = testTag("dairy-free", models.TagGroupDiet, foodOnly)
	store := &fakeStore{tags: tags, tagCounts: map[string]int64{"vegan": 3}}
	s := &Service{db: store}

	_, backfilled, err := s.UpdateTag(context.Background(), "vegan", models.RecipeTagRequest{
		Group: models.TagGroupDiet, Categories: foodOnly, Labels: map[string]string{"en": "Vegan"},
		Implies: []string{"vegetarian", "dairy-free"},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3), backfilled)
	assert.Equal(t, []string{"dairy-free"}, store.addedTags["vegan"])

	store.addedTags = nil
	_, _, err = s.UpdateTag(context.Background(), "vegan", models.RecipeTagRequest{
		Group: models.TagGroupDiet, Categories: foodOnly, Labels: map[string]string{"en": "Vegan"},
		Implies: []string{"dairy-free"},
	})
	require.NoError(t, err)
	assert.Nil(t, store.addedTags, "removing an implication must not touch recipes")
}

func TestUpdateTagRefusesDroppingUsedCategory(t *testing.T) {
	store := &fakeStore{tags: dietTags(), tagCounts: map[string]int64{"budget": 1}}
	s := &Service{db: store}
	_, _, err := s.UpdateTag(context.Background(), "budget", models.RecipeTagRequest{
		Group: models.TagGroupPractical, Categories: foodOnly, Labels: map[string]string{"en": "Budget"},
	})
	assert.ErrorIs(t, err, ErrTagInUse)
}

func TestDeleteTagRefusedWhileInUse(t *testing.T) {
	store := &fakeStore{tags: dietTags(), tagCounts: map[string]int64{"gluten-free": 2}}
	s := &Service{db: store}

	assert.ErrorIs(t, s.DeleteTag(context.Background(), "vegetarian"), ErrTagInUse, "implied by vegan")
	assert.ErrorIs(t, s.DeleteTag(context.Background(), "gluten-free"), ErrTagInUse, "carried by recipes")
	assert.ErrorIs(t, s.DeleteTag(context.Background(), "keto"), ErrTagNotFound)
	require.NoError(t, s.DeleteTag(context.Background(), "budget"))
	_, ok := store.tags["budget"]
	assert.False(t, ok)
}

func TestListTagsOrdersByGroupAndResolvesLabel(t *testing.T) {
	tags := dietTags()
	vegan := tags["vegan"]
	vegan.Labels = map[string]string{"en": "Vegan", "fr": "Végan"}
	tags["vegan"] = vegan
	s := &Service{db: &fakeStore{tags: tags}}

	list, err := s.ListTags(context.Background(), "fr-CA")
	require.NoError(t, err)
	slugs := make([]string, len(list))
	for i, tag := range list {
		slugs[i] = tag.Slug
	}
	assert.Equal(t, []string{"gluten-free", "pescatarian", "vegan", "vegetarian", "budget"}, slugs)
	assert.Equal(t, "Végan", list[slices.IndexFunc(list, func(t models.RecipeTag) bool { return t.Slug == "vegan" })].Label)
	assert.Equal(t, "budget", list[4].Label, "falls back to the source label")
}

func TestPickTranslationKeepsCanonicalTags(t *testing.T) {
	canonical := models.Recipe{SourceHash: "h", Tags: []string{"vegan"}}
	translation := models.Recipe{SourceHash: "h", Tags: []string{"stale"}}
	assert.Equal(t, []string{"vegan"}, pickTranslation(canonical, translation, "fr", true).Tags)
}

func TestSourceHashIgnoresTags(t *testing.T) {
	recipe := models.RecipeDB{Title: "Soup"}
	tagged := recipe
	tagged.Tags = []string{"vegan"}
	assert.Equal(t, sourceHash(recipe), sourceHash(tagged))
}
