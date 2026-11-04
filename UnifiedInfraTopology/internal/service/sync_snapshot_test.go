package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSyncSnapshotNormalizesInjectedAndFallback(t *testing.T) {
	fallback := time.Date(2026, 11, 3, 10, 0, 0, 987654321, time.FixedZone("offset", 8*60*60))
	injected := fallback.Add(-time.Hour)
	want := injected.UTC().Truncate(time.Microsecond)
	ctx := WithSyncSnapshot(context.Background(), injected)
	require.Equal(t, want, SyncSnapshot(ctx, fallback))
	require.Equal(t, fallback.UTC().Truncate(time.Microsecond), SyncSnapshot(context.Background(), fallback))
	require.Equal(t, fallback.UTC().Truncate(time.Microsecond), SyncSnapshot(WithSyncSnapshot(context.Background(), time.Time{}), fallback))
	require.Equal(t, want, SyncSnapshot(context.WithoutCancel(ctx), fallback))
	require.True(t, SyncSnapshot(context.Background(), time.Time{}).IsZero())
}
