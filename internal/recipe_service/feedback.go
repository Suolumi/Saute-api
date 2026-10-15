package recipe_service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/text/unicode/norm"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

const (
	maxFeedbackTitleLength       = 120
	maxFeedbackDescriptionLength = 5000
	maxFeedbackLinkLength        = 500
	// MaxFeedbackPhotos bounds how many photos one feedback can carry.
	MaxFeedbackPhotos = 3
)

var (
	ErrFeedbackNotFound = errors.New("feedback not found")
	// ErrFeedbackClosed guards the actions only an open feedback allows:
	// voting, and its author editing or deleting it.
	ErrFeedbackClosed = errors.New("feedback is closed")
)

// recipeLinkPattern matches a site path to a recipe page, e.g.
// /en/recipes/<id>, capturing the recipe id.
var recipeLinkPattern = regexp.MustCompile(`^/[A-Za-z-]+/recipes/([0-9a-f]{24})/?$`)

// ligatureFolder expands the ligatures NFD leaves intact, matching the
// site's highlight.ts so "oeuf" finds "œuf".
var ligatureFolder = strings.NewReplacer("œ", "oe", "æ", "ae")

// foldSearchText lowercases s, expands ligatures and strips accents, so
// "Crème" and "creme" match each other.
func foldSearchText(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(ligatureFolder.Replace(strings.ToLower(s))) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeFeedbackLinkPath accepts "" or a path on this site (the client
// strips the origin after checking it), and returns the path alone.
func normalizeFeedbackLinkPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxFeedbackLinkLength || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\x00") {
		return "", fmt.Errorf("%w: link must be a path on this site", ErrInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		return "", fmt.Errorf("%w: link must be a path on this site", ErrInvalid)
	}
	return parsed.Path, nil
}

func validateFeedbackContent(req models.FeedbackRequest) (models.FeedbackContent, error) {
	content := models.FeedbackContent{
		Type:        req.Type,
		Title:       strings.TrimSpace(req.Title),
		Description: strings.TrimSpace(req.Description),
	}
	if content.Type != models.FeedbackBug && content.Type != models.FeedbackIdea {
		return content, fmt.Errorf("%w: type must be %q or %q", ErrInvalid, models.FeedbackBug, models.FeedbackIdea)
	}
	if content.Title == "" || len([]rune(content.Title)) > maxFeedbackTitleLength {
		return content, fmt.Errorf("%w: title must be between 1 and %d characters", ErrInvalid, maxFeedbackTitleLength)
	}
	if content.Description == "" || len([]rune(content.Description)) > maxFeedbackDescriptionLength {
		return content, fmt.Errorf("%w: description must be between 1 and %d characters", ErrInvalid, maxFeedbackDescriptionLength)
	}
	linkPath, err := normalizeFeedbackLinkPath(req.LinkPath)
	if err != nil {
		return content, err
	}
	content.LinkPath = linkPath
	content.SearchText = foldSearchText(content.Title + "\n" + content.Description)
	return content, nil
}

func feedbackError(err error) error {
	if errors.Is(err, mongorepo.FeedbackNotFoundError) {
		return ErrFeedbackNotFound
	}
	return err
}

// CreateFeedback validates and stores a new feedback from userID, with up to
// MaxFeedbackPhotos photos.
func (s *Service) CreateFeedback(ctx context.Context, userID string, req models.FeedbackRequest, photos []PictureUpload) (models.FeedbackView, error) {
	authorID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return models.FeedbackView{}, ErrForbidden
	}
	content, err := validateFeedbackContent(req)
	if err != nil {
		return models.FeedbackView{}, err
	}
	if len(photos) > MaxFeedbackPhotos {
		return models.FeedbackView{}, fmt.Errorf("%w: at most %d photos", ErrInvalid, MaxFeedbackPhotos)
	}
	normalized, err := s.normalizePictures(photos)
	if err != nil {
		return models.FeedbackView{}, err
	}
	written, err := s.writePictures(normalized)
	if err != nil {
		return models.FeedbackView{}, err
	}
	created, err := s.db.CreateFeedback(ctx, models.Feedback{
		Type: content.Type, Title: content.Title, Description: content.Description,
		Photos: written, LinkPath: content.LinkPath, SearchText: content.SearchText, Author: authorID,
	})
	if err != nil {
		s.removePictures(written)
		return models.FeedbackView{}, err
	}
	views, err := s.feedbackViews(ctx, []models.Feedback{created}, userID, false)
	if err != nil {
		return models.FeedbackView{}, err
	}
	return views[0], nil
}

// GetFeedback returns one feedback as seen by viewerID ("" when anonymous).
func (s *Service) GetFeedback(ctx context.Context, id, viewerID string) (models.FeedbackView, error) {
	feedback, err := s.db.GetFeedbackById(ctx, id)
	if err != nil {
		return models.FeedbackView{}, feedbackError(err)
	}
	views, err := s.feedbackViews(ctx, []models.Feedback{feedback}, viewerID, false)
	if err != nil {
		return models.FeedbackView{}, err
	}
	return views[0], nil
}

