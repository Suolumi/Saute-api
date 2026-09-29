package recipe_service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
	"recipes/internal/utils"
)

const (
	minToolboxGPer100ml    = 20.0
	maxToolboxGPer100ml    = 250.0
	minToolboxUnitToBase   = 0.001
	maxToolboxUnitToBase   = 100000.0
	maxToolboxNameLength   = 100
	maxToolboxNoteLength   = 500
	maxToolboxTextLength   = 300
	maxToolboxSymbolLength = 20
)

// ListToolboxIngredients returns every ingredient in the quantity converter's
// density list, alphabetically - the list is curated and small enough that
// no pagination is needed; the client searches/filters it itself. Each
// entry's Name/Note is resolved into locale when a machine translation is
// available (see resolveToolboxIngredientLocale), else left in its source
// language.
func (s *Service) ListToolboxIngredients(ctx context.Context, locale string) ([]models.ToolboxIngredient, error) {
	items, err := s.db.ListToolboxIngredients(ctx)
	if err != nil {
		return nil, err
	}
	locale, _ = normalizeLocale(locale)
	for i := range items {
		items[i] = resolveToolboxIngredientLocale(items[i], locale)
	}
	return items, nil
}

// ListToolboxUnits returns every unit the quantity converter can convert
// to/from, Name resolved into locale like ListToolboxIngredients.
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
// locale like ListToolboxIngredients.
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

// resolveToolboxIngredientLocale swaps in item's stored translation for
// locale, when one exists and locale isn't just the source language under a
// different regional tag (e.g. "en-US" vs "en"). A locale with no stored
// translation yet (translate-on-write hasn't caught up, or the translator
// isn't configured) falls back to the source text, same as a recipe with no
// current translation row.
func resolveToolboxIngredientLocale(item models.ToolboxIngredient, locale string) models.ToolboxIngredient {
	if locale != "" && !sameBaseLocale(item.SourceLocale, locale) {
		if t, ok := item.Translations[locale]; ok {
			item.Name = t.Name
			item.Note = t.Note
		}
	}
	item.Translations = nil
	return item
}

// resolveToolboxUnitLocale is resolveToolboxIngredientLocale for units -
// only Name is translatable.
func resolveToolboxUnitLocale(item models.ToolboxUnit, locale string) models.ToolboxUnit {
	if locale != "" && !sameBaseLocale(item.SourceLocale, locale) {
		if t, ok := item.Translations[locale]; ok {
			item.Name = t.Name
		}
	}
	item.Translations = nil
	return item
}

// resolveToolboxSubstitutionLocale is resolveToolboxIngredientLocale for
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
	case models.ToolboxSuggestionIngredient:
		entry, err := s.db.GetToolboxIngredientById(ctx, id.Hex())
		if err != nil {
			return err
		}
		source, err := s.translator.DetectLocale(entry.Name)
		if err != nil {
			return err
		}
		translations := make(map[string]models.ToolboxIngredientTranslation)
		for _, locale := range targetLocalesFor(source, s.targetLocales) {
			translated, err := s.translateTextsCased([]string{entry.Name, entry.Note}, locale)
			if err != nil {
				return err
			}
			note := ""
			if entry.Note != "" {
				note = translated[1]
			}
			translations[locale] = models.ToolboxIngredientTranslation{Name: translated[0], Note: note}
		}
		return s.db.SetToolboxIngredientTranslations(ctx, id.Hex(), source, translations)

	case models.ToolboxSuggestionUnit:
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

	case models.ToolboxSuggestionSubstitution:
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

// EnsureToolboxTranslations schedules translation for every ingredient/unit
// entry that predates translate-on-write, i.e. has no SourceLocale yet -
// covers the starter set SeedToolboxDefaults inserts, which runs before the
// translator is constructed and so can't translate inline. Safe to call on
// every startup: an entry that already has a SourceLocale is skipped, so a
// restart doesn't re-translate (and re-spend API quota on) entries already
// handled. Substitutions never get seed data (see SeedToolboxDefaults), so
// there's nothing to backfill there - every one already went through
// scheduleToolboxTranslation at creation.
func (s *Service) EnsureToolboxTranslations(ctx context.Context) {
	if !s.canTranslate() {
		return
	}
	if ingredients, err := s.db.ListToolboxIngredients(ctx); err == nil {
		for _, ingredient := range ingredients {
			if ingredient.SourceLocale == "" && ingredient.Id != nil {
				s.scheduleToolboxTranslation(models.ToolboxSuggestionIngredient, *ingredient.Id)
			}
		}
	}
	if units, err := s.db.ListToolboxUnits(ctx); err == nil {
		for _, unit := range units {
			if unit.SourceLocale == "" && unit.Id != nil {
				s.scheduleToolboxTranslation(models.ToolboxSuggestionUnit, *unit.Id)
			}
		}
	}
}

