package store_test

import (
	"context"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

func TestOpenBoundsTheConnectionPool(t *testing.T) {
	db, err := store.Open(context.Background(), env.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if got := db.Stats().MaxOpenConnections; got != store.MaxOpenConns {
		t.Errorf("MaxOpenConnections = %d, want %d", got, store.MaxOpenConns)
	}
	if store.MaxOpenConns != 10 || store.MaxIdleConns <= 0 || store.MaxIdleConns > store.MaxOpenConns {
		t.Errorf("pool constants out of range: open=%d idle=%d", store.MaxOpenConns, store.MaxIdleConns)
	}
	if store.ConnMaxLifetime <= 0 || store.ConnMaxIdleTime <= 0 {
		t.Errorf("connection lifetimes must be positive: %v %v", store.ConnMaxLifetime, store.ConnMaxIdleTime)
	}
}
