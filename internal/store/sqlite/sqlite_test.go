package sqlite_test

import (
	"context"
	"testing"

	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/sqlite"
	"github.com/felixgeelhaar/olymp/internal/store/suite"
)

func TestSQLiteBackend(t *testing.T) {
	suite.Run(t, func(t *testing.T) ports.Repos {
		repos, err := sqlite.Open(context.Background(), ":memory:")
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() {
			if repos.Close != nil {
				_ = repos.Close()
			}
		})
		return repos
	})
}