// SubmitIngredientSuggestion validates and stores a user-proposed addition
// or correction to the density list. A name that already matches an
// existing entry (case-insensitively) is submitted as a correction to that
// entry (TargetID set) rather than rejected as a duplicate.
func (s *Service) SubmitIngredientSuggestion(ctx context.Context, userID string, req models.SubmitIngredientSuggestionRequest) (models.ToolboxSuggestion, error) {
	submittedBy, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return models.ToolboxSuggestion{}, ErrForbidden
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxToolboxNameLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: name must be between 1 and %d characters", ErrInvalid, maxToolboxNameLength)
	}
	if req.GPer100ml < minToolboxGPer100ml || req.GPer100ml > maxToolboxGPer100ml {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: density must be between %g and %g grams per 100ml", ErrInvalid, minToolboxGPer100ml, maxToolboxGPer100ml)
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > maxToolboxNoteLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: note must be at most %d characters", ErrInvalid, maxToolboxNoteLength)
	}

	targetID, err := s.findToolboxIngredientTarget(ctx, name)
	if err != nil {
		return models.ToolboxSuggestion{}, err
	}

	suggestion := models.ToolboxSuggestion{
		Kind: models.ToolboxSuggestionIngredient, TargetID: targetID, SubmittedBy: submittedBy, Note: note,
		IngredientName: name, GPer100ml: req.GPer100ml,
	}
	return s.db.CreateToolboxSuggestion(ctx, suggestion)
}

func (s *Service) findToolboxIngredientTarget(ctx context.Context, name string) (*primitive.ObjectID, error) {
	existing, err := s.db.GetToolboxIngredientByNameLower(ctx, strings.ToLower(name))
	if err == nil {
		return existing.Id, nil
	}
	if errors.Is(err, mongorepo.ToolboxEntryNotFoundError) {
		return nil, nil
	}
	return nil, err
}

// SubmitUnitSuggestion validates and stores a user-proposed addition or
// correction to the unit list, with the same duplicate-becomes-correction
// treatment as SubmitIngredientSuggestion.
func (s *Service) SubmitUnitSuggestion(ctx context.Context, userID string, req models.SubmitUnitSuggestionRequest) (models.ToolboxSuggestion, error) {
	submittedBy, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return models.ToolboxSuggestion{}, ErrForbidden
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxToolboxNameLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: name must be between 1 and %d characters", ErrInvalid, maxToolboxNameLength)
	}
	symbol := strings.TrimSpace(req.Symbol)
	if symbol == "" || len(symbol) > maxToolboxSymbolLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: symbol must be between 1 and %d characters", ErrInvalid, maxToolboxSymbolLength)
	}
	kind := strings.TrimSpace(strings.ToLower(req.Kind))
	if kind != models.ToolboxUnitWeight && kind != models.ToolboxUnitVolume {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: kind must be %q or %q", ErrInvalid, models.ToolboxUnitWeight, models.ToolboxUnitVolume)
	}
	if req.ToBase < minToolboxUnitToBase || req.ToBase > maxToolboxUnitToBase {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: conversion factor must be between %g and %g", ErrInvalid, minToolboxUnitToBase, maxToolboxUnitToBase)
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > maxToolboxNoteLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: note must be at most %d characters", ErrInvalid, maxToolboxNoteLength)
	}

	var targetID *primitive.ObjectID
	existing, err := s.db.GetToolboxUnitByNameLower(ctx, strings.ToLower(name))
	switch {
	case err == nil:
		targetID = existing.Id
	case errors.Is(err, mongorepo.ToolboxEntryNotFoundError):
		// no existing match - a new unit
	default:
		return models.ToolboxSuggestion{}, err
	}

	suggestion := models.ToolboxSuggestion{
		Kind: models.ToolboxSuggestionUnit, TargetID: targetID, SubmittedBy: submittedBy, Note: note,
		UnitName: name, UnitSymbol: symbol, UnitKind: kind, UnitToBase: req.ToBase,
	}
	return s.db.CreateToolboxSuggestion(ctx, suggestion)
}

