package service

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveLinksToDiagnostics(t *testing.T) {
	path := os.Getenv("LINKS_TO_DIAGNOSTIC_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读诊断 links_to 图端点拒绝原因")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.page_size", 2000)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	source := model.Source{ID: "00000000000000000000000000001030", ConfigRef: "idc1030"}
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	reader, ok := graph.(repository.LinksToEndpointReader)
	require.True(t, ok)
	worker := NewLinksToWorker(adapter.NewCMDB(conf), graph)
	_, candidates, devices, skipped, reasons, err := worker.collect(ctx, source)
	require.NoError(t, err)
	invalid := make([]bool, len(candidates))
	err = parallelLinksCandidates(ctx, len(candidates), func(c context.Context, index int) error {
		candidate := candidates[index]
		valid, err := reader.LinksToEndpointsValid(c,
			model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.FromDeviceSN},
			model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.ToDeviceSN},
			candidate.Identity.From, candidate.Identity.To)
		invalid[index] = !valid
		return err
	})
	require.NoError(t, err)
	count := 0
	for _, rejected := range invalid {
		if rejected {
			count++
		}
	}
	t.Logf("devices=%d canonical_candidates=%d rejected=%d collection_skipped=%d collection_reasons=%v", devices, len(candidates), count, skipped, reasons)
	if count == 0 {
		return
	}
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	for index, rejected := range invalid {
		if !rejected {
			continue
		}
		candidate := candidates[index]
		diagnostic := struct {
			SourceID string                  `json:"source_id"`
			From     linksEndpointDiagnostic `json:"from"`
			To       linksEndpointDiagnostic `json:"to"`
		}{SourceID: source.ID}
		diagnostic.From = readLinksEndpointDiagnostic(t, ctx, client, source.ID, candidate.FromDeviceSN, candidate.Identity.From)
		diagnostic.To = readLinksEndpointDiagnostic(t, ctx, client, source.ID, candidate.ToDeviceSN, candidate.Identity.To)
		encoded, err := json.Marshal(diagnostic)
		require.NoError(t, err)
		t.Logf("links_to_diagnostic=%s", encoded)
	}
}

type linksDiagnosticCell struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type linksDiagnosticRow map[string]linksDiagnosticCell

type linksEndpointDiagnostic struct {
	DeviceSN      string               `json:"device_sn"`
	PortUUID      string               `json:"port_uuid"`
	DeviceVID     string               `json:"device_vid"`
	PortVID       string               `json:"port_vid"`
	DeviceMissing bool                 `json:"device_missing"`
	PortMissing   bool                 `json:"port_missing"`
	Device        []linksDiagnosticRow `json:"device_properties"`
	Port          []linksDiagnosticRow `json:"interface_properties"`
	Owners        []linksDiagnosticRow `json:"same_source_owns_interface_owners"`
	Reasons       []string             `json:"observed_validator_reasons"`
}

func readLinksEndpointDiagnostic(t *testing.T, ctx context.Context, client graphclient.Client, sourceID, sn string, port model.EntityIdentity) linksEndpointDiagnostic {
	t.Helper()
	device := model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityDevice, StableID: sn}
	deviceVID, err := device.VID()
	require.NoError(t, err)
	portVID, err := port.VID()
	require.NoError(t, err)
	diagnostic := linksEndpointDiagnostic{DeviceSN: sn, PortUUID: port.StableID, DeviceVID: deviceVID, PortVID: portVID, Reasons: []string{}}
	// FETCH 仅拼接经过身份模型生成的哈希 VID；业务输入使用参数化 MATCH。
	diagnostic.Device = readLinksDiagnosticRows(t, ctx, client, fmt.Sprintf(`FETCH PROP ON device %q YIELD properties(vertex).source_id AS source_id, properties(vertex).device_sn AS device_sn, properties(vertex).parent_type_id AS parent_type_id;`, deviceVID), nil, []string{"source_id", "device_sn", "parent_type_id"})
	diagnostic.Port = readLinksDiagnosticRows(t, ctx, client, fmt.Sprintf(`FETCH PROP ON interface %q YIELD properties(vertex).source_id AS source_id, properties(vertex).port_uuid AS port_uuid, properties(vertex).port_type AS port_type;`, portVID), nil, []string{"source_id", "port_uuid", "port_type"})
	diagnostic.DeviceMissing = len(diagnostic.Device) == 0
	diagnostic.PortMissing = len(diagnostic.Port) == 0
	query := `MATCH (t:device)-[o:composition_relation]->(p:interface) WHERE id(p) == $port_vid AND o.source_id == $source_id AND o.relation_kind == "owns_interface" RETURN id(t) AS owner_vid, t.device.source_id AS owner_source, t.device.device_sn AS owner_sn, t.device.parent_type_id AS owner_type, p.interface.source_id AS port_source, p.interface.port_uuid AS port_uuid, p.interface.port_type AS port_type;`
	diagnostic.Owners = readLinksDiagnosticRows(t, ctx, client, query, map[string]interface{}{"port_vid": portVID, "source_id": sourceID}, []string{"owner_vid", "owner_source", "owner_sn", "owner_type", "port_source", "port_uuid", "port_type"})
	if len(diagnostic.Owners) != 1 {
		diagnostic.Reasons = append(diagnostic.Reasons, fmt.Sprintf("same-source owns_interface owner rows=%d; validator requires exactly 1", len(diagnostic.Owners)))
	} else {
		row := diagnostic.Owners[0]
		for _, expected := range []struct {
			column string
			kind   string
			value  any
		}{
			{"owner_source", "string", sourceID}, {"owner_sn", "string", sn}, {"owner_type", "int", int64(54)},
			{"port_source", "string", sourceID}, {"port_uuid", "string", port.StableID}, {"port_type", "string", "physics"},
		} {
			actual := row[expected.column]
			if actual.Type != expected.kind || actual.Value != expected.value {
				diagnostic.Reasons = append(diagnostic.Reasons, fmt.Sprintf("%s: expected %s %v; observed %s %v", expected.column, expected.kind, expected.value, actual.Type, actual.Value))
			}
		}
	}
	if len(diagnostic.Reasons) == 0 {
		diagnostic.Reasons = append(diagnostic.Reasons, "endpoint currently satisfies validator; other endpoint or graph changed since precheck")
	}
	return diagnostic
}

func readLinksDiagnosticRows(t *testing.T, ctx context.Context, client graphclient.Client, query string, params map[string]interface{}, columns []string) []linksDiagnosticRow {
	t.Helper()
	result, err := client.ExecuteParameter(ctx, query, params)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsSucceed(), "read-only diagnostic query failed")
	require.Equal(t, len(columns), result.GetColSize())
	rows := make([]linksDiagnosticRow, 0, result.GetRowSize())
	for index := 0; index < result.GetRowSize(); index++ {
		row, err := result.GetRowValuesByIndex(index)
		require.NoError(t, err)
		decoded := linksDiagnosticRow{}
		for col, name := range columns {
			value, err := row.GetValueByIndex(col)
			require.NoError(t, err)
			cell := linksDiagnosticCell{Type: "null_or_other", Value: fmt.Sprint(value)}
			if value.IsString() {
				cell.Type = "string"
				cell.Value, err = value.AsString()
			} else if value.IsInt() {
				cell.Type = "int"
				cell.Value, err = value.AsInt()
			}
			require.NoError(t, err)
			decoded[name] = cell
		}
		rows = append(rows, decoded)
	}
	return rows
}
