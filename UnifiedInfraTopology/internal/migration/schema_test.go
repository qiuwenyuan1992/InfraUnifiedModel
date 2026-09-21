package migration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testID(n int) string { return fmt.Sprintf("%032x", n) }

func seedInventory(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, Apply(context.Background(), db))
	rows := []any{
		&model.Source{ID: testID(2), Name: "cmdb", AdapterKind: "fixture", ConfigRef: "source/cmdb", Enabled: true},
		&model.SyncRun{ID: testID(3), Status: "queued", Mode: "full", RequestHash: "hash", IdempotencyKey: "request-1", RequestedBy: "operator", CreatedAt: time.Now().UTC()},
		&model.Generation{ID: testID(4), RunID: testID(3), State: "published", InventoryReady: true, CreatedAt: time.Now().UTC()},
		&model.Entity{ID: testID(5), Kind: "device", CreatedAt: time.Now().UTC()},
		&model.Entity{ID: testID(6), Kind: "interface", CreatedAt: time.Now().UTC()},
		&model.Entity{ID: testID(7), Kind: "address", CreatedAt: time.Now().UTC()},
		&model.SyncRunSource{RunID: testID(3), SourceID: testID(2), Status: "queued"},
		&model.SourceKey{ID: testID(8), SourceID: testID(2), KeyHash: make([]byte, 32), ObjectType: "device", Namespace: "cmdb", NativeID: "42"},
		&model.IdentityBinding{ID: testID(9), SourceKeyID: testID(8), EntityID: testID(5), Incarnation: 1, FirstGenerationID: testID(4)},
	}
	for _, row := range rows {
		require.NoError(t, db.Create(row).Error, "%T", row)
	}
	// 仅验证历史迁移约束，运行时资产模型不映射这些休眠表。
	require.NoError(t, db.Exec("INSERT INTO device_versions (generation_id, entity_id, name, device_kind, role, lifecycle, resolution_status) VALUES (?, ?, 'switch', 'switch', 'unknown', 'active', 'resolved')", testID(4), testID(5)).Error)
	require.NoError(t, db.Exec("INSERT INTO interface_versions (generation_id, entity_id, device_id, namespace, source_name, normalized_name, interface_kind, admin_state, oper_state, lifecycle, resolution_status) VALUES (?, ?, ?, 'default', 'Gi1', 'Gi1', 'physical', 'unknown', 'unknown', 'active', 'resolved')", testID(4), testID(6), testID(5)).Error)
	require.NoError(t, db.Exec("INSERT INTO address_versions (generation_id, entity_id, device_id, address_family, address, address_scope_key, scope_status, purpose, lifecycle, resolution_status) VALUES (?, ?, ?, 4, ?, 'source:unknown', 'unknown', 'management', 'active', 'resolved')", testID(4), testID(7), testID(5), make([]byte, 16)).Error)
}

func TestSchemaRoundTripsNullableFieldsAndKeys(t *testing.T) {
	db := testDB(t)
	seedInventory(t, db)
	var state model.InventoryState
	require.NoError(t, db.First(&state, 1).Error)
	require.Nil(t, state.ActiveGenerationID)
	require.Equal(t, "uninitialized", state.ProjectionState)
	require.Zero(t, state.ProjectionEpoch)
	var run model.SyncRun
	require.NoError(t, db.First(&run).Error)
	require.Nil(t, run.BaseGenerationID)
	require.Nil(t, run.GenerationID)
	require.Nil(t, run.CancelRequestedAt)
	require.Nil(t, run.StartedAt)
	require.Nil(t, run.FinishedAt)
	var device struct{ SerialNumber *string }
	require.NoError(t, db.Table("device_versions").Take(&device).Error)
	require.Nil(t, device.SerialNumber)
	var iface struct {
		SpeedBPS *int64 `gorm:"column:speed_bps"`
	}
	require.NoError(t, db.Table("interface_versions").Take(&iface).Error)
	require.Nil(t, iface.SpeedBPS)
	var address struct {
		InterfaceID  *string
		PrefixLength *int
		Address      []byte
	}
	require.NoError(t, db.Table("address_versions").Take(&address).Error)
	require.Nil(t, address.InterfaceID)
	require.Nil(t, address.PrefixLength)
	require.Len(t, address.Address, 16)
	var binding model.IdentityBinding
	require.NoError(t, db.First(&binding).Error)
	require.Nil(t, binding.RetiredGenerationID)
	var generation model.Generation
	require.NoError(t, db.First(&generation).Error)
	require.Nil(t, generation.PublishedAt)
	require.True(t, generation.InventoryReady)
	require.False(t, generation.GraphReady)
	require.False(t, generation.RoutingReady)
	zero := int64(0)
	require.NoError(t, db.Table("interface_versions").Where("entity_id = ?", testID(6)).Update("speed_bps", &zero).Error)
	require.NoError(t, db.Table("interface_versions").Take(&iface).Error)
	require.NotNil(t, iface.SpeedBPS)
	require.EqualValues(t, 0, *iface.SpeedBPS)
}