// SubmitSubstitutionSuggestion validates and stores a user-proposed
// addition or correction to the substitutions list. The problem text is the
// matching key: an existing entry whose problem matches case-insensitively
// makes this a correction to that entry.
func (s *Service) SubmitSubstitutionSuggestion(ctx context.Context, userID string, req models.SubmitSubstitutionSuggestionRequest) (models.ToolboxSuggestion, error) {
	submittedBy, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return models.ToolboxSuggestion{}, ErrForbidden
	}
	problem := strings.TrimSpace(req.Problem)
	if problem == "" || len(problem) > maxToolboxTextLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: problem must be between 1 and %d characters", ErrInvalid, maxToolboxTextLength)
	}
	solution := strings.TrimSpace(req.Solution)
	if solution == "" || len(solution) > maxToolboxTextLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: solution must be between 1 and %d characters", ErrInvalid, maxToolboxTextLength)
	}
	tag := strings.TrimSpace(req.Tag)
	if len(tag) > maxToolboxNameLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: tag must be at most %d characters", ErrInvalid, maxToolboxNameLength)
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > maxToolboxNoteLength {
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: note must be at most %d characters", ErrInvalid, maxToolboxNoteLength)
	}

	var targetID *primitive.ObjectID
	existing, err := s.db.GetToolboxSubstitutionByProblemLower(ctx, strings.ToLower(problem))
	switch {
	case err == nil:
		targetID = existing.Id
	case errors.Is(err, mongorepo.ToolboxEntryNotFoundError):
		// no existing match - a new substitution
	default:
		return models.ToolboxSuggestion{}, err
	}

	suggestion := models.ToolboxSuggestion{
		Kind: models.ToolboxSuggestionSubstitution, TargetID: targetID, SubmittedBy: submittedBy, Note: note,
		SubProblem: problem, SubSolution: solution, SubTag: tag,
	}
	return s.db.CreateToolboxSuggestion(ctx, suggestion)
}

// ListToolboxSuggestionsForAdmin resolves each suggestion's submitter
// username and, for a correction, the entry's current name/problem, so the
// admin review tab can render what would be overwritten.
func (s *Service) ListToolboxSuggestionsForAdmin(ctx context.Context, status, kind string, limit, offset int64) (models.ListToolboxSuggestionsResponse, error) {
	suggestions, total, err := s.db.ListToolboxSuggestions(ctx, status, kind, limit, offset)
	if err != nil {
		return models.ListToolboxSuggestionsResponse{}, err
	}
	views := make([]models.ToolboxSuggestionView, 0, len(suggestions))
	if len(suggestions) == 0 {
		return models.ListToolboxSuggestionsResponse{Length: total, Items: views}, nil
	}

	userIDs := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		userIDs = append(userIDs, suggestion.SubmittedBy.Hex())
	}
	users, err := s.db.GetUsersByIDs(ctx, userIDs)
	if err != nil {
		return models.ListToolboxSuggestionsResponse{}, err
	}

	for _, suggestion := range suggestions {
		view := models.ToolboxSuggestionView{
			Id: suggestion.Id, Kind: suggestion.Kind, TargetID: suggestion.TargetID, IsCorrection: suggestion.TargetID != nil,
			SubmittedBy: suggestion.SubmittedBy, SubmittedByUsername: users[suggestion.SubmittedBy.Hex()].Username,
			Note: suggestion.Note, Status: suggestion.Status, CreatedAt: suggestion.CreatedAt, ReviewedAt: suggestion.ReviewedAt,
			IngredientName: suggestion.IngredientName, GPer100ml: suggestion.GPer100ml,
			UnitName: suggestion.UnitName, UnitSymbol: suggestion.UnitSymbol, UnitKind: suggestion.UnitKind, UnitToBase: suggestion.UnitToBase,
			SubProblem: suggestion.SubProblem, SubSolution: suggestion.SubSolution, SubTag: suggestion.SubTag,
		}
		if suggestion.TargetID != nil {
			view.CurrentName = s.currentToolboxEntryName(ctx, suggestion.Kind, suggestion.TargetID.Hex())
		}
		views = append(views, view)
	}
	return models.ListToolboxSuggestionsResponse{Length: total, Items: views}, nil
}

func (s *Service) currentToolboxEntryName(ctx context.Context, kind, id string) string {
	switch kind {
	case models.ToolboxSuggestionIngredient:
		if entry, err := s.db.GetToolboxIngredientById(ctx, id); err == nil {
			return entry.Name
		}
	case models.ToolboxSuggestionUnit:
		if entry, err := s.db.GetToolboxUnitById(ctx, id); err == nil {
			return entry.Name
		}
	case models.ToolboxSuggestionSubstitution:
		if entry, err := s.db.GetToolboxSubstitutionById(ctx, id); err == nil {
			return entry.Problem
		}
	}
	return ""
}

