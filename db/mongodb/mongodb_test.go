package mongodb_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AhmadRaza-Khan/semii/db"
	"github.com/AhmadRaza-Khan/semii/db/mongodb"
	"github.com/AhmadRaza-Khan/semii/testkit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestMongo(t *testing.T) {
	ctx := context.Background()
	client, d := testkit.Mongo(t, testkit.WithMongoSetup(func(ctx context.Context, d *mongo.Database) error {
		return mongodb.EnsureIndexes(ctx, d, map[string][]mongo.IndexModel{
			"users": {{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)}},
			"empty": nil,
		})
	}))
	users := d.Collection("users")
	if _, err := users.InsertOne(ctx, bson.M{"email": "a@x.io"}); err != nil {
		t.Fatal(err)
	}
	_, err := users.InsertOne(ctx, bson.M{"email": "a@x.io"})
	if !errors.Is(mongodb.MapError(err), db.ErrConflict) {
		t.Errorf("duplicate: %v", err)
	}
	err = users.FindOne(ctx, bson.M{"email": "nobody"}).Err()
	if mapped := mongodb.MapError(err); !errors.Is(mapped, db.ErrNotFound) || !errors.Is(mapped, mongo.ErrNoDocuments) {
		t.Errorf("not found: %v", mapped)
	}
	other := errors.New("x")
	if mongodb.MapError(other) != other || mongodb.MapError(nil) != nil {
		t.Error("passthrough")
	}

	// Transactions commit and roll back on the replica set.
	if err := mongodb.WithTx(ctx, client, func(sc context.Context) error {
		_, err := users.InsertOne(sc, bson.M{"email": "b@x.io"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := mongodb.WithTx(ctx, client, func(sc context.Context) error {
		if _, err := users.InsertOne(sc, bson.M{"email": "c@x.io"}); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("abort: %v", err)
	}
	if n, _ := users.CountDocuments(ctx, bson.M{}); n != 2 {
		t.Errorf("%d documents, want 2", n)
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, _, err := mongodb.Open(ctx, "mongodb://x", "", mongodb.Options{}); err == nil {
		t.Error("empty database accepted")
	}
	if _, _, err := mongodb.Open(ctx, "nope://", "d", mongodb.Options{}); err == nil {
		t.Error("bad URI accepted")
	}
	_, _, err := mongodb.Open(ctx, "mongodb://u:secretpw@127.0.0.1:1/?serverSelectionTimeoutMS=500", "d", mongodb.Options{Timeout: 500e6, MaxPoolSize: 2})
	if err == nil || strings.Contains(err.Error(), "secretpw") {
		t.Errorf("unreachable: %v", err)
	}
	c, d, err := mongodb.Open(ctx, "mongodb://127.0.0.1:1", "d", mongodb.Options{NoPing: true})
	if err != nil || d.Name() != "d" {
		t.Fatal(err)
	}
	_ = c.Disconnect(ctx)
}
