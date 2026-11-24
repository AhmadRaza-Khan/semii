// Package mongodb opens MongoDB clients, runs transactions and maps driver
// errors to the semii db sentinels.
package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AhmadRaza-Khan/semii/db"
	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// Options tune the client. Zero values use the driver defaults, except
// Timeout (server selection and the open ping), which defaults to 10s.
type Options struct {
	MaxPoolSize uint64
	Timeout     time.Duration
	// NoPing skips the connectivity check Open performs by default.
	NoPing bool
}

// Open connects to uri and returns the client and the named database.
// Errors never include the URI, which usually carries a password.
func Open(ctx context.Context, uri, database string, o Options) (*mongo.Client, *mongo.Database, error) {
	if database == "" {
		return nil, nil, errors.New("mongodb: database name is required")
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	opts := options.Client().ApplyURI(uri).SetServerSelectionTimeout(o.Timeout)
	if o.MaxPoolSize > 0 {
		opts.SetMaxPoolSize(o.MaxPoolSize)
	}
	if err := opts.Validate(); err != nil {
		return nil, nil, errors.New("mongodb: invalid connection URI")
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("mongodb: connect: %w", err)
	}
	if !o.NoPing {
		pctx, cancel := context.WithTimeout(ctx, o.Timeout)
		defer cancel()
		if err := client.Ping(pctx, readpref.Primary()); err != nil {
			_ = client.Disconnect(context.Background())
			return nil, nil, fmt.Errorf("mongodb: ping: %w", err)
		}
	}
	return client, client.Database(database), nil
}

// ErrNoReplicaSet explains why a transaction failed on a standalone server.
var ErrNoReplicaSet = errors.New("mongodb: transactions need a replica set; " +
	"start mongod with --replSet and run rs.initiate() (the generated docker-compose.yml does)")

// WithTx runs fn in a transaction. Use the context fn receives for every
// operation that must be part of it. Transient errors are retried by the
// driver; fn may run more than once.
func WithTx(ctx context.Context, c *mongo.Client, fn func(ctx context.Context) error) error {
	sess, err := c.StartSession()
	if err != nil {
		return fmt.Errorf("mongodb: start session: %w", err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	_, err = sess.WithTransaction(ctx, func(sc context.Context) (any, error) { return nil, fn(sc) })
	if err != nil && isStandalone(err) {
		return ErrNoReplicaSet
	}
	return err
}

func isStandalone(err error) bool {
	var ce mongo.CommandError
	if errors.As(err, &ce) && ce.Code == 20 { // IllegalOperation
		return true
	}
	return strings.Contains(err.Error(), "only allowed on a replica set member or mongos")
}

// MapError translates driver errors into db sentinels, keeping the
// original in the chain:
//
//	mongo.ErrNoDocuments      -> db.ErrNotFound
//	duplicate key (E11000)    -> db.ErrConflict
func MapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mongo.ErrNoDocuments):
		return fmt.Errorf("%w: %w", db.ErrNotFound, err)
	case mongo.IsDuplicateKeyError(err):
		return fmt.Errorf("%w: %w", db.ErrConflict, err)
	}
	return err
}

// EnsureIndexes creates the given indexes per collection. Creating an
// index that already exists with the same definition is a no-op.
func EnsureIndexes(ctx context.Context, d *mongo.Database, indexes map[string][]mongo.IndexModel) error {
	for coll, models := range indexes {
		if len(models) == 0 {
			continue
		}
		if _, err := d.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
			return fmt.Errorf("mongodb: indexes for %s: %w", coll, err)
		}
	}
	return nil
}

// Decimal128 converts a decimal to BSON Decimal128 (exact). Values that do
// not fit (more than 34 significant digits) become NaN.
func Decimal128(d decimal.Decimal) bson.Decimal128 {
	v, err := bson.ParseDecimal128(d.String())
	if err != nil {
		v, _ = bson.ParseDecimal128("NaN")
	}
	return v
}

// Decimal converts BSON Decimal128 back; NaN and infinities become zero.
func Decimal(v bson.Decimal128) decimal.Decimal {
	d, err := decimal.NewFromString(v.String())
	if err != nil {
		return decimal.Zero
	}
	return d
}

// MapPtr applies f to *p, keeping nil as nil.
func MapPtr[A, B any](p *A, f func(A) B) *B {
	if p == nil {
		return nil
	}
	v := f(*p)
	return &v
}

// JSONString stores a JSON value as a string field ("" for nil).
func JSONString(raw json.RawMessage) string { return string(raw) }

// RawJSON reads a JSON string field back ("" is nil).
func RawJSON(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}