// ApproveToolboxSuggestion applies a submitted addition/correction to the
// live reference list: updates the target entry in place for a correction,
// otherwise inserts a new entry.
func (s *Service) ApproveToolboxSuggestion(ctx context.Context, id, adminID string) (models.ToolboxSuggestion, error) {
	suggestion, err := s.db.GetToolboxSuggestionById(ctx, id)
	if err != nil {
		if errors.Is(err, mongorepo.ToolboxSuggestionNotFoundError) {
			return models.ToolboxSuggestion{}, ErrToolboxSuggestionNotFound
		}
		return models.ToolboxSuggestion{}, err
	}

	switch suggestion.Kind {
	case models.ToolboxSuggestionIngredient:
		if suggestion.TargetID != nil {
			if _, err := s.db.UpdateToolboxIngredient(ctx, suggestion.TargetID.Hex(), suggestion.IngredientName, suggestion.GPer100ml, suggestion.Note); err != nil {
				return models.ToolboxSuggestion{}, toolboxEntryError(err)
			}
			s.scheduleToolboxTranslation(models.ToolboxSuggestionIngredient, *suggestion.TargetID)
		} else if entry, err := s.db.CreateToolboxIngredient(ctx, suggestion.IngredientName, suggestion.GPer100ml, suggestion.Note); err != nil {
			return models.ToolboxSuggestion{}, err
		} else if entry.Id != nil {
			s.scheduleToolboxTranslation(models.ToolboxSuggestionIngredient, *entry.Id)
		}
	case models.ToolboxSuggestionUnit:
		if suggestion.TargetID != nil {
			if _, err := s.db.UpdateToolboxUnit(ctx, suggestion.TargetID.Hex(), suggestion.UnitName, suggestion.UnitSymbol, suggestion.UnitKind, suggestion.UnitToBase); err != nil {
				return models.ToolboxSuggestion{}, toolboxEntryError(err)
			}
			s.scheduleToolboxTranslation(models.ToolboxSuggestionUnit, *suggestion.TargetID)
		} else if entry, err := s.db.CreateToolboxUnit(ctx, suggestion.UnitName, suggestion.UnitSymbol, suggestion.UnitKind, suggestion.UnitToBase); err != nil {
			return models.ToolboxSuggestion{}, err
		} else if entry.Id != nil {
			s.scheduleToolboxTranslation(models.ToolboxSuggestionUnit, *entry.Id)
		}
	case models.ToolboxSuggestionSubstitution:
		if suggestion.TargetID != nil {
			if _, err := s.db.UpdateToolboxSubstitution(ctx, suggestion.TargetID.Hex(), suggestion.SubProblem, suggestion.SubSolution, suggestion.SubTag); err != nil {
				return models.ToolboxSuggestion{}, toolboxEntryError(err)
			}
			s.scheduleToolboxTranslation(models.ToolboxSuggestionSubstitution, *suggestion.TargetID)
		} else if entry, err := s.db.CreateToolboxSubstitution(ctx, suggestion.SubProblem, suggestion.SubSolution, suggestion.SubTag); err != nil {
			return models.ToolboxSuggestion{}, err
		} else if entry.Id != nil {
			s.scheduleToolboxTranslation(models.ToolboxSuggestionSubstitution, *entry.Id)
		}
	default:
		return models.ToolboxSuggestion{}, fmt.Errorf("%w: unknown suggestion kind %q", ErrInvalid, suggestion.Kind)
	}

	if err := s.db.UpdateToolboxSuggestionStatus(ctx, id, models.ToolboxSuggestionApproved, adminID); err != nil {
		return models.ToolboxSuggestion{}, err
	}
	suggestion.Status = models.ToolboxSuggestionApproved
	return suggestion, nil
}

func toolboxEntryError(err error) error {
	if errors.Is(err, mongorepo.ToolboxEntryNotFoundError) {
		return ErrToolboxEntryNotFound
	}
	return err
}

// RejectToolboxSuggestion dismisses a submitted addition/correction with no
// further effect - no reason is recorded, matching translation suggestions'
// v1 scope.
func (s *Service) RejectToolboxSuggestion(ctx context.Context, id, adminID string) (models.ToolboxSuggestion, error) {
	suggestion, err := s.db.GetToolboxSuggestionById(ctx, id)
	if err != nil {
		if errors.Is(err, mongorepo.ToolboxSuggestionNotFoundError) {
			return models.ToolboxSuggestion{}, ErrToolboxSuggestionNotFound
		}
		return models.ToolboxSuggestion{}, err
	}
	if err := s.db.UpdateToolboxSuggestionStatus(ctx, id, models.ToolboxSuggestionRejected, adminID); err != nil {
		return models.ToolboxSuggestion{}, err
	}
	suggestion.Status = models.ToolboxSuggestionRejected
	return suggestion, nil
}
