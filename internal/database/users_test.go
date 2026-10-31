package database

import (
	"reflect"
	"testing"
)

func TestUserSchemaMinimalist(t *testing.T) {
	// Security audit: User struct must strictly have only 2 fields: ID and UserID
	uType := reflect.TypeOf(User{})
	if uType.NumField() != 2 {
		t.Fatalf("User struct must strictly contain exactly 2 fields (_id and user_id). Found %d fields!", uType.NumField())
	}

	for i := 0; i < uType.NumField(); i++ {
		field := uType.Field(i)
		bsonTag := field.Tag.Get("bson")
		if field.Name == "ID" && bsonTag != "_id,omitempty" {
			t.Errorf("expected _id tag for ID, got %s", bsonTag)
		}
		if field.Name == "UserID" && bsonTag != "user_id" {
			t.Errorf("expected user_id tag for UserID, got %s", bsonTag)
		}
	}
}
