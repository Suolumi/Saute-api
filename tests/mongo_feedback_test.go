package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"recipes/internal/models"
)

func createTestFeedback(t *testing.T, ctx context.Context, db interface {
	CreateFeedback(context.Context, models.Feedback) (models.Feedback, error)
}, feedback models.Feedback) string {
	t.Helper()
	created, err := db.CreateFeedback(ctx, feedback)
	require.NoError(t, err)
	return created.Id.Hex()
}

// TestFeedbackVotesStayConsistent verifies voting is idempotent, the count
// always matches the voter list, and a closed item can't be voted on.
func TestFeedbackVotesStayConsistent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "feedback-author")
	voter := insertTestUser(t, db, ctx, "feedback-voter")
	id := createTestFeedback(t, ctx, db, models.Feedback{Type: models.FeedbackBug, Title: "Bug", Description: "d", Author: author})

	require.NoError(t, db.SetFeedbackVote(ctx, id, voter.Hex(), true))
	require.NoError(t, db.SetFeedbackVote(ctx, id, voter.Hex(), true))
	got, err := db.GetFeedbackById(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 2, got.VoteCount)
	assert.Len(t, got.Voters, 2)

	require.NoError(t, db.SetFeedbackVote(ctx, id, voter.Hex(), false))
	require.NoError(t, db.SetFeedbackVote(ctx, id, voter.Hex(), false))
	got, err = db.GetFeedbackById(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 1, got.VoteCount)

	_, err = db.SetFeedbackStatus(ctx, id, models.FeedbackDone)
	require.NoError(t, err)
	require.NoError(t, db.SetFeedbackVote(ctx, id, voter.Hex(), true))
	got, err = db.GetFeedbackById(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 1, got.VoteCount)
}

// TestFeedbackStatusFlags verifies closing flags the item unread for its
// author and seen for admins, and reopening clears the close date.
func TestFeedbackStatusFlags(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "feedback-status-author")
	id := createTestFeedback(t, ctx, db, models.Feedback{Type: models.FeedbackIdea, Title: "Idea", Description: "d", Author: author})

	unseen, total, err := db.ListFeedback(ctx, models.ListFeedbackParams{AdminUnseen: true})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Len(t, unseen, 1)

	closed, err := db.SetFeedbackStatus(ctx, id, models.FeedbackDeclined)
	require.NoError(t, err)
	assert.True(t, closed.AuthorUnread)
	assert.True(t, closed.AdminSeen)
	assert.NotNil(t, closed.ClosedAt)

	_, total, err = db.ListFeedback(ctx, models.ListFeedbackParams{AuthorID: author.Hex(), AuthorUnread: true})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.NoError(t, db.MarkFeedbackAuthorSeen(ctx, id, author.Hex()))
	_, total, err = db.ListFeedback(ctx, models.ListFeedbackParams{AuthorID: author.Hex(), AuthorUnread: true})
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)

	reopened, err := db.SetFeedbackStatus(ctx, id, models.FeedbackOpen)
	require.NoError(t, err)
	assert.False(t, reopened.AuthorUnread)
	assert.Nil(t, reopened.ClosedAt)
	assert.Equal(t, 1, reopened.VoteCount)
}

// TestListFeedbackFiltersAndSorts covers the board's status, type, search and
// sort options.
func TestListFeedbackFiltersAndSorts(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "feedback-list-author")
	voter := insertTestUser(t, db, ctx, "feedback-list-voter")
	older := createTestFeedback(t, ctx, db, models.Feedback{Type: models.FeedbackBug, Title: "Older", Description: "d", SearchText: "creme brulee", Author: author})
	newer := createTestFeedback(t, ctx, db, models.Feedback{Type: models.FeedbackIdea, Title: "Newer", Description: "d", SearchText: "timer", Author: author})
	closed := createTestFeedback(t, ctx, db, models.Feedback{Type: models.FeedbackBug, Title: "Closed", Description: "d", Author: author})
	require.NoError(t, db.SetFeedbackVote(ctx, older, voter.Hex(), true))
	_, err := db.SetFeedbackStatus(ctx, closed, models.FeedbackDone)
	require.NoError(t, err)

	ids := func(params models.ListFeedbackParams) []string {
		items, _, err := db.ListFeedback(ctx, params)
		require.NoError(t, err)
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.Id.Hex())
		}
		return out
	}
	assert.Equal(t, []string{older, newer}, ids(models.ListFeedbackParams{Status: models.FeedbackOpen, SortByVotes: true}))
	assert.Equal(t, []string{newer, older}, ids(models.ListFeedbackParams{Status: models.FeedbackOpen}))
	assert.Equal(t, []string{closed}, ids(models.ListFeedbackParams{Status: "closed"}))
	assert.Equal(t, []string{newer}, ids(models.ListFeedbackParams{Type: models.FeedbackIdea}))
	assert.Equal(t, []string{older}, ids(models.ListFeedbackParams{Query: "brulee"}))
	assert.Empty(t, ids(models.ListFeedbackParams{Query: "brul.e"}))
}

// TestReferencedPicturesIncludesFeedbackPhotos guards the image cleanup sweep
// against deleting feedback photos.
func TestReferencedPicturesIncludesFeedbackPhotos(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "feedback-photo-author")
	createTestFeedback(t, ctx, db, models.Feedback{Type: models.FeedbackBug, Title: "Photo", Description: "d", Photos: []string{"feedback-photo.jpg"}, Author: author})

	references, err := db.ReferencedPictures(ctx)
	require.NoError(t, err)
	assert.Contains(t, references, "feedback-photo.jpg")
}

// TestFeedbackTranslationCache verifies a translation is cached, never stored
// over text edited since it was read, and cleared by an edit.
func TestFeedbackTranslationCache(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	author := insertTestUser(t, db, ctx, "feedback-translation-author")
	id := createTestFeedback(t, ctx, db, models.Feedback{Type: models.FeedbackBug, Title: "Minuteur", Description: "Il ne sonne pas", Author: author})
	original, err := db.GetFeedbackById(ctx, id)
	require.NoError(t, err)

	translation := &models.FeedbackTranslation{Title: "Timer", Description: "It doesn't ring"}
	require.NoError(t, db.SetFeedbackTranslation(ctx, id, original.UpdatedAt, "fr", "en", translation))
	got, err := db.GetFeedbackById(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "fr", got.SourceLocale)
	assert.Equal(t, *translation, got.Translations["en"])

	edited, err := db.UpdateFeedbackContent(ctx, id, models.FeedbackContent{Type: models.FeedbackBug, Title: "Minuteur muet", Description: "Il ne sonne jamais"})
	require.NoError(t, err)
	assert.Empty(t, edited.SourceLocale)
	assert.Empty(t, edited.Translations)

	require.NoError(t, db.SetFeedbackTranslation(ctx, id, original.UpdatedAt, "fr", "en", translation))
	got, err = db.GetFeedbackById(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, got.Translations, "a translation of the pre-edit text must not be stored")
}
