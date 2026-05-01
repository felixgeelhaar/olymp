package store_test

import (
	"context"
	"testing"

	"github.com/felixgeelhaar/olymp/internal/store"
)

func TestOpen_Defaults(t *testing.T) {
	repos, err := store.Open(context.Background(), store.Config{})
	if err != nil {
		t.Fatalf("default open: %v", err)
	}
	if repos.Runs == nil || repos.Sessions == nil || repos.IntentTypes == nil ||
		repos.Audit == nil || repos.Approvals == nil {
		t.Fatal("default repos missing fields")
	}
	if repos.Close != nil {
		_ = repos.Close()
	}
}

func TestOpen_Memory(t *testing.T) {
	if _, err := store.Open(context.Background(), store.Config{Type: "memory"}); err != nil {
		t.Fatalf("memory open: %v", err)
	}
}

func TestOpen_SQLite(t *testing.T) {
	repos, err := store.Open(context.Background(), store.Config{Type: "sqlite", Conn: ":memory:"})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
}

func TestOpen_Unknown(t *testing.T) {
	if _, err := store.Open(context.Background(), store.Config{Type: "bogus"}); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}
