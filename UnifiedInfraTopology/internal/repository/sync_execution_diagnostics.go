package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

// RecordSyncExecutionDiagnostic 作为可选能力保留既有 InventoryRepository 接口兼容性。
func (r *inventoryRepository) RecordSyncExecutionDiagnostic(ctx context.Context, diagnostic *model.SyncDiagnostic) error {
	if diagnostic == nil || diagnostic.RunID == "" || diagnostic.SourceID == "" || diagnostic.Resource == "" || diagnostic.Code == "" || diagnostic.CreatedAt.IsZero() || !json.Valid([]byte(diagnostic.Detail)) {
		return ErrInventoryConflict
	}
	var run model.SyncRun
	if err := r.r.DB(ctx).Where("id = ? AND source_id = ?", diagnostic.RunID, diagnostic.SourceID).First(&run).Error; err != nil {
		return ErrInventoryConflict
	}
	if diagnostic.ID == "" {
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return err
		}
		diagnostic.ID = hex.EncodeToString(bytes)
	}
	diagnostic.CreatedAt = diagnostic.CreatedAt.UTC().Truncate(time.Microsecond)
	return r.r.DB(ctx).Create(diagnostic).Error
}
