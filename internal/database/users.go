package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// User represents the minimal MongoDB document strictly containing only the Telegram User ID.
// As mandated by security policy, no names, usernames, phone numbers, or credentials may ever be stored.
type User struct {
	ID     bson.ObjectID `bson:"_id,omitempty"`
	UserID int64         `bson:"user_id"`
}

// AddUser inserts a numeric Telegram User ID into the database.
// If the user already exists, duplicate key errors are safely ignored.
func (d *Database) AddUser(ctx context.Context, userID int64) error {
	if userID == 0 {
		return errors.New("cannot store invalid user_id 0")
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	user := User{UserID: userID}
	_, err := d.usersCol.InsertOne(ctxTimeout, user)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			// Already registered, this is normal and expected
			return nil
		}
		return fmt.Errorf("failed to insert user_id into database: %w", err)
	}

	slog.Debug("Registered new user in database", "user_id", userID)
	return nil
}

// GetAllUserIDs retrieves all stored Telegram User IDs for broadcasting.
func (d *Database) GetAllUserIDs(ctx context.Context) ([]int64, error) {
	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cursor, err := d.usersCol.Find(ctxTimeout, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("failed to query users collection: %w", err)
	}
	defer cursor.Close(ctxTimeout)

	var userIDs []int64
	for cursor.Next(ctxTimeout) {
		var u User
		if err := cursor.Decode(&u); err != nil {
			slog.Warn("Failed to decode user document", "error", err)
			continue
		}
		if u.UserID != 0 {
			userIDs = append(userIDs, u.UserID)
		}
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor iteration error: %w", err)
	}

	return userIDs, nil
}

// GetTotalUsers returns the total count of registered users.
func (d *Database) GetTotalUsers(ctx context.Context) (int64, error) {
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	count, err := d.usersCol.CountDocuments(ctxTimeout, bson.D{})
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return count, nil
}
