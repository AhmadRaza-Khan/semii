package testkit

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/AhmadRaza-Khan/semii/db/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Mongo returns a client and a fresh database (dropped when the test ends)
// on a replica set, so transactions work. WithMongoSetup runs on the new
// database, typically to ensure indexes.
func Mongo(t testing.TB, opts ...DBOption) (*mongo.Client, *mongo.Database) {
	t.Helper()
	uri := serverURL(t, "mongo")
	var o dbOptions
	for _, opt := range opts {
		opt(&o)
	}
	ctx := context.Background()
	name := fmt.Sprintf("test_%d_%d", os.Getpid(), dbSeq.Add(1))
	client, d, err := mongodb.Open(ctx, uri, name, mongodb.Options{})
	if err != nil {
		t.Fatalf("testkit: connect mongo: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	if o.mongoSetup != nil {
		if err := o.mongoSetup(ctx, d); err != nil {
			t.Fatalf("testkit: mongo setup: %v", err)
		}
	}
	return client, d
}