// ListFeedback returns one page of the public board as seen by viewerID.
// params.Query is folded here, so callers pass the raw search text.
func (s *Service) ListFeedback(ctx context.Context, viewerID string, params models.ListFeedbackParams) (models.ListFeedbackResponse, error) {
	params.Query = foldSearchText(strings.TrimSpace(params.Query))
	return s.listFeedback(ctx, viewerID, params, false)
}

func (s *Service) listFeedback(ctx context.Context, viewerID string, params models.ListFeedbackParams, admin bool) (models.ListFeedbackResponse, error) {
	items, total, err := s.db.ListFeedback(ctx, params)
	if err != nil {
		return models.ListFeedbackResponse{}, err
	}
	views, err := s.feedbackViews(ctx, items, viewerID, admin)
	if err != nil {
		return models.ListFeedbackResponse{}, err
	}
	return models.ListFeedbackResponse{Length: total, Items: views}, nil
}

// FeedbackUpdates lists userID's own feedback whose closing they haven't seen.
func (s *Service) FeedbackUpdates(ctx context.Context, userID string) (models.ListFeedbackResponse, error) {
	return s.listFeedback(ctx, userID, models.ListFeedbackParams{AuthorID: userID, AuthorUnread: true}, false)
}

// MarkFeedbackSeen clears the "status changed" flag on userID's own feedback.
func (s *Service) MarkFeedbackSeen(ctx context.Context, id, userID string) error {
	return feedbackError(s.db.MarkFeedbackAuthorSeen(ctx, id, userID))
}

// UpdateFeedback lets the author edit an open feedback. req.Photos lists the
// stored photos to keep (nil keeps them all); newPhotos are appended after
// them.
func (s *Service) UpdateFeedback(ctx context.Context, id, userID string, req models.FeedbackRequest, newPhotos []PictureUpload) (models.FeedbackView, error) {
	existing, err := s.db.GetFeedbackById(ctx, id)
	if err != nil {
		return models.FeedbackView{}, feedbackError(err)
	}
	if existing.Author.Hex() != userID {
		return models.FeedbackView{}, ErrForbidden
	}
	if existing.Status != models.FeedbackOpen {
		return models.FeedbackView{}, ErrFeedbackClosed
	}
	content, err := validateFeedbackContent(req)
	if err != nil {
		return models.FeedbackView{}, err
	}
	if req.Photos == nil {
		req.Photos = existing.Photos
	}
	kept := make([]string, 0, len(req.Photos))
	for _, photo := range req.Photos {
		if !slices.Contains(existing.Photos, photo) {
			return models.FeedbackView{}, fmt.Errorf("%w: unknown photo %q", ErrInvalid, photo)
		}
		if !slices.Contains(kept, photo) {
			kept = append(kept, photo)
		}
	}
	if len(kept)+len(newPhotos) > MaxFeedbackPhotos {
		return models.FeedbackView{}, fmt.Errorf("%w: at most %d photos", ErrInvalid, MaxFeedbackPhotos)
	}
	normalized, err := s.normalizePictures(newPhotos)
	if err != nil {
		return models.FeedbackView{}, err
	}
	written, err := s.writePictures(normalized)
	if err != nil {
		return models.FeedbackView{}, err
	}
	content.Photos = append(kept, written...)
	updated, err := s.db.UpdateFeedbackContent(ctx, id, content)
	if err != nil {
		s.removePictures(written)
		if errors.Is(err, mongorepo.FeedbackNotFoundError) {
			// It existed and was open a moment ago, so an admin closed it.
			return models.FeedbackView{}, ErrFeedbackClosed
		}
		return models.FeedbackView{}, err
	}
	var dropped []string
	for _, photo := range existing.Photos {
		if !slices.Contains(content.Photos, photo) {
			dropped = append(dropped, photo)
		}
	}
	s.removePictures(dropped)
	views, err := s.feedbackViews(ctx, []models.Feedback{updated}, userID, false)
	if err != nil {
		return models.FeedbackView{}, err
	}
	return views[0], nil
}

// DeleteFeedback removes a feedback and its photos. Its author may delete it
// while it is open; an admin may delete any.
func (s *Service) DeleteFeedback(ctx context.Context, id, userID string, admin bool) error {
	existing, err := s.db.GetFeedbackById(ctx, id)
	if err != nil {
		return feedbackError(err)
	}
	if !admin {
		if existing.Author.Hex() != userID {
			return ErrForbidden
		}
		if existing.Status != models.FeedbackOpen {
			return ErrFeedbackClosed
		}
	}
	deleted, err := s.db.DeleteFeedback(ctx, id)
	if err != nil {
		return feedbackError(err)
	}
	s.removePictures(deleted.Photos)
	return nil
}

