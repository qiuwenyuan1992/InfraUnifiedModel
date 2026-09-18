package adapter

import (
	"context"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
)

func TestCMDBIsExplicitlyUnavailable(t *testing.T) {
	a := NewCMDB()
	require.ErrorIs(t, a.Validate(context.Background(), model.Source{}), ErrNotImplemented)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, a.Validate(ctx, model.Source{}), context.Canceled)
}
