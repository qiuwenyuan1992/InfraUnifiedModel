package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
)

func TestPowerDiagnosticCycles(t *testing.T) {
	all := powerDiagnosticFixture(map[model.EntityType][]adapter.EntityRecord{
		model.EntityTransformer: {
			powerDiagnosticRecord(10, "A", map[string]any{"standby_transformer_id": int64(2)}),
			powerDiagnosticRecord(2, "B", map[string]any{"standby_transformer_id": int64(10)}),
			powerDiagnosticRecord(3, "self", map[string]any{"standby_transformer_id": int64(3)}),
			powerDiagnosticRecord(4, "long-A", map[string]any{"standby_transformer_id": int64(5)}),
			powerDiagnosticRecord(5, "long-B", map[string]any{"standby_transformer_id": int64(6)}),
			powerDiagnosticRecord(6, "long-C", map[string]any{"standby_transformer_id": int64(4)}),
			powerDiagnosticRecord(7, "tail", map[string]any{"standby_transformer_id": int64(10)}),
			powerDiagnosticRecord(8, "missing", map[string]any{"standby_transformer_id": int64(999)}),
			powerDiagnosticRecord(9, "empty", map[string]any{"standby_transformer_id": nil}),
		},
	})
	report, err := buildPowerDiagnostic(context.Background(), model.Source{ID: "s"}, time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), "digest", all, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cycles) != 3 || len(report.RejectedEdges) != 2 || report.Summary.Skipped != 2 || report.Summary.Candidates != 6 || report.Summary.Reasons["standby_cycle"] != 0 || report.Summary.Reasons["standby_self_reference"] != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	for i, expected := range [][]int64{{2, 10}, {3}, {4, 5, 6}} {
		ids := []int64{}
		for _, node := range report.Cycles[i].Nodes {
			ids = append(ids, node.InstanceID)
		}
		if !reflect.DeepEqual(expected, ids) {
			t.Fatalf("cycle %d: %v", i, ids)
		}
	}
	if report.Cycles[0].Classification != "mutual_pair" || report.Cycles[1].Classification != "self" || report.Cycles[2].Classification != "long_cycle" {
		t.Fatalf("classifications: %+v", report.Cycles)
	}
	if report.Cycles[0].Edges[0].From.Code != "B" || report.Cycles[0].Edges[0].To.Code != "A" || len(report.StandbyRows) != 9 {
		t.Fatal("standby source and resolved codes lost")
	}
	if report.Summary.MutualPairs != 1 || report.Cycles[0].Edges[0].Reason != "mutual_pair" || report.Cycles[2].Edges[0].Reason != "standby_configuration_cycle" {
		t.Fatal("all multi-transformer standby cycles must remain informational")
	}
	for _, edge := range report.RejectedEdges {
		if edge.From.InstanceID != 3 && edge.From.InstanceID != 8 {
			t.Fatal("multi-transformer standby cycle included in rejected edges")
		}
	}
}

func TestPowerDiagnosticConflictsDoNotReject(t *testing.T) {
	all := powerDiagnosticFixture(map[model.EntityType][]adapter.EntityRecord{
		model.EntityUPS: {powerDiagnosticRecord(1, "UPS-A", map[string]any{"ups_group_id": int64(3), "transformer_id_up": int64(5)})},
		model.EntityRPP: {powerDiagnosticRecord(2, "RPP-A", map[string]any{"ups_group_id": int64(3), "transformer_group_a_id": int64(5)})},
		model.EntityUPSGroup: {powerDiagnosticRecord(3, "group-A", map[string]any{"transformer_id_up": int64(4)})},
		model.EntityTransformer: {powerDiagnosticRecord(4, "primary", nil), powerDiagnosticRecord(5, "device", nil)},
	})
	report, err := buildPowerDiagnostic(context.Background(), model.Source{ID: "s"}, time.Now().UTC(), "", all, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ConsistencyConflicts) != 2 || report.Summary.Skipped != 0 || report.Summary.Candidates != 3 || report.Summary.Reasons["conflict"] != 2 {
		t.Fatalf("conflicts rejected valid main facts: %+v", report)
	}
	for _, row := range report.ConsistencyConflicts {
		if row.Group.InstanceID != 3 || row.Group.Code != "group-A" || row.ExpectedTransformer.InstanceID != 4 || row.ExpectedTransformer.Code != "primary" || row.DeviceTransformer.InstanceID != 5 || row.DeviceTransformer.Code != "device" || row.RejectsMainRelationship {
			t.Fatalf("incomplete consistency details: %+v", row)
		}
	}
}