func TestSchemaRejectsBrokenReferencesAndDuplicateKeys(t *testing.T) {
	db := testDB(t)
	seedInventory(t, db)
	checks := []struct {
		name, sql string
		args      []any
	}{
		{"duplicate version", "INSERT INTO device_versions SELECT * FROM device_versions", nil},
		{"referenced run identity", "UPDATE sync_runs SET id = ? WHERE id = ?", []any{testID(30), testID(3)}},
		{"missing entity", "UPDATE device_versions SET entity_id = ?", []any{testID(99)}},
		{"missing generation", "UPDATE device_versions SET generation_id = ?", []any{testID(99)}},
		{"missing device", "UPDATE interface_versions SET device_id = ?", []any{testID(99)}},
		{"missing interface", "UPDATE address_versions SET interface_id = ?", []any{testID(99)}},
		{"missing source", "UPDATE sync_run_sources SET source_id = ?", []any{testID(99)}},
		{"duplicate binding", "INSERT INTO identity_bindings SELECT ?, source_key_id, incarnation, entity_id, first_generation_id, retired_generation_id FROM identity_bindings", []any{testID(90)}},
		{"duplicate source key", "INSERT INTO source_keys SELECT ?, source_id, key_hash, object_type, namespace, native_id FROM source_keys", []any{testID(91)}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) { require.Error(t, db.Exec(check.sql, check.args...).Error) })
	}
	duplicateRun := model.SyncRun{ID: testID(30), Status: "queued", Mode: "full", RequestHash: "other", IdempotencyKey: "request-1", RequestedBy: "operator", CreatedAt: time.Now().UTC()}
	require.Error(t, db.Create(&duplicateRun).Error)
	duplicateSource := model.Source{ID: testID(31), Name: "cmdb", AdapterKind: "fixture", ConfigRef: "source/other", Enabled: true}
	require.Error(t, db.Create(&duplicateSource).Error)
	require.NoError(t, db.Exec("UPDATE address_versions SET interface_id = ?", testID(6)).Error)
	// 同一接口只有同代且同设备的地址才能引用。
	require.NoError(t, db.Create(&model.Entity{ID: testID(10), Kind: "device", CreatedAt: time.Now().UTC()}).Error)
	require.NoError(t, db.Exec("INSERT INTO device_versions (generation_id, entity_id, name, device_kind, role, lifecycle, resolution_status) VALUES (?, ?, 'other', 'switch', 'unknown', 'active', 'resolved')", testID(4), testID(10)).Error)
	require.Error(t, db.Exec("UPDATE address_versions SET device_id = ?", testID(10)).Error)
}

func TestSchemaInventoryStateAndGlobalReferences(t *testing.T) {
	db := testDB(t)
	seedInventory(t, db)
	require.Error(t, db.Create(&model.InventoryState{ID: 2}).Error)
	require.NoError(t, db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("active_generation_id", testID(4)).Error)
	require.Error(t, db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("active_generation_id", testID(99)).Error)

	run := model.SyncRun{ID: testID(30), Status: "queued", Mode: "full", BaseGenerationID: ptr(testID(4)), RequestHash: "hash", IdempotencyKey: "request-2", RequestedBy: "operator", CreatedAt: time.Now().UTC()}
	require.NoError(t, db.Create(&run).Error)
	generation := model.Generation{ID: testID(40), RunID: testID(30), State: "building", CreatedAt: time.Now().UTC()}
	require.NoError(t, db.Create(&generation).Error)
	require.NoError(t, db.Model(&run).Update("generation_id", generation.ID).Error)
	require.Error(t, db.Create(&model.Generation{ID: testID(41), RunID: testID(99), State: "building", CreatedAt: time.Now().UTC()}).Error)
}

func ptr[T any](value T) *T { return &value }