// VoteFeedback adds (vote) or removes userID's upvote on an open feedback.
// An author can't withdraw the vote their own post carries.
func (s *Service) VoteFeedback(ctx context.Context, id, userID string, vote bool) (models.FeedbackView, error) {
	existing, err := s.db.GetFeedbackById(ctx, id)
	if err != nil {
		return models.FeedbackView{}, feedbackError(err)
	}
	if existing.Status != models.FeedbackOpen {
		return models.FeedbackView{}, ErrFeedbackClosed
	}
	if !vote && existing.Author.Hex() == userID {
		return models.FeedbackView{}, fmt.Errorf("%w: you can't remove the vote on your own feedback", ErrInvalid)
	}
	if err := s.db.SetFeedbackVote(ctx, id, userID, vote); err != nil {
		return models.FeedbackView{}, feedbackError(err)
	}
	return s.GetFeedback(ctx, id, userID)
}

// ListFeedbackForAdmin returns one page of the admin queue. tab is "new"
// (not yet opened by an admin), a status, or "all".
func (s *Service) ListFeedbackForAdmin(ctx context.Context, tab string, limit, offset int64) (models.ListFeedbackResponse, error) {
	params := models.ListFeedbackParams{Limit: limit, Offset: offset}
	switch tab {
	case "new":
		params.AdminUnseen = true
	case models.FeedbackOpen, models.FeedbackDone, models.FeedbackDeclined:
		params.Status = tab
	case "all", "":
	default:
		return models.ListFeedbackResponse{}, fmt.Errorf("%w: unknown tab %q", ErrInvalid, tab)
	}
	return s.listFeedback(ctx, "", params, true)
}

// SetFeedbackStatus is the admin action that marks a feedback done, declined,
// or open again. Votes are kept either way.
func (s *Service) SetFeedbackStatus(ctx context.Context, id, status string) (models.FeedbackView, error) {
	if status != models.FeedbackOpen && status != models.FeedbackDone && status != models.FeedbackDeclined {
		return models.FeedbackView{}, fmt.Errorf("%w: unknown status %q", ErrInvalid, status)
	}
	updated, err := s.db.SetFeedbackStatus(ctx, id, status)
	if err != nil {
		return models.FeedbackView{}, feedbackError(err)
	}
	views, err := s.feedbackViews(ctx, []models.Feedback{updated}, "", true)
	if err != nil {
		return models.FeedbackView{}, err
	}
	return views[0], nil
}

// MarkFeedbackAdminSeen takes a feedback out of the admin "New" tab.
func (s *Service) MarkFeedbackAdminSeen(ctx context.Context, id string) error {
	return feedbackError(s.db.MarkFeedbackAdminSeen(ctx, id))
}

// feedbackViews resolves authors and recipe link titles for items in two
// batched lookups, and reports viewerID's own votes and items.
func (s *Service) feedbackViews(ctx context.Context, items []models.Feedback, viewerID string, admin bool) ([]models.FeedbackView, error) {
	views := make([]models.FeedbackView, 0, len(items))
	if len(items) == 0 {
		return views, nil
	}
	authorIDs := make([]string, 0, len(items))
	var recipeIDs []string
	for _, item := range items {
		authorIDs = append(authorIDs, item.Author.Hex())
		if match := recipeLinkPattern.FindStringSubmatch(item.LinkPath); match != nil {
			recipeIDs = append(recipeIDs, match[1])
		}
	}
	users, err := s.db.GetUsersByIDs(ctx, authorIDs)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	if len(recipeIDs) > 0 {
		if titles, err = s.db.GetRecipeTitles(ctx, recipeIDs); err != nil {
			return nil, err
		}
	}
	for _, item := range items {
		mine := viewerID != "" && item.Author.Hex() == viewerID
		view := models.FeedbackView{
			Id: item.Id, Type: item.Type, Title: item.Title, Description: item.Description,
			Photos: item.Photos, LinkPath: item.LinkPath, Status: item.Status, VoteCount: item.VoteCount,
			Mine: mine, Unread: mine && item.AuthorUnread,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, ClosedAt: item.ClosedAt,
		}
		if view.Photos == nil {
			view.Photos = []string{}
		}
		if author, ok := users[item.Author.Hex()]; ok {
			view.Author = &author
		}
		if match := recipeLinkPattern.FindStringSubmatch(item.LinkPath); match != nil {
			view.LinkTitle = titles[match[1]]
		}
		if viewerID != "" {
			view.Voted = slices.ContainsFunc(item.Voters, func(id primitive.ObjectID) bool { return id.Hex() == viewerID })
		}
		if admin {
			seen := item.AdminSeen
			view.AdminSeen = &seen
		}
		views = append(views, view)
	}
	return views, nil
}
