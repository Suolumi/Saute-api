package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
)

func (c *Client) ReferencedPictures(ctx context.Context) (map[string]struct{}, error) {
	references := make(map[string]struct{})
	users, err := c.db.Collection(userCollection).Find(ctx, bson.D{}, options.Find().SetProjection(bson.D{{Key: "picture", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var userPictures []struct {
		Picture string `bson:"picture"`
	}
	if err := users.All(ctx, &userPictures); err != nil {
		return nil, err
	}
	for _, picture := range userPictures {
		if picture.Picture != "" {
			references[picture.Picture] = struct{}{}
		}
	}

	recipes, err := c.db.Collection(recipesCollection).Find(ctx, bson.D{}, options.Find().SetProjection(bson.D{{Key: "pictures", Value: 1}, {Key: "steps.picture", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var recipePictures []struct {
		Pictures []models.RecipePicture `bson:"pictures"`
		Steps    []struct {
			Picture string `bson:"picture"`
		} `bson:"steps"`
	}
	if err := recipes.All(ctx, &recipePictures); err != nil {
		return nil, err
	}
	for _, recipe := range recipePictures {
		for _, picture := range recipe.Pictures {
			references[picture.Filename] = struct{}{}
		}
		for _, step := range recipe.Steps {
			if step.Picture != "" {
				references[step.Picture] = struct{}{}
			}
		}
	}
	return references, nil
}
