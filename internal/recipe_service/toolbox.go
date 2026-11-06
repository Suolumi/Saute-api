package recipe_service

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"recipes/internal/models"
	"recipes/internal/utils"
)

// Kinds of Toolbox entry scheduleToolboxTranslation handles.
const (
	toolboxKindUnit         = "unit"
	toolboxKindSubstitution = "substitution"
)

const (
	minToolboxUnitToBase   = 0.001
	maxToolboxUnitToBase   = 100000.0
	maxToolboxNameLength   = 100
	maxToolboxTextLength   = 300
	maxToolboxSymbolLength = 20
)

// ListToolboxUnits returns every unit conversions and nutrition understand,
// Name resolved into locale when a machine translation exists.
func (s *Service) ListToolboxUnits(ctx context.Context, locale string) ([]models.ToolboxUnit, error) {
	items, err := s.db.ListToolboxUnits(ctx)
	if err != nil {
		return nil, err
	}
	locale, _ = normalizeLocale(locale)
	for i := range items {
		items[i] = resolveToolboxUnitLocale(items[i], locale)
	}
	return items, nil
}

// ListToolboxSubstitutions returns every ingredient-swap tip, resolved into
// locale like ListToolboxUnits.
func (s *Service) ListToolboxSubstitutions(ctx context.Context, locale string) ([]models.ToolboxSubstitution, error) {
	items, err := s.db.ListToolboxSubstitutions(ctx)
	if err != nil {
		return nil, err
	}
	locale, _ = normalizeLocale(locale)
	for i := range items {
		items[i] = resolveToolboxSubstitutionLocale(items[i], locale)
	}
	return items, nil
}

// resolveToolboxUnitLocale swaps in item's stored translation for locale,
// unless locale is the item's own source language. Only Name is translatable.
func resolveToolboxUnitLocale(item models.ToolboxUnit, locale string) models.ToolboxUnit {
	if locale != "" && !sameBaseLocale(item.SourceLocale, locale) {
		if t, ok := item.Translations[locale]; ok {
			item.Name = t.Name
		}
	}
	item.Translations = nil
	return item
}

// resolveToolboxSubstitutionLocale is resolveToolboxUnitLocale for
// substitutions.
func resolveToolboxSubstitutionLocale(item models.ToolboxSubstitution, locale string) models.ToolboxSubstitution {
	if locale != "" && !sameBaseLocale(item.SourceLocale, locale) {
		if t, ok := item.Translations[locale]; ok {
			item.Problem = t.Problem
			item.Solution = t.Solution
			item.Tag = t.Tag
		}
	}
	item.Translations = nil
	return item
}

// scheduleToolboxTranslation kicks a detached goroutine that detects one
// toolbox entry's source language and machine-translates it into every
// other configured target locale, mirroring Service.scheduleTranslations for
// recipes but writing straight into the entry's own translations map rather
// than a sibling per-locale document - the list is small enough that the
// whole map is cheaply regenerated on every write, and (unlike a recipe's
// translation) nothing here needs to survive that regeneration, so there's
// no override/snapshot machinery to re-apply. Unlike recipe translation,
// there's no retry backoff - a failure is logged and the entry is simply
// left un-translated for that locale until the next write.
func (s *Service) scheduleToolboxTranslation(kind string, id primitive.ObjectID) {
	if !s.canTranslate() {
		return
	}
	go func() {
		if err := s.translateToolboxEntry(kind, id); err != nil {
			utils.LogError("translate toolbox entry", err, "kind", kind, "id", id.Hex())
		}
	}()
}

// translateTextsCased is TranslateTexts with a workaround for a quirk in
// Google's NMT model: a standalone capitalized word is sometimes read as a
// proper noun and translated to a different word entirely, rather than as
// the ordinary word it is - e.g. "Centiliter" (the toolbox's own unit name)
// comes back as "Centimètres" in French, and "Honey" as "Chéri" (a term of
// endearment) instead of "Miel". Lowercasing before translating and
// restoring the original's leading capitalization on the result avoids this
// without losing toolbox entries' always-capitalized display convention.
func (s *Service) translateTextsCased(texts []string, to string) ([]string, error) {
	lowered := make([]string, len(texts))
	for i, text := range texts {
		lowered[i] = strings.ToLower(text)
	}
	translated, err := s.translator.TranslateTexts(lowered, to)
	if err != nil {
		return nil, err
	}
	for i, text := range texts {
		translated[i] = matchLeadingCase(text, translated[i])
	}
	return translated, nil
}

