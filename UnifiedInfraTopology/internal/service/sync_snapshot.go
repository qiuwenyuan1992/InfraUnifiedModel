package service

import (
	"context"
	"time"
)

type syncSnapshotKey struct{}

func WithSyncSnapshot(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, syncSnapshotKey{}, at.UTC().Truncate(time.Microsecond))
}

func SyncSnapshot(ctx context.Context, fallback time.Time) time.Time {
	if at, ok := ctx.Value(syncSnapshotKey{}).(time.Time); ok && !at.IsZero() {
		return at
	}
	return fallback.UTC().Truncate(time.Microsecond)
}
