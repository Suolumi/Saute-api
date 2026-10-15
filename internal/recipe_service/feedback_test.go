package recipe_service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"

	mongorepo "recipes/internal/database/mongo"
	"recipes/internal/models"
)

func (f *fakeStore) CreateFeedback(_ context.Context, feedback models.Feedback) (models.Feedback, error) {
	if f.feedback == nil {
		f.feedback = map[string]models.Feedback{}
	}
	id := primitive.NewObjectID()
	feedback.Id = &id
	feedback.Status = models.FeedbackOpen
	feedback.Voters = []primitive.ObjectID{feedback.Author}
	feedback.VoteCount = 1
	f.feedback[id.Hex()] = feedback
	return feedback, nil
}
func (f *fakeStore) GetFeedbackById(_ context.Context, id string) (models.Feedback, error) {
	feedback, ok := f.feedback[id]
	if !ok {
		return models.Feedback{}, mongorepo.FeedbackNotFoundError
	}
	return feedback, nil
}
func (f *fakeStore) ListFeedback(context.Context, models.ListFeedbackParams) ([]models.Feedback, int64, error) {
	return nil, 0, nil
}
func (f *fakeStore) UpdateFeedbackContent(_ context.Context, id string, content models.FeedbackContent) (models.Feedback, error) {
	feedback, ok := f.feedback[id]
	if !ok || feedback.Status != models.FeedbackOpen {
		return models.Feedback{}, mongorepo.FeedbackNotFoundError
	}
	feedback.Type, feedback.Title, feedback.Description = content.Type, content.Title, content.Description
	feedback.Photos, feedback.LinkPath, feedback.SearchText = content.Photos, content.LinkPath, content.SearchText
	f.feedback[id] = feedback
	return feedback, nil
}
func (f *fakeStore) DeleteFeedback(_ context.Context, id string) (models.Feedback, error) {
	feedback, ok := f.feedback[id]
	if !ok {
		return models.Feedback{}, mongorepo.FeedbackNotFoundError
	}
	delete(f.feedback, id)
	return feedback, nil
}
func (f *fakeStore) SetFeedbackVote(_ context.Context, id, userID string, vote bool) error {
	feedback := f.feedback[id]
	userObjectID, _ := primitive.ObjectIDFromHex(userID)
	voters := []primitive.ObjectID{}
	for _, voter := range feedback.Voters {
		if voter != userObjectID {
			voters = append(voters, voter)
		}
	}
	if vote {
		voters = append(voters, userObjectID)
	}
	feedback.Voters, feedback.VoteCount = voters, len(voters)
	f.feedback[id] = feedback
	return nil
}
func (f *fakeStore) SetFeedbackStatus(_ context.Context, id, status string) (models.Feedback, error) {
	feedback, ok := f.feedback[id]
	if !ok {
		return models.Feedback{}, mongorepo.FeedbackNotFoundError
	}
	feedback.Status = status
	f.feedback[id] = feedback
	return feedback, nil
}
func (f *fakeStore) SetFeedbackTranslation(_ context.Context, id string, updatedAt time.Time, sourceLocale, locale string, translation *models.FeedbackTranslation) error {
	feedback, ok := f.feedback[id]
	if !ok || !feedback.UpdatedAt.Equal(updatedAt) {
		return nil
	}
	feedback.SourceLocale = sourceLocale
	if translation != nil {
		if feedback.Translations == nil {
			feedback.Translations = map[string]models.FeedbackTranslation{}
		}
		feedback.Translations[locale] = *translation
	}
	f.feedback[id] = feedback
	return nil
}
func (f *fakeStore) MarkFeedbackAdminSeen(context.Context, string) error          { return nil }
func (f *fakeStore) MarkFeedbackAuthorSeen(context.Context, string, string) error { return nil }

func validFeedback() models.FeedbackRequest {
	return models.FeedbackRequest{Type: models.FeedbackBug, Title: "Timer doesn't ring", Description: "Nothing happens."}
}

func TestFoldSearchText(t *testing.T) {
	assert.Equal(t, "creme brulee", foldSearchText("Crème Brûlée"))
	assert.Equal(t, "paivan ruoka", foldSearchText("Päivän ruoka"))
	assert.Equal(t, "boeuf", foldSearchText("BŒUF"))
}

func TestNormalizeFeedbackLinkPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"  ", ""},
		{"/en/recipes/0123456789abcdef01234567", "/en/recipes/0123456789abcdef01234567"},
		{"/fr/toolbox?tab=oven#x", "/fr/toolbox"},
	} {
		got, err := normalizeFeedbackLinkPath(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}
	for _, in := range []string{
		"https://example.com/en/home",
		"//evil.example/en/home",
		"en/home",
		"javascript:alert(1)",
		"/\\evil.example",
	} {
		_, err := normalizeFeedbackLinkPath(in)
		assert.ErrorIs(t, err, ErrInvalid, in)
	}
}

func TestValidateFeedbackContent(t *testing.T) {
	for name, mutate := range map[string]func(*models.FeedbackRequest){
		"unknown type":      func(r *models.FeedbackRequest) { r.Type = "question" },
		"blank title":       func(r *models.FeedbackRequest) { r.Title = "   " },
		"blank description": func(r *models.FeedbackRequest) { r.Description = "" },
		"external link":     func(r *models.FeedbackRequest) { r.LinkPath = "https://example.com" },
	} {
		req := validFeedback()
		mutate(&req)
		_, err := validateFeedbackContent(req)
		assert.ErrorIs(t, err, ErrInvalid, name)
	}
	content, err := validateFeedbackContent(validFeedback())
	require.NoError(t, err)
	assert.Equal(t, "timer doesn't ring\nnothing happens.", content.SearchText)
}

