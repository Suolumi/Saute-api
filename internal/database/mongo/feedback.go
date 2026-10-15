package mongo

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

const feedbackCollection = "feedback"

var FeedbackNotFoundError = errors.New("feedback not found")

// CreateFeedback inserts a new open feedback, stamping the server-side fields
// regardless of what the caller set. The author's vote is included.
func (c *Client) CreateFeedback(ctx context.Context, feedback models.Feedback) (models.Feedback, error) {
	now := time.Now().UTC()
	feedback.Id = nil
	feedback.Status = models.FeedbackOpen
	feedback.Voters = []primitive.ObjectID{feedback.Author}
	feedback.VoteCount = 1
	feedback.AdminSeen = false
	feedback.AuthorUnread = false
	feedback.CreatedAt = now
	feedback.UpdatedAt = now
	feedback.ClosedAt = nil
	if feedback.Photos == nil {
		feedback.Photos = []string{}
	}
	result, err := c.db.Collection(feedbackCollection).InsertOne(ctx, feedback)
	if err != nil {
		return models.Feedback{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	feedback.Id = &id
	return feedback, nil
}

func (c *Client) GetFeedbackById(ctx context.Context, id string) (models.Feedback, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Feedback{}, FeedbackNotFoundError
	}
	var feedback models.Feedback
	err = c.db.Collection(feedbackCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&feedback)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.Feedback{}, FeedbackNotFoundError
	}
	return feedback, err
}

func feedbackFilter(params models.ListFeedbackParams) (bson.D, error) {
	filter := bson.D{}
	switch params.Status {
	case "":
	case "closed":
		filter = append(filter, bson.E{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{models.FeedbackDone, models.FeedbackDeclined}}}})
	default:
		filter = append(filter, bson.E{Key: "status", Value: params.Status})
	}
	if params.Type != "" {
		filter = append(filter, bson.E{Key: "type", Value: params.Type})
	}
	if params.Query != "" {
		filter = append(filter, bson.E{Key: "search_text", Value: primitive.Regex{Pattern: regexp.QuoteMeta(params.Query)}})
	}
	if params.AuthorID != "" {
		authorID, err := primitive.ObjectIDFromHex(params.AuthorID)
		if err != nil {
			return nil, err
		}
		filter = append(filter, bson.E{Key: "author", Value: authorID})
	}
	if params.AuthorUnread {
		filter = append(filter, bson.E{Key: "author_unread", Value: true})
	}
	if params.AdminUnseen {
		filter = append(filter, bson.E{Key: "admin_seen", Value: false})
	}
	return filter, nil
}

// ListFeedback returns one page of feedback matching params, most voted or
// newest first, plus the total match count. limit/offset <= 0 are ignored.
func (c *Client) ListFeedback(ctx context.Context, params models.ListFeedbackParams) ([]models.Feedback, int64, error) {
	filter, err := feedbackFilter(params)
	if err != nil {
		return nil, 0, err
	}
	collection := c.db.Collection(feedbackCollection)
	count, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	sort := bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}
	switch {
	case params.SortByVotes:
		sort = append(bson.D{{Key: "vote_count", Value: -1}}, sort...)
	case params.Status == "closed" || params.Status == models.FeedbackDone || params.Status == models.FeedbackDeclined:
		sort = append(bson.D{{Key: "closed_at", Value: -1}}, sort...)
	}
	findOptions := options.Find().SetSort(sort)
	if params.Limit > 0 {
		findOptions.SetLimit(params.Limit)
	}
	if params.Offset > 0 {
		findOptions.SetSkip(params.Offset)
	}
	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, 0, err
	}
	items := []models.Feedback{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, 0, err
	}
	return items, count, nil
}

// UpdateFeedbackContent replaces the editable fields of an open feedback.
// FeedbackNotFoundError means no open feedback with that id exists.
func (c *Client) UpdateFeedbackContent(ctx context.Context, id string, content models.FeedbackContent) (models.Feedback, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Feedback{}, FeedbackNotFoundError
	}
	if content.Photos == nil {
		content.Photos = []string{}
	}
	var updated models.Feedback
	err = c.db.Collection(feedbackCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: objectID}, {Key: "status", Value: models.FeedbackOpen}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "type", Value: content.Type},
			{Key: "title", Value: content.Title},
			{Key: "description", Value: content.Description},
			{Key: "photos", Value: content.Photos},
			{Key: "link_path", Value: content.LinkPath},
			{Key: "search_text", Value: content.SearchText},
			{Key: "updated_at", Value: time.Now().UTC()},
		}}, {Key: "$unset", Value: bson.D{
			{Key: "source_locale", Value: ""},
			{Key: "translations", Value: ""},
		}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.Feedback{}, FeedbackNotFoundError
	}
	return updated, err
}