// matchLeadingCase upper-cases translated's first rune when original's is
// upper-case, leaving translated's casing otherwise untouched (it's an NMT
// translation of a lowercased source, not something to further reshape).
func matchLeadingCase(original, translated string) string {
	origRunes := []rune(original)
	if len(origRunes) == 0 || !unicode.IsUpper(origRunes[0]) {
		return translated
	}
	translatedRunes := []rune(translated)
	if len(translatedRunes) == 0 {
		return translated
	}
	translatedRunes[0] = unicode.ToUpper(translatedRunes[0])
	return string(translatedRunes)
}

func (s *Service) translateToolboxEntry(kind string, id primitive.ObjectID) error {
	s.translateSlots <- struct{}{}
	defer func() { <-s.translateSlots }()
	ctx := context.Background()

	switch kind {
	case toolboxKindUnit:
		entry, err := s.db.GetToolboxUnitById(ctx, id.Hex())
		if err != nil {
			return err
		}
		source, err := s.translator.DetectLocale(entry.Name)
		if err != nil {
			return err
		}
		translations := make(map[string]models.ToolboxUnitTranslation)
		for _, locale := range targetLocalesFor(source, s.targetLocales) {
			translated, err := s.translateTextsCased([]string{entry.Name}, locale)
			if err != nil {
				return err
			}
			translations[locale] = models.ToolboxUnitTranslation{Name: translated[0]}
		}
		return s.db.SetToolboxUnitTranslations(ctx, id.Hex(), source, translations)

	case toolboxKindSubstitution:
		entry, err := s.db.GetToolboxSubstitutionById(ctx, id.Hex())
		if err != nil {
			return err
		}
		source, err := s.translator.DetectLocale(entry.Problem + " " + entry.Solution)
		if err != nil {
			return err
		}
		translations := make(map[string]models.ToolboxSubstitutionTranslation)
		for _, locale := range targetLocalesFor(source, s.targetLocales) {
			translated, err := s.translateTextsCased([]string{entry.Problem, entry.Solution, entry.Tag}, locale)
			if err != nil {
				return err
			}
			tag := ""
			if entry.Tag != "" {
				tag = translated[2]
			}
			translations[locale] = models.ToolboxSubstitutionTranslation{Problem: translated[0], Solution: translated[1], Tag: tag}
		}
		return s.db.SetToolboxSubstitutionTranslations(ctx, id.Hex(), source, translations)

	default:
		return fmt.Errorf("%w: unknown toolbox kind %q", ErrInvalid, kind)
	}
}

// toolboxNeedsTranslation reports whether an entry has never been translated
// (no SourceLocale yet, e.g. seeded before the translator existed) or lacks a
// translation for one of targets.
func toolboxNeedsTranslation[T any](sourceLocale string, translations map[string]T, targets []string) bool {
	if sourceLocale == "" {
		return true
	}
	for _, locale := range targetLocalesFor(sourceLocale, targets) {
		if _, ok := translations[locale]; !ok {
			return true
		}
	}
	return false
}

// backfillToolboxTranslations schedules translation for every toolbox entry
// toolboxNeedsTranslation flags. Retranslating rebuilds the entry's whole
// map, which is cheap at the toolbox's size.
func (s *Service) backfillToolboxTranslations(ctx context.Context) {
	if units, err := s.db.ListToolboxUnits(ctx); err == nil {
		for _, item := range units {
			if item.Id != nil && toolboxNeedsTranslation(item.SourceLocale, item.Translations, s.targetLocales) {
				s.scheduleToolboxTranslation(toolboxKindUnit, *item.Id)
			}
		}
	}
	if substitutions, err := s.db.ListToolboxSubstitutions(ctx); err == nil {
		for _, item := range substitutions {
			if item.Id != nil && toolboxNeedsTranslation(item.SourceLocale, item.Translations, s.targetLocales) {
				s.scheduleToolboxTranslation(toolboxKindSubstitution, *item.Id)
			}
		}
	}
}
