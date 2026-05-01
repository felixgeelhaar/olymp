package memory_test

import (
	"testing"

	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
	"github.com/felixgeelhaar/olymp/internal/store/suite"
)

func TestMemoryBackend(t *testing.T) {
	suite.Run(t, func(_ *testing.T) ports.Repos { return memory.New() })
}