func (c *Client) DeleteFeedback(ctx context.Context, id string) (models.Feedback, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Feedback{}, FeedbackNotFoundError
	}
	var deleted models.Feedback
	err = c.db.Collection(feedbackCollection).FindOneAndDelete(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&deleted)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.Feedback{}, FeedbackNotFoundError
	}
	return deleted, err
}

// SetFeedbackVote adds or removes userID's vote on an open feedback. The
// filter makes it idempotent, so the count never drifts from the voter list.
func (c *Client) SetFeedbackVote(ctx context.Context, id, userID string, vote bool) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return FeedbackNotFoundError
	}
	userObjectID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return err
	}
	filter := bson.D{{Key: "_id", Value: objectID}, {Key: "status", Value: models.FeedbackOpen}}
	var update bson.D
	if vote {
		filter = append(filter, bson.E{Key: "voters", Value: bson.D{{Key: "$ne", Value: userObjectID}}})
		update = bson.D{{Key: "$push", Value: bson.D{{Key: "voters", Value: userObjectID}}}, {Key: "$inc", Value: bson.D{{Key: "vote_count", Value: 1}}}}
	} else {
		filter = append(filter, bson.E{Key: "voters", Value: userObjectID})
		update = bson.D{{Key: "$pull", Value: bson.D{{Key: "voters", Value: userObjectID}}}, {Key: "$inc", Value: bson.D{{Key: "vote_count", Value: -1}}}}
	}
	_, err = c.db.Collection(feedbackCollection).UpdateOne(ctx, filter, update)
	return err
}

// SetFeedbackStatus moves a feedback to status. Closing flags it unread for
// its author; reopening clears that flag and the close date.
func (c *Client) SetFeedbackStatus(ctx context.Context, id, status string) (models.Feedback, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.Feedback{}, FeedbackNotFoundError
	}
	now := time.Now().UTC()
	set := bson.D{
		{Key: "status", Value: status},
		{Key: "admin_seen", Value: true},
		{Key: "author_unread", Value: status != models.FeedbackOpen},
		{Key: "updated_at", Value: now},
	}
	var update bson.D
	if status == models.FeedbackOpen {
		update = bson.D{{Key: "$set", Value: set}, {Key: "$unset", Value: bson.D{{Key: "closed_at", Value: ""}}}}
	} else {
		update = bson.D{{Key: "$set", Value: append(set, bson.E{Key: "closed_at", Value: now})}}
	}
	var updated models.Feedback
	err = c.db.Collection(feedbackCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: objectID}}, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.Feedback{}, FeedbackNotFoundError
	}
	return updated, err
}

func (c *Client) MarkFeedbackAdminSeen(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return FeedbackNotFoundError
	}
	_, err = c.db.Collection(feedbackCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "admin_seen", Value: true}}}},
	)
	return err
}

// MarkFeedbackAuthorSeen clears the unread flag on authorID's own feedback;
// it is a no-op on anyone else's.
func (c *Client) MarkFeedbackAuthorSeen(ctx context.Context, id, authorID string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return FeedbackNotFoundError
	}
	authorObjectID, err := primitive.ObjectIDFromHex(authorID)
	if err != nil {
		return err
	}
	_, err = c.db.Collection(feedbackCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}, {Key: "author", Value: authorObjectID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "author_unread", Value: false}}}},
	)
	return err
}

// SetFeedbackTranslation stores the detected source locale and, when
// translation is non-nil, the translation into locale. It only applies if
// the feedback hasn't been edited since updatedAt, so a translation of
// outdated text is never cached.
func (c *Client) SetFeedbackTranslation(ctx context.Context, id string, updatedAt time.Time, sourceLocale, locale string, translation *models.FeedbackTranslation) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return FeedbackNotFoundError
	}
	set := bson.D{{Key: "source_locale", Value: sourceLocale}}
	if translation != nil {
		set = append(set, bson.E{Key: "translations." + locale, Value: translation})
	}
	_, err = c.db.Collection(feedbackCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}, {Key: "updated_at", Value: updatedAt}},
		bson.D{{Key: "$set", Value: set}},
	)
	return err
}
