package recipe_service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
	"recipes/internal/utils"
)

const maxTagLabelLength = 40

var (
	ErrTagNotFound = errors.New("tag not found")
	ErrTagExists   = errors.New("tag already exists")
	// ErrTagInUse is DeleteTag's guard, and UpdateTag's when narrowing a
	// tag's categories would orphan it on recipes or on implying tags.
	ErrTagInUse = errors.New("tag is in use")
)

var tagSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func tagError(err error) error {
	switch {
	case errors.Is(err, mongorepo.TagNotFoundError):
		return ErrTagNotFound
	case errors.Is(err, mongorepo.TagExistsError):
		return ErrTagExists
	}
	return err
}

func (s *Service) tagsBySlug(ctx context.Context) (map[string]models.RecipeTag, error) {
	tags, err := s.db.ListRecipeTags(ctx)
	if err != nil {
		return nil, err
	}
	bySlug := make(map[string]models.RecipeTag, len(tags))
	for _, tag := range tags {
		bySlug[tag.Slug] = tag
	}
	return bySlug, nil
}

// ListTags returns every tag in display order (group, then slug). A non-empty
// locale also resolves each tag's Label for it.
func (s *Service) ListTags(ctx context.Context, locale string) ([]models.RecipeTag, error) {
	locale, err := normalizeLocale(locale)
	if err != nil {
		return nil, err
	}
	tags, err := s.db.ListRecipeTags(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(tags, func(a, b models.RecipeTag) int {
		return cmp.Or(
			cmp.Compare(slices.Index(models.TagGroups, a.Group), slices.Index(models.TagGroups, b.Group)),
			cmp.Compare(a.Slug, b.Slug),
		)
	})
	if locale != "" {
		for i := range tags {
			tags[i].Label = resolveTagLabel(tags[i], locale)
		}
	}
	return tags, nil
}

// resolveTagLabel picks the label for locale, then for its base language, then
// the source label, then the slug.
func resolveTagLabel(tag models.RecipeTag, locale string) string {
	if label := tag.Labels[locale]; label != "" {
		return label
	}
	for key, label := range tag.Labels {
		if label != "" && sameBaseLocale(key, locale) {
			return label
		}
	}
	if label := tag.Labels[tag.SourceLocale]; label != "" {
		return label
	}
	return tag.Slug
}

// tagClosure returns slugs plus every tag they transitively imply, sorted.
func tagClosure(slugs []string, all map[string]models.RecipeTag) []string {
	seen := make(map[string]bool, len(slugs))
	queue := slices.Clone(slugs)
	for len(queue) > 0 {
		slug := queue[0]
		queue = queue[1:]
		if seen[slug] {
			continue
		}
		seen[slug] = true
		queue = append(queue, all[slug].Implies...)
	}
	closure := make([]string, 0, len(seen))
	for slug := range seen {
		closure = append(closure, slug)
	}
	slices.Sort(closure)
	return closure
}

// tagAppliesTo reports whether tag can be used on a recipe of category.
func tagAppliesTo(tag models.RecipeTag, category models.RecipeCategory) bool {
	return slices.Contains(tag.Categories, defaultCategory(category))
}

// validRecipeTags returns the slugs accepted on a recipe of category, for
// error messages.
func validRecipeTags(all map[string]models.RecipeTag, category models.RecipeCategory) string {
	valid := make([]string, 0, len(all))
	for slug, tag := range all {
		if tagAppliesTo(tag, category) {
			valid = append(valid, slug)
		}
	}
	slices.Sort(valid)
	return strings.Join(valid, ", ")
}

// resolveRecipeTags validates recipe.Tags against the tag list and the
// recipe's category, then expands them with every implied tag. Call after
// validateRecipe, which defaults the category.
func (s *Service) resolveRecipeTags(ctx context.Context, recipe *models.RecipeDB) error {
	if len(recipe.Tags) == 0 {
		recipe.Tags = nil
		return nil
	}
	all, err := s.tagsBySlug(ctx)
	if err != nil {
		return err
	}
	for _, slug := range recipe.Tags {
		tag, ok := all[slug]
		if !ok || !tagAppliesTo(tag, recipe.Category) {
			return fmt.Errorf("%w: unknown tag %q for a %s recipe (valid: %s)", ErrInvalid, slug, defaultCategory(recipe.Category), validRecipeTags(all, recipe.Category))
		}
	}
	recipe.Tags = tagClosure(recipe.Tags, all)
	return nil
}

// buildTag validates req into a tag. existing is the stored tag on update,
// nil on create; all holds every stored tag.
func (s *Service) buildTag(req models.RecipeTagRequest, existing *models.RecipeTag, all map[string]models.RecipeTag) (models.RecipeTag, error) {
	tag := models.RecipeTag{Group: req.Group, CreatedAt: time.Now().UTC()}
	if existing != nil {
		tag.Slug = existing.Slug
		tag.CreatedAt = existing.CreatedAt
	} else {
		tag.Slug = strings.TrimSpace(req.Slug)
		if len(tag.Slug) > maxTagLabelLength || !tagSlugPattern.MatchString(tag.Slug) {
			return models.RecipeTag{}, fmt.Errorf("%w: slug must be lowercase letters, digits and single hyphens", ErrInvalid)
		}
	}
	if !slices.Contains(models.TagGroups, tag.Group) {
		return models.RecipeTag{}, fmt.Errorf("%w: invalid tag group", ErrInvalid)
	}
	for _, category := range req.Categories {
		if !slices.Contains(models.RecipeCategories, category) {
			return models.RecipeTag{}, fmt.Errorf("%w: invalid tag category", ErrInvalid)
		}
		if !slices.Contains(tag.Categories, category) {
			tag.Categories = append(tag.Categories, category)
		}
	}
	if len(tag.Categories) == 0 {
		return models.RecipeTag{}, fmt.Errorf("%w: a tag needs at least one category", ErrInvalid)
	}

	tag.Labels = map[string]string{}
	for locale, label := range req.Labels {
		locale, err := normalizeLocale(locale)
		if err != nil {
			return models.RecipeTag{}, err
		}
		label = strings.TrimSpace(label)
		if label == "" || locale == "" {
			continue
		}
		if len([]rune(label)) > maxTagLabelLength {
			return models.RecipeTag{}, fmt.Errorf("%w: a tag label must not exceed %d characters", ErrInvalid, maxTagLabelLength)
		}
		tag.Labels[locale] = label
	}
	if len(tag.Labels) == 0 {
		return models.RecipeTag{}, fmt.Errorf("%w: a tag needs at least one label", ErrInvalid)
	}
	tag.SourceLocale, _ = normalizeLocale(req.SourceLocale)
	if tag.Labels[tag.SourceLocale] == "" {
		tag.SourceLocale = ""
		for _, locale := range s.targetLocales {
			if tag.Labels[locale] != "" {
				tag.SourceLocale = locale
				break
			}
		}
		if tag.SourceLocale == "" {
			locales := make([]string, 0, len(tag.Labels))
			for locale := range tag.Labels {
				locales = append(locales, locale)
			}
			tag.SourceLocale = slices.Min(locales)
		}
	}

	tag.Implies = []string{}
	for _, slug := range req.Implies {
		implied, ok := all[slug]
		if !ok {
			return models.RecipeTag{}, fmt.Errorf("%w: implied tag %q does not exist", ErrInvalid, slug)
		}
		if slug == tag.Slug {
			return models.RecipeTag{}, fmt.Errorf("%w: a tag cannot imply itself", ErrInvalid)
		}
		for _, category := range tag.Categories {
			if !slices.Contains(implied.Categories, category) {
				return models.RecipeTag{}, fmt.Errorf("%w: implied tag %q must apply to every category this tag applies to", ErrInvalid, slug)
			}
		}
		if slices.Contains(tagClosure([]string{slug}, all), tag.Slug) {
			return models.RecipeTag{}, fmt.Errorf("%w: implying %q would create a cycle", ErrInvalid, slug)
		}
		if !slices.Contains(tag.Implies, slug) {
			tag.Implies = append(tag.Implies, slug)
		}
	}
	slices.Sort(tag.Implies)
	return tag, nil
}

// CreateTag adds a tag and machine-translates its blank labels in the
// background.
func (s *Service) CreateTag(ctx context.Context, req models.RecipeTagRequest) (models.RecipeTag, error) {
	all, err := s.tagsBySlug(ctx)
	if err != nil {
		return models.RecipeTag{}, err
	}
	tag, err := s.buildTag(req, nil, all)
	if err != nil {
		return models.RecipeTag{}, err
	}
	if err := s.db.CreateRecipeTag(ctx, tag); err != nil {
		return models.RecipeTag{}, tagError(err)
	}
	s.scheduleTagLabelTranslation(tag)
	return tag, nil
}

// UpdateTag overwrites a tag. Implications it newly gains are backfilled onto
// every recipe carrying it; removed ones stay on recipes, since an author may
// have picked them on purpose.
func (s *Service) UpdateTag(ctx context.Context, slug string, req models.RecipeTagRequest) (models.RecipeTag, int64, error) {
	all, err := s.tagsBySlug(ctx)
	if err != nil {
		return models.RecipeTag{}, 0, err
	}
	existing, ok := all[slug]
	if !ok {
		return models.RecipeTag{}, 0, ErrTagNotFound
	}
	tag, err := s.buildTag(req, &existing, all)
	if err != nil {
		return models.RecipeTag{}, 0, err
	}

	var dropped []models.RecipeCategory
	for _, category := range existing.Categories {
		if !slices.Contains(tag.Categories, category) {
			dropped = append(dropped, category)
		}
	}
	if len(dropped) > 0 {
		for _, other := range all {
			if slices.Contains(other.Implies, slug) && slices.ContainsFunc(dropped, func(c models.RecipeCategory) bool { return slices.Contains(other.Categories, c) }) {
				return models.RecipeTag{}, 0, fmt.Errorf("%w: tag %q implies it in a category being removed", ErrTagInUse, other.Slug)
			}
		}
		count, err := s.db.CountRecipesWithTag(ctx, slug, dropped)
		if err != nil {
			return models.RecipeTag{}, 0, err
		}
		if count > 0 {
			return models.RecipeTag{}, 0, fmt.Errorf("%w: %d recipe(s) in a category being removed carry it", ErrTagInUse, count)
		}
	}

	updated, err := s.db.UpdateRecipeTag(ctx, tag)
	if err != nil {
		return models.RecipeTag{}, 0, tagError(err)
	}
	all[slug] = updated
	oldClosure := tagClosure(existing.Implies, all)
	var added []string
	for _, implied := range tagClosure(updated.Implies, all) {
		if !slices.Contains(oldClosure, implied) {
			added = append(added, implied)
		}
	}
	var backfilled int64
	if len(added) > 0 {
		backfilled, err = s.db.AddTagsToRecipesWithTag(ctx, slug, added)
		if err != nil {
			return models.RecipeTag{}, 0, err
		}
	}
	s.scheduleTagLabelTranslation(updated)
	return updated, backfilled, nil
}

// DeleteTag removes a tag nothing uses: no recipe carries it and no other tag
// implies it.
func (s *Service) DeleteTag(ctx context.Context, slug string) error {
	all, err := s.tagsBySlug(ctx)
	if err != nil {
		return err
	}
	if _, ok := all[slug]; !ok {
		return ErrTagNotFound
	}
	for _, other := range all {
		if slices.Contains(other.Implies, slug) {
			return fmt.Errorf("%w: tag %q implies it", ErrTagInUse, other.Slug)
		}
	}
	count, err := s.db.CountRecipesWithTag(ctx, slug, nil)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: %d recipe(s) carry it", ErrTagInUse, count)
	}
	return tagError(s.db.DeleteRecipeTag(ctx, slug))
}

// scheduleTagLabelTranslation fills each configured locale's blank label from
// the source label, in the background.
func (s *Service) scheduleTagLabelTranslation(tag models.RecipeTag) {
	if !s.canTranslate() {
		return
	}
	source := tag.Labels[tag.SourceLocale]
	var missing []string
	for _, locale := range s.targetLocales {
		if tag.Labels[locale] == "" && !sameBaseLocale(locale, tag.SourceLocale) {
			missing = append(missing, locale)
		}
	}
	if len(missing) == 0 {
		return
	}
	go func() {
		s.translateSlots <- struct{}{}
		defer func() { <-s.translateSlots }()
		for _, locale := range missing {
			translated, err := s.translateTextsCased([]string{source}, locale)
			if err != nil || len(translated) != 1 {
				utils.LogError("translate tag label", err, "tag", tag.Slug, "locale", locale)
				continue
			}
			if err := s.db.FillRecipeTagLabel(context.Background(), tag.Slug, tag.SourceLocale, source, locale, translated[0]); err != nil {
				utils.LogError("store tag label", err, "tag", tag.Slug, "locale", locale)
			}
		}
	}()
}