func TestCreateFeedbackCountsAuthorVoteAndResolvesRecipeLink(t *testing.T) {
	recipeID := primitive.NewObjectID().Hex()
	store := &fakeStore{getRecipeTitlesFn: func(ids []string) (map[string]string, error) {
		return map[string]string{recipeID: "Lemon tart"}, nil
	}}
	s := &Service{db: store, imageDir: t.TempDir(), maxPictureBytes: 1 << 20}
	author := primitive.NewObjectID().Hex()
	req := validFeedback()
	req.LinkPath = "/en/recipes/" + recipeID

	view, err := s.CreateFeedback(context.Background(), author, req, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, view.VoteCount)
	assert.True(t, view.Voted)
	assert.True(t, view.Mine)
	assert.Equal(t, "Lemon tart", view.LinkTitle)
	assert.Equal(t, []string{}, view.Photos)

	_, err = s.CreateFeedback(context.Background(), author, validFeedback(), make([]PictureUpload, MaxFeedbackPhotos+1))
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestVoteFeedback(t *testing.T) {
	ctx := context.Background()
	s := &Service{db: &fakeStore{}, imageDir: t.TempDir()}
	author := primitive.NewObjectID().Hex()
	voter := primitive.NewObjectID().Hex()
	created, err := s.CreateFeedback(ctx, author, validFeedback(), nil)
	require.NoError(t, err)
	id := created.Id.Hex()

	view, err := s.VoteFeedback(ctx, id, voter, true)
	require.NoError(t, err)
	assert.Equal(t, 2, view.VoteCount)
	assert.True(t, view.Voted)

	view, err = s.VoteFeedback(ctx, id, voter, false)
	require.NoError(t, err)
	assert.Equal(t, 1, view.VoteCount)

	_, err = s.VoteFeedback(ctx, id, author, false)
	assert.ErrorIs(t, err, ErrInvalid)

	_, err = s.SetFeedbackStatus(ctx, id, models.FeedbackDone)
	require.NoError(t, err)
	_, err = s.VoteFeedback(ctx, id, voter, true)
	assert.ErrorIs(t, err, ErrFeedbackClosed)
}

func TestUpdateAndDeleteFeedbackPermissions(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	s := &Service{db: store, imageDir: t.TempDir()}
	author := primitive.NewObjectID().Hex()
	other := primitive.NewObjectID().Hex()
	created, err := s.CreateFeedback(ctx, author, validFeedback(), nil)
	require.NoError(t, err)
	id := created.Id.Hex()

	_, err = s.UpdateFeedback(ctx, id, other, validFeedback(), nil)
	assert.ErrorIs(t, err, ErrForbidden)

	withUnknownPhoto := validFeedback()
	withUnknownPhoto.Photos = []string{"someone-else.jpg"}
	_, err = s.UpdateFeedback(ctx, id, author, withUnknownPhoto, nil)
	assert.ErrorIs(t, err, ErrInvalid)

	edited := validFeedback()
	edited.Title = "Timer never rings"
	view, err := s.UpdateFeedback(ctx, id, author, edited, nil)
	require.NoError(t, err)
	assert.Equal(t, "Timer never rings", view.Title)

	assert.ErrorIs(t, s.DeleteFeedback(ctx, id, other, false), ErrForbidden)

	_, err = s.SetFeedbackStatus(ctx, id, models.FeedbackDeclined)
	require.NoError(t, err)
	_, err = s.UpdateFeedback(ctx, id, author, edited, nil)
	assert.ErrorIs(t, err, ErrFeedbackClosed)
	assert.ErrorIs(t, s.DeleteFeedback(ctx, id, author, false), ErrFeedbackClosed)
	assert.NoError(t, s.DeleteFeedback(ctx, id, other, true))
	assert.ErrorIs(t, s.DeleteFeedback(ctx, id, other, true), ErrFeedbackNotFound)
}

func TestFeedbackStatusAndTabValidation(t *testing.T) {
	s := &Service{db: &fakeStore{}}
	_, err := s.SetFeedbackStatus(context.Background(), primitive.NewObjectID().Hex(), "closed")
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = s.ListFeedbackForAdmin(context.Background(), "pending", 10, 0)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestUpdateFeedbackKeepsPhotosWhenOmitted(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	s := &Service{db: store, imageDir: t.TempDir()}
	author := primitive.NewObjectID().Hex()
	created, err := s.CreateFeedback(ctx, author, validFeedback(), nil)
	require.NoError(t, err)
	id := created.Id.Hex()
	stored := store.feedback[id]
	stored.Photos = []string{"a.jpg", "b.jpg"}
	store.feedback[id] = stored

	view, err := s.UpdateFeedback(ctx, id, author, validFeedback(), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.jpg", "b.jpg"}, view.Photos)

	keepOne := validFeedback()
	keepOne.Photos = []string{"b.jpg"}
	view, err = s.UpdateFeedback(ctx, id, author, keepOne, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"b.jpg"}, view.Photos)
}

func TestTranslateFeedbackOnDemandAndCached(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	detects, translations := 0, 0
	translator := &fakeTranslator{
		detectLocaleFn: func(string) (string, error) { detects++; return "fr", nil },
		translateTextsFn: func(texts []string, to string) ([]string, error) {
			translations++
			return []string{"EN " + texts[0], "EN " + texts[1]}, nil
		},
	}
	s := &Service{db: store, translator: translator, targetLocales: []string{"en", "fr", "fi"}, imageDir: t.TempDir()}
	created, err := s.CreateFeedback(ctx, primitive.NewObjectID().Hex(), validFeedback(), nil)
	require.NoError(t, err)
	id := created.Id.Hex()
	assert.Zero(t, detects+translations, "creating must not translate")

	got, err := s.TranslateFeedback(ctx, id, "en")
	require.NoError(t, err)
	assert.Equal(t, "EN Timer doesn't ring", got.Title)
	assert.Equal(t, "fr", got.SourceLocale)
	assert.False(t, got.SameLanguage)

	_, err = s.TranslateFeedback(ctx, id, "en")
	require.NoError(t, err)
	assert.Equal(t, 1, translations, "second request is served from the cache")

	same, err := s.TranslateFeedback(ctx, id, "fr-CA")
	require.NoError(t, err)
	assert.True(t, same.SameLanguage)
	assert.Equal(t, "Timer doesn't ring", same.Title)
	assert.Equal(t, 1, detects, "source locale is detected once")

	_, err = s.TranslateFeedback(ctx, id, "de")
	assert.ErrorIs(t, err, ErrInvalid)

	_, err = (&Service{db: store}).TranslateFeedback(ctx, id, "en")
	assert.ErrorIs(t, err, ErrTranslationUnavailable)
}
