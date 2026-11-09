package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MigrateRecipeFamilies gives every recipe without a family_id its family: the
// root's id for a variation, its own id otherwise. It then drops the legacy
// variation_of field and its index. Idempotent.
func (c *Client) MigrateRecipeFamilies(ctx context.Context) error {
	recipes := c.db.Collection(recipesCollection)
	_, err := recipes.UpdateMany(ctx,
		bson.M{"family_id": bson.M{"$exists": false}},
		bson.A{bson.M{"$set": bson.M{"family_id": bson.M{"$ifNull": bson.A{"$variation_of", "$_id"}}}}},
	)
	if err != nil {
		return fmt.Errorf("migrate recipe families: %w", err)
	}
	if _, err := recipes.UpdateMany(ctx,
		bson.M{"variation_of": bson.M{"$exists": true}},
		bson.M{"$unset": bson.M{"variation_of": ""}},
	); err != nil {
		return fmt.Errorf("migrate recipe families: unset variation_of: %w", err)
	}
	if _, err := recipes.Indexes().DropOne(ctx, "recipes_variation_of"); err != nil {
		var cmdErr mongo.CommandError
		if !errors.As(err, &cmdErr) || (cmdErr.Name != "IndexNotFound" && cmdErr.Code != 27 && cmdErr.Code != 26) {
			return fmt.Errorf("migrate recipe families: drop recipes_variation_of: %w", err)
		}
	}
	return nil
}

func objectIDs(ids []string) ([]primitive.ObjectID, error) {
	out := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		objectID, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			return nil, err
		}
		out = append(out, objectID)
	}
	return out, nil
}

// GetFamilySizes returns the member count of each given family. A family with
// no members is absent.
func (c *Client) GetFamilySizes(ctx context.Context, familyIDs []string) (map[string]int64, error) {
	result := make(map[string]int64, len(familyIDs))
	familyObjectIDs, err := objectIDs(familyIDs)
	if err != nil {
		return nil, err
	}
	if len(familyObjectIDs) == 0 {
		return result, nil
	}
	cursor, err := c.db.Collection(recipesCollection).Aggregate(ctx, bson.A{
		bson.D{{Key: "$match", Value: bson.D{{Key: "family_id", Value: bson.D{{Key: "$in", Value: familyObjectIDs}}}}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$family_id"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
	})
	if err != nil {
		return nil, err
	}
	var docs []struct {
		ID    primitive.ObjectID `bson:"_id"`
		Count int64              `bson:"count"`
	}
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	for _, doc := range docs {
		result[doc.ID.Hex()] = doc.Count
	}
	return result, nil
}

// GetNewestFamilyMemberID returns the newest member of familyID other than
// excludeID (which may be empty), or nil when there is none.
func (c *Client) GetNewestFamilyMemberID(ctx context.Context, familyID, excludeID string) (*primitive.ObjectID, error) {
	familyObjectID, err := primitive.ObjectIDFromHex(familyID)
	if err != nil {
		return nil, err
	}
	filter := bson.M{"family_id": familyObjectID}
	if excludeID != "" {
		excludeObjectID, err := primitive.ObjectIDFromHex(excludeID)
		if err != nil {
			return nil, err
		}
		filter["_id"] = bson.M{"$ne": excludeObjectID}
	}
	var doc struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	err = c.db.Collection(recipesCollection).FindOne(ctx, filter,
		options.FindOne().SetSort(bson.D{{Key: "_id", Value: -1}}).SetProjection(bson.M{"_id": 1}),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &doc.ID, nil
}

// GetNewestFamilyMemberNotReferencing returns the newest member of familyID
// other than recipeID whose ingredients do not reference recipeID, or nil when
// there is none. It is the repoint target for a delete: a member that refs
// recipeID would end up referencing itself.
func (c *Client) GetNewestFamilyMemberNotReferencing(ctx context.Context, familyID, recipeID string) (*primitive.ObjectID, error) {
	ids, err := objectIDs([]string{familyID, recipeID})
	if err != nil {
		return nil, err
	}
	var doc struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	err = c.db.Collection(recipesCollection).FindOne(ctx, bson.M{
		"family_id":              ids[0],
		"_id":                    bson.M{"$ne": ids[1]},
		"ingredients.recipe_ref": bson.M{"$ne": ids[1]},
	}, options.FindOne().SetSort(bson.D{{Key: "_id", Value: -1}}).SetProjection(bson.M{"_id": 1}),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &doc.ID, nil
}

// SetFamilyID moves one recipe into familyID.
func (c *Client) SetFamilyID(ctx context.Context, recipeID, familyID string) error {
	ids, err := objectIDs([]string{recipeID, familyID})
	if err != nil {
		return err
	}
	_, err = c.db.Collection(recipesCollection).UpdateOne(ctx,
		bson.M{"_id": ids[0]},
		bson.M{"$set": bson.M{"family_id": ids[1]}},
	)
	return err
}

// RekeyFamily moves every member of familyID except excludeID to newFamilyID.
func (c *Client) RekeyFamily(ctx context.Context, familyID, excludeID, newFamilyID string) error {
	ids, err := objectIDs([]string{familyID, excludeID, newFamilyID})
	if err != nil {
		return err
	}
	_, err = c.db.Collection(recipesCollection).UpdateMany(ctx,
		bson.M{"family_id": ids[0], "_id": bson.M{"$ne": ids[1]}},
		bson.M{"$set": bson.M{"family_id": ids[2]}},
	)
	return err
}

// FamilyReferencesRecipe reports whether any member of familyID has an
// ingredient referencing targetID, powering LinkVariation's self-reference
// loop check.
func (c *Client) FamilyReferencesRecipe(ctx context.Context, familyID, targetID string) (bool, error) {
	ids, err := objectIDs([]string{familyID, targetID})
	if err != nil {
		return false, err
	}
	count, err := c.db.Collection(recipesCollection).CountDocuments(ctx,
		bson.M{"family_id": ids[0], "ingredients.recipe_ref": ids[1]},
		options.Count().SetLimit(1),
	)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// RecipeReferencesFamily reports whether any ingredient of recipeID references
// a member of familyID.
func (c *Client) RecipeReferencesFamily(ctx context.Context, recipeID, familyID string) (bool, error) {
	ids, err := objectIDs([]string{recipeID, familyID})
	if err != nil {
		return false, err
	}
	var doc struct {
		Ingredients []struct {
			RecipeRef *primitive.ObjectID `bson:"recipe_ref"`
		} `bson:"ingredients"`
	}
	err = c.db.Collection(recipesCollection).FindOne(ctx,
		bson.M{"_id": ids[0]},
		options.FindOne().SetProjection(bson.M{"ingredients.recipe_ref": 1}),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var refs []primitive.ObjectID
	for _, ing := range doc.Ingredients {
		if ing.RecipeRef != nil {
			refs = append(refs, *ing.RecipeRef)
		}
	}
	if len(refs) == 0 {
		return false, nil
	}
	count, err := c.db.Collection(recipesCollection).CountDocuments(ctx,
		bson.M{"_id": bson.M{"$in": refs}, "family_id": ids[1]},
		options.Count().SetLimit(1),
	)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
