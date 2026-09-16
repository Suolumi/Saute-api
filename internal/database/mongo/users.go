package mongo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"recipes/internal/models"
	"recipes/internal/utils"
)

const userCollection = "users"

var UserNotFoundError = errors.New("user not found")
var UserConflictError = errors.New("user conflict")

func (c *Client) GetUserById(id string) (models.UserDB, error) {
	var user models.UserDB

	objectId, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.UserDB{}, err
	}
	cursor := c.db.Collection(userCollection).FindOne(context.TODO(), bson.M{
		"_id": objectId,
	})
	if err := cursor.Decode(&user); errors.Is(err, mongo.ErrNoDocuments) {
		return models.UserDB{}, UserNotFoundError
	} else if err != nil {
		return models.UserDB{}, err
	}
	return user, nil
}

func (c *Client) GetUserByIdentifier(identifier string) (models.UserDB, error) {
	var user models.UserDB
	filter := bson.M{"$or": []bson.M{
		{"username": identifier},
		{"email": identifier},
	}}
	err := c.db.Collection(userCollection).FindOne(context.Background(), filter).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.UserDB{}, UserNotFoundError
	}
	return user, err
}

func (c *Client) UpdateUserById(id string, user models.UserDB) (models.UserDB, error) {
	objectId, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.UserDB{}, err
	}

	passwordChanged := user.Password != ""
	if passwordChanged {
		hashedPassword, err := utils.HashPassword(user.Password)
		if err != nil {
			return models.UserDB{}, err
		}

		user.Password = hashedPassword
	}

	update := bson.M{"$set": user}
	if passwordChanged {
		update["$inc"] = bson.M{"mcp_auth_version": 1}
	}
	cursor := c.db.Collection(userCollection).FindOneAndUpdate(context.TODO(), bson.M{
		"_id": objectId,
	}, update)
	var updated models.UserDB
	if err := cursor.Decode(&updated); errors.Is(err, mongo.ErrNoDocuments) {
		return models.UserDB{}, UserNotFoundError
	} else if err != nil {
		return models.UserDB{}, err
	}
	return c.GetUserById(id)
}

func (c *Client) BumpMCPAuthVersion(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}
	result, err := c.db.Collection(userCollection).UpdateOne(ctx, bson.M{"_id": objectID}, bson.M{"$inc": bson.M{"mcp_auth_version": 1}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return UserNotFoundError
	}
	return nil
}

func (c *Client) UpdateUserInterfaceById(id string, user interface{}) (models.UserDB, error) {
	objectId, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return models.UserDB{}, err
	}

	cursor := c.db.Collection(userCollection).FindOneAndUpdate(context.TODO(), bson.M{
		"_id": objectId,
	}, bson.M{
		"$set": user,
	})
	var updated models.UserDB
	if err := cursor.Decode(&updated); errors.Is(err, mongo.ErrNoDocuments) {
		return models.UserDB{}, UserNotFoundError
	} else if err != nil {
		return models.UserDB{}, err
	}
	return c.GetUserById(id)
}

func (c *Client) DeleteUserById(id string) (models.UserDB, error) {
	var user models.UserDB
	objectId, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return user, err
	}

	cursor := c.db.Collection(userCollection).FindOneAndDelete(context.TODO(), bson.M{
		"_id": objectId,
	})
	if err := cursor.Decode(&user); errors.Is(err, mongo.ErrNoDocuments) {
		return user, UserNotFoundError
	} else if err != nil {
		return user, err
	}
	return user, nil
}

func (c *Client) GetUsers(username string, limit, offset int) ([]models.UserDB, int64, error) {
	var users []models.UserDB
	reqOptions := options.Find()

	if limit != 0 {
		reqOptions.SetLimit(int64(limit))
	}
	if offset != 0 {
		reqOptions.SetSkip(int64(offset))
	}

	filter := bson.M{}
	if username != "" {
		filter["username"] = primitive.Regex{Pattern: regexp.QuoteMeta(username), Options: "i"}
	}
	number, err := c.db.Collection(userCollection).CountDocuments(context.TODO(), filter)
	if err != nil {
		return nil, 0, err
	}

	cursor, err := c.db.Collection(userCollection).Find(context.TODO(), filter, reqOptions)
	if err != nil {
		return nil, 0, err
	}

	if err = cursor.All(context.TODO(), &users); err != nil {
		return nil, 0, err
	}

	return users, number, nil
}

// GetUsersByIDs batch-fetches the display-facing view (id, username,
// picture) of every user in ids, for resolving recipe picture attribution.
// Unknown ids are simply absent from the result.
func (c *Client) GetUsersByIDs(ctx context.Context, ids []string) (map[string]models.UserView, error) {
	result := make(map[string]models.UserView, len(ids))
	objectIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		if objectID, err := primitive.ObjectIDFromHex(id); err == nil {
			objectIDs = append(objectIDs, objectID)
		}
	}
	if len(objectIDs) == 0 {
		return result, nil
	}
	cursor, err := c.db.Collection(userCollection).Find(ctx,
		bson.M{"_id": bson.M{"$in": objectIDs}},
		options.Find().SetProjection(bson.M{"username": 1, "picture": 1}),
	)
	if err != nil {
		return nil, err
	}
	var users []models.UserView
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	for _, user := range users {
		if user.Id != nil {
			result[user.Id.Hex()] = user
		}
	}
	return result, nil
}

func (c *Client) CountUsers(ctx context.Context) (int64, error) {
	return c.db.Collection(userCollection).CountDocuments(ctx, bson.M{})
}

func (c *Client) CountAdmins(ctx context.Context) (int64, error) {
	return c.db.Collection(userCollection).CountDocuments(ctx, bson.M{"admin": true})
}

func (c *Client) CreateUser(user models.UserDB) (models.UserDB, error) {
	hashedPassword, err := utils.HashPassword(user.Password)
	if err != nil {
		return models.UserDB{}, err
	}

	user.Password = hashedPassword

	cursor, err := c.db.Collection(userCollection).InsertOne(context.TODO(), user)
	if err != nil {
		return models.UserDB{}, err
	}
	return c.GetUserById(cursor.InsertedID.(primitive.ObjectID).Hex())
}

func (c *Client) UserConflicts(user models.UserDB) (models.UserDB, error) {
	val := reflect.ValueOf(&user).Elem()
	typ := val.Type()
	dbUser := models.UserDB{}

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldInfos := typ.Field(i)

		if field.IsZero() {
			continue
		}

		bsonName := strings.Split(fieldInfos.Tag.Get("bson"), ",")[0]

		cursor := c.db.Collection(userCollection).FindOne(context.TODO(), bson.M{bsonName: field.Interface()})
		if err := cursor.Decode(&dbUser); err == nil {
			return dbUser, fmt.Errorf("%w: %s is already taken", UserConflictError, bsonName)
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return models.UserDB{}, fmt.Errorf("check user conflict: %w", err)
		}

	}

	return models.UserDB{}, nil
}
