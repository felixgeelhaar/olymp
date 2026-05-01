// Package store wires backends to the repository ports. Domain code calls
// Open and never needs to know which backend is in play.
package store

import (
	"context"
	"fmt"

	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
	"github.com/felixgeelhaar/olymp/internal/store/postgres"
	"github.com/felixgeelhaar/olymp/internal/store/sqlite"
)

// Config selects a backend at startup.
type Config struct {
	Type string // "memory" | "sqlite" | "postgres"
	Conn string // backend-specific connection string
}

// Open returns a fresh ports.Repos for the configured backend.
func Open(ctx context.Context, cfg Config) (ports.Repos, error) {
	switch cfg.Type {
	case "", "memory":
		return memory.New(), nil
	case "sqlite":
		return sqlite.Open(ctx, cfg.Conn)
	case "postgres":
		return postgres.Open(ctx, cfg.Conn)
	default:
		return ports.Repos{}, fmt.Errorf("store: unknown db type: %q", cfg.Type)
	}
}