func TestPowerDiagnosticInvalidCabinetPaths(t *testing.T) {
	zero := powerDiagnosticRecord(1, "cabinet-zero", map[string]any{"row_switch_id_a": int64(0), "row_switch_id_b": int64(-1)})
	zero.Identity = "cabinet-uuid-1"
	empty := powerDiagnosticRecord(2, "cabinet-empty", map[string]any{"row_switch_id_b": nil})
	empty.Identity = "cabinet-uuid-2"
	all := powerDiagnosticFixture(map[model.EntityType][]adapter.EntityRecord{model.EntityCabinet: {zero, empty}})
	report, err := buildPowerDiagnostic(context.Background(), model.Source{ID: "s"}, time.Now().UTC(), "", all, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.InvalidReferenceRows) != 2 || len(report.EmptyReferenceRows) != 2 || report.Summary.Reasons["invalid_reference"] != 2 || report.Summary.Skipped != 2 {
		t.Fatalf("invalid/empty not distinct: %+v", report)
	}
	if report.InvalidReferenceRows[0].Path != "A" || report.InvalidReferenceRows[0].Reference.State != "zero" || report.EmptyReferenceRows[0].Reference.State != "missing_property" || report.EmptyReferenceRows[1].Reference.State != "null_or_normalized_empty" {
		t.Fatal("raw reference states or paths lost")
	}
}

func powerDiagnosticRecord(id int64, code string, props map[string]any) adapter.EntityRecord {
	if props == nil {
		props = map[string]any{}
	}
	props["code"] = code
	return adapter.EntityRecord{SourceInstanceID: id, Identity: strconv.FormatInt(id, 10), Properties: props}
}

func powerDiagnosticFixture(records map[model.EntityType][]adapter.EntityRecord) map[model.EntityType]*powerRecords {
	all := map[model.EntityType]*powerRecords{}
	for _, entity := range powerEntityTypes {
		data := &powerRecords{records: records[entity], byInstance: map[int64][]adapter.EntityRecord{}, identities: map[string]int{}}
		for _, record := range data.records {
			data.byInstance[record.SourceInstanceID] = append(data.byInstance[record.SourceInstanceID], record)
			data.identities[record.Identity]++
		}
		all[entity] = data
	}
	return all
}

func TestLivePowerDiagnostics(t *testing.T) {
	configPath, reportPath := os.Getenv("POWER_DIAGNOSTIC_CONFIG"), os.Getenv("POWER_DIAGNOSTIC_REPORT")
	if configPath == "" {
		t.Skip("显式设置 POWER_DIAGNOSTIC_CONFIG 和 POWER_DIAGNOSTIC_REPORT 后，只读采集 IDC238 电力诊断")
	}
	if !filepath.IsAbs(configPath) || !filepath.IsAbs(reportPath) || filepath.Ext(reportPath) != ".json" {
		t.Fatal("POWER_DIAGNOSTIC_CONFIG 和 POWER_DIAGNOSTIC_REPORT 必须为绝对路径；报告必须为 .json")
	}
	conf := viper.New()
	conf.SetConfigFile(configPath)
	if err := conf.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	source := model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"}
	collector := adapter.NewCMDB(conf)
	for _, entity := range powerEntityTypes {
		full, err := collector.CoversFullSource(source, entity)
		if err != nil || full {
			t.Fatalf("诊断要求已配置 IDC238 %s 采集条件，不允许全源读取", entity)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	// 不构造图仓储，不调用 Run，仅执行一次来源采集。
	worker := NewPowerSyncWorker(collector, nil)
	snapshot := time.Now().UTC().Truncate(time.Microsecond)
	digest, all, objects, err := worker.collect(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildPowerDiagnostic(ctx, source, snapshot, digest, all, objects)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(reportPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(append(encoded, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	t.Logf("objects=%d candidates=%d skipped=%d reasons=%v cycles=%d mutual_standby_pairs=%d rejected_standby_edges=%d consistency_conflicts=%d invalid_reference_rows=%d report=%s graph_writes=0", objects, report.Summary.Candidates, report.Summary.Skipped, report.Summary.Reasons, len(report.Cycles), report.Summary.MutualPairs, len(report.RejectedEdges), len(report.ConsistencyConflicts), len(report.InvalidReferenceRows), reportPath)
}
