package wire

import (
	"context"
	"testing"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/pkg/log"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWorkerWiringNeedsNoExternalServices(t *testing.T) {
	w, err := NewWire(&log.Logger{Logger: zap.NewNop()})
	require.NoError(t, err)
	require.ErrorIs(t, w.Execute(context.Background(), model.Source{}), adapter.ErrNotImplemented)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, w.Run(ctx))
}
