package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Database manages MongoDB connection and collections.
type Database struct {
	client   *mongo.Client
	db       *mongo.Database
	usersCol *mongo.Collection
}

// NewDatabase initializes a connection to MongoDB with retry logic and ping verification.
func NewDatabase(ctx context.Context, uri, dbName string) (*Database, error) {
	slog.Info("Connecting to MongoDB...")

	opts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second)

	var client *mongo.Client
	var err error

	// Retry connection up to 3 times with exponential backoff
	maxRetries := 3
	for i := 1; i <= maxRetries; i++ {
		client, err = mongo.Connect(opts)
		if err == nil {
			pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
			err = client.Ping(pingCtx, nil)
			pingCancel()
			if err == nil {
				break
			}
		}

		slog.Warn("Failed to connect to MongoDB, retrying...", "attempt", i, "max_retries", maxRetries, "error", err)
		if i < maxRetries {
			time.Sleep(time.Duration(i*2) * time.Second)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("unable to establish MongoDB connection after retries: %w", err)
	}

	db := client.Database(dbName)
	usersCol := db.Collection("users")

	dbInstance := &Database{
		client:   client,
		db:       db,
		usersCol: usersCol,
	}

	// Ensure unique index on user_id
	if err := dbInstance.ensureIndexes(ctx); err != nil {
		slog.Warn("Failed to create unique index on user_id", "error", err)
	} else {
		slog.Info("MongoDB unique index on user_id verified")
	}

	slog.Info("MongoDB connected successfully", "database", dbName)
	return dbInstance, nil
}

// ensureIndexes enforces uniqueness on the numeric user_id field.
func (d *Database) ensureIndexes(ctx context.Context) error {
	indexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := d.usersCol.Indexes().CreateOne(ctxTimeout, indexModel)
	return err
}

// Ping checks the health of the MongoDB connection.
func (d *Database) Ping(ctx context.Context) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return d.client.Ping(ctxTimeout, nil)
}

// Close gracefully closes the MongoDB connection.
func (d *Database) Close(ctx context.Context) error {
	slog.Info("Closing MongoDB connection...")
	return d.client.Disconnect(ctx)
}
