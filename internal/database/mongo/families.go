package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
)

// MigrateRecipeFamilies gives every recipe without a family_id its family: the
// root's id for a variation, its own id otherwise. Idempotent.
func (c *Client) MigrateRecipeFamilies(ctx context.Context) error {
	_, err := c.db.Collection(recipesCollection).UpdateMany(ctx,
		bson.M{"family_id": bson.M{"$exists": false}},
		bson.A{bson.M{"$set": bson.M{"family_id": bson.M{"$ifNull": bson.A{"$variation_of", "$_id"}}}}},
	)
	if err != nil {
		return fmt.Errorf("migrate recipe families: %w", err)
	}
	return nil
}
