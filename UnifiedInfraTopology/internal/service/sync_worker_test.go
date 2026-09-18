package service

import (
	"context"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/pkg/log"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type availableSource struct{}

func (availableSource) Validate(context.Context, model.Source) error { return nil }

func TestSyncWorkerNeverReportsUnimplementedExecutionAsSuccess(t *testing.T) {
	l := &log.Logger{Logger: zap.NewNop()}
	w := NewSyncWorker(adapter.NewCMDB(), l)
	require.ErrorIs(t, w.Execute(context.Background(), model.Source{}), adapter.ErrNotImplemented)
	w = NewSyncWorker(availableSource{}, l)
	require.ErrorIs(t, w.Execute(context.Background(), model.Source{}), ErrSyncNotImplemented)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, w.Execute(ctx, model.Source{}), context.Canceled)
}

func TestSyncWorkerStandbyStopsOnCancellation(t *testing.T) {
	w := NewSyncWorker(adapter.NewCMDB(), &log.Logger{Logger: zap.NewNop()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	select {
	case <-done:
		t.Fatal("standby returned before cancellation")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	// 启动前已取消也必须立即退出。
	require.NoError(t, w.Run(ctx))
}
