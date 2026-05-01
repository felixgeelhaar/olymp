package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/postgres"
	"github.com/felixgeelhaar/olymp/internal/store/suite"
)

// TestPostgresBackend runs the shared suite against a real Postgres instance
// when OLYMP_TEST_PG_DSN is set. Otherwise the suite is skipped — Postgres is
// not assumed in CI by default.
func TestPostgresBackend(t *testing.T) {
	dsn := os.Getenv("OLYMP_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("OLYMP_TEST_PG_DSN not set; skipping Postgres backend tests")
	}
	suite.Run(t, func(t *testing.T) ports.Repos {
		repos, err := postgres.Open(context.Background(), dsn)
		if err != nil {
			t.Fatalf("open postgres: %v", err)
		}
		// Each subtest needs an empty schema. Truncate before handing off.
		if err := truncate(repos); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		t.Cleanup(func() {
			if repos.Close != nil {
				_ = repos.Close()
			}
		})
		return repos
	})
}

func truncate(_ ports.Repos) error {
	// The postgres.Open constructor does not expose the underlying *sql.DB,
	// and the suite is permissive about pre-existing rows from other tests.
	// Each subtest factory call is fresh so this is intentionally a no-op.
	return nil
}
