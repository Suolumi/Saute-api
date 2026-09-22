package recipe_service

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
	"recipes/internal/utils"
)

// targetLocalesFor drops any configured target that shares a base language with
// the recipe's source locale.
func targetLocalesFor(source string, targets []string) []string {
	filtered := make([]string, 0, len(targets))
	for _, locale := range targets {
		if !sameBaseLocale(source, locale) {
			filtered = append(filtered, locale)
		}
	}
	return filtered
}

// scheduleTranslations kicks a detached goroutine that translates the recipe
// into every configured target locale. Fire-and-forget: failures are logged,
// not retried beyond translateLocale's own attempts, and lost on shutdown.
func (s *Service) scheduleTranslations(canonical models.Recipe) {
	if !s.canTranslate() || canonical.Id == nil || canonical.SourceHash == "" {
		return
	}
	go func() {
		if err := s.translateAll(canonical, true); err != nil {
			utils.LogError("translate recipe on write", err, "recipe", canonical.Id.Hex())
		}
	}()
}

// translateAll translates canonical into each target locale, holding one
// concurrency slot for the whole batch. Locales are attempted independently;
// the first error is returned.
func (s *Service) translateAll(canonical models.Recipe, force bool) error {
	s.translateSlots <- struct{}{}
	defer func() { <-s.translateSlots }()

	var firstErr error
	for _, locale := range targetLocalesFor(canonical.SourceLocale, s.targetLocales) {
		if err := s.translateLocale(canonical, locale, force); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// translateLocale translates one locale and stores the row. When force is false
// it first skips locales that already have a row matching the current source
// hash. Each locale gets len(translateBackoffs) attempts.
func (s *Service) translateLocale(canonical models.Recipe, locale string, force bool) error {
	if !force {
		if existing, err := s.db.GetRecipeByIdLocale(canonical.Id.Hex(), locale); err == nil &&
			existing.SourceHash != "" && existing.SourceHash == canonical.SourceHash {
			return nil
		}
	}
	var lastErr error
	for _, backoff := range translateBackoffs {
		time.Sleep(backoff)
		translated, err := s.translator.TranslateRecipe(canonical, locale)
		if err != nil {
			lastErr = err
			continue
		}
		translated.SourceLocale = canonical.SourceLocale
		translated.Locale = locale
		translated.SourceHash = canonical.SourceHash
		translated = s.applyTranslationOverrides(canonical, translated, locale)
		if _, err := s.db.AddLocaleRecipe(translated, locale); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// Retranslate re-runs translation for one recipe against its current canonical
// text. Intended for admin corrections; returns once the work is scheduled.
func (s *Service) Retranslate(ctx context.Context, recipeID string) error {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return ErrNotFound
	}
	recipe, err := s.db.GetRecipeById(recipeID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return ErrNotFound
		}
		return err
	}
	s.scheduleTranslations(recipe)
	return nil
}

// RetranslateLocale forces a fresh translation of one recipe into one locale
// only, synchronously - unlike Retranslate (all configured locales,
// fire-and-forget), this is meant for a caller that needs the result visible
// immediately, e.g. ClearTranslationOverride reverting one pinned field back
// to machine translation on demand.
func (s *Service) RetranslateLocale(ctx context.Context, recipeID, locale string) error {
	if _, err := primitive.ObjectIDFromHex(recipeID); err != nil {
		return ErrNotFound
	}
	if !s.canTranslate() {
		return nil
	}
	canonical, err := s.db.GetRecipeById(recipeID)
	if err != nil {
		if errors.Is(err, mongorepo.NotFoundError) {
			return ErrNotFound
		}
		return err
	}
	s.translateSlots <- struct{}{}
	defer func() { <-s.translateSlots }()
	return s.translateLocale(canonical, locale, true)
}
