package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

const translationSuggestionsCollection = "recipe_translation_suggestions"

var TranslationSuggestionNotFoundError = errors.New("translation suggestion not found")

// CreateTranslationSuggestion inserts a new pending suggestion, stamping
// Status/CreatedAt server-side regardless of what the caller set.
func (c *Client) CreateTranslationSuggestion(ctx context.Context, suggestion models.TranslationSuggestion) (models.TranslationSuggestion, error) {
	suggestion.Id = nil
	suggestion.Status = models.TranslationSuggestionPending
	suggestion.CreatedAt = time.Now().UTC()
	suggestion.ReviewedAt = nil
	suggestion.ReviewedBy = nil
	result, err := c.db.Collection(translationSuggestionsCollection).InsertOne(ctx, suggestion)
	if err != nil {
		return models.TranslationSuggestion{}, err
	}
	id := result.InsertedID.(primitive.ObjectID)
	suggestion.Id = &id
	return suggestion, nil
}

func (c *Client) GetTranslationSuggestionById(ctx context.Context, id string) (models.TranslationSuggestion, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.TranslationSuggestion{}, TranslationSuggestionNotFoundError
	}
	var suggestion models.TranslationSuggestion
	err = c.db.Collection(translationSuggestionsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: objectID}}).Decode(&suggestion)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.TranslationSuggestion{}, TranslationSuggestionNotFoundError
		}
		return models.TranslationSuggestion{}, err
	}
	return suggestion, nil
}

// ListTranslationSuggestions returns a page of suggestions, newest first,
// optionally filtered by status ("" means every status). limit/offset <= 0
// are ignored (no limit/no skip).
func (c *Client) ListTranslationSuggestions(ctx context.Context, status string, limit, offset int64) ([]models.TranslationSuggestion, int64, error) {
	filter := bson.D{}
	if status != "" {
		filter = bson.D{{Key: "status", Value: status}}
	}
	collection := c.db.Collection(translationSuggestionsCollection)
	count, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	findOptions := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	if limit > 0 {
		findOptions.SetLimit(limit)
	}
	if offset > 0 {
		findOptions.SetSkip(offset)
	}
	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, 0, err
	}
	suggestions := []models.TranslationSuggestion{}
	if err := cursor.All(ctx, &suggestions); err != nil {
		return nil, 0, err
	}
	return suggestions, count, nil
}

// UpdateTranslationSuggestionStatus transitions a suggestion to a terminal (or
// stale) status, stamping who reviewed it and when. reviewedBy may be empty
// (a system-detected staleness has no reviewing admin).
func (c *Client) UpdateTranslationSuggestionStatus(ctx context.Context, id, status, reviewedBy string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return TranslationSuggestionNotFoundError
	}
	set := bson.D{
		{Key: "status", Value: status},
		{Key: "reviewed_at", Value: time.Now().UTC()},
	}
	if reviewedBy != "" {
		if reviewerID, err := primitive.ObjectIDFromHex(reviewedBy); err == nil {
			set = append(set, bson.E{Key: "reviewed_by", Value: reviewerID})
		}
	}
	result, err := c.db.Collection(translationSuggestionsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: objectID}},
		bson.D{{Key: "$set", Value: set}},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return TranslationSuggestionNotFoundError
	}
	return nil
}

// HasPendingTranslationSuggestion reports whether userID already has an
// unreviewed suggestion for this (recipe, locale) pair.
func (c *Client) HasPendingTranslationSuggestion(ctx context.Context, recipeID, locale, userID string) (bool, error) {
	recipeObjectID, err := primitive.ObjectIDFromHex(recipeID)
	if err != nil {
		return false, err
	}
	userObjectID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return false, err
	}
	count, err := c.db.Collection(translationSuggestionsCollection).CountDocuments(ctx, bson.D{
		{Key: "recipe_id", Value: recipeObjectID},
		{Key: "locale", Value: locale},
		{Key: "submitted_by", Value: userObjectID},
		{Key: "status", Value: models.TranslationSuggestionPending},
	})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
