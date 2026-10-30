package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

type uplinkDiagnosticValue struct {
	Text    *string `json:"text,omitempty"`
	Integer *int64  `json:"integer,omitempty"`
}

type uplinkDiagnosticFields map[string]uplinkDiagnosticValue

func (f uplinkDiagnosticFields) text(key string) string {
	if f[key].Text == nil {
		return ""
	}
	return *f[key].Text
}

func (f uplinkDiagnosticFields) integerEquals(key string, expected int64) bool {
	return f[key].Integer != nil && *f[key].Integer == expected
}

type uplinkDiagnosticObservation struct {
	Server uplinkDiagnosticFields   `json:"server"`
	Tor    uplinkDiagnosticFields   `json:"tor"`
	Port   uplinkDiagnosticFields   `json:"port"`
	Owners []uplinkDiagnosticFields `json:"owners"`
}

type uplinkDiagnosticRow struct {
	adapter.UplinkDiagnosticReference
	RelationID string                       `json:"relation_id,omitempty"`
	AllCauses  []string                     `json:"all_causes"`
	Graph      *uplinkDiagnosticObservation `json:"graph,omitempty"`
}

type uplinkDiagnosticDistribution struct {
	TorSN      string `json:"tor_sn"`
	References int    `json:"references"`
}

type uplinkDiagnosticReport struct {
	GeneratedAt            string                         `json:"generated_at"`
	SourceID               string                         `json:"source_id"`
	SourceDigest           string                         `json:"source_digest"`
	Servers                int                            `json:"servers"`
	References             int                            `json:"references"`
	CMDBResolved           int                            `json:"cmdb_resolved"`
	CMDBUnresolved         int                            `json:"cmdb_unresolved"`
	GraphValidReferences   int                            `json:"graph_valid_references"`
	UniqueValidRelationIDs int                            `json:"unique_valid_relation_ids"`
	GraphInvalidReferences int                            `json:"graph_invalid_references"`
	SkippedReferences      int                            `json:"skipped_references"`
	DistinctSkippedServers int                            `json:"distinct_skipped_servers"`
	DistinctSkippedToRs    int                            `json:"distinct_skipped_tors"`
	PrimaryReasons         map[string]int                 `json:"primary_reasons"`
	AllReasons             map[string]int                 `json:"all_reasons"`
	TopToRs                []uplinkDiagnosticDistribution `json:"top_10_skipped_tors"`
	ReferenceUnit          string                         `json:"reference_unit"`
	ConsistencyNote        string                         `json:"consistency_note"`
	Skipped                []uplinkDiagnosticRow          `json:"skipped"`
}

// TestLiveServerUplinkDiagnostics 只有同时设置两个绝对路径才连接实时只读数据源。
func TestLiveServerUplinkDiagnostics(t *testing.T) {
	configPath, reportPath := os.Getenv("UPLINK_DIAGNOSTIC_CONFIG"), os.Getenv("UPLINK_DIAGNOSTIC_REPORT")
	if configPath == "" && reportPath == "" {
		t.Skip("设置 UPLINK_DIAGNOSTIC_CONFIG 和 UPLINK_DIAGNOSTIC_REPORT 后执行只读诊断")
	}
	require.True(t, filepath.IsAbs(configPath), "UPLINK_DIAGNOSTIC_CONFIG 必须是绝对路径")
	require.True(t, filepath.IsAbs(reportPath), "UPLINK_DIAGNOSTIC_REPORT 必须是绝对路径")
	csvPath := strings.TrimSuffix(reportPath, filepath.Ext(reportPath)) + ".csv"
	require.NotEqual(t, filepath.Clean(configPath), filepath.Clean(reportPath))
	require.NotEqual(t, filepath.Clean(configPath), filepath.Clean(csvPath))
	require.NotEqual(t, reportPath, csvPath, "报告路径不能以 .csv 结尾")
	// 不覆盖已有报告或配置；父目录由调用者准备。
	for _, path := range []string{reportPath, csvPath} {
		_, err := os.Lstat(path)
		require.True(t, os.IsNotExist(err), "报告路径已存在或无法检查：%s", path)
	}
	conf := viper.New()
	conf.SetConfigFile(configPath)
	require.NoError(t, conf.ReadInConfig())
	source := model.Source{ID: "00000000000000000000000000001030", ConfigRef: "idc1030"}
	require.Equal(t, int64(1030), conf.GetInt64("inventory.cmdb.sources.idc1030.device_condition.idc_id"), "诊断仅允许 IDC1030")
	ctx, cancel := context.WithTimeout(context.Background(), 280*time.Second)
	defer cancel()
	collected, err := adapter.NewCMDB(conf).CollectServerUplinkDiagnostics(ctx, source)
	require.NoError(t, err)
	graphConfig, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(graphConfig)
	require.NoError(t, err)
	defer client.Close()
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	reader, ok := graph.(repository.ServerUplinkEndpointReader)
	require.True(t, ok)
	devices, err := uplinkDiagnosticQuery(ctx, client, "MATCH (d:device) WHERE d.device.source_id == $source_id RETURN id(d) AS vid, d.device.source_id AS source_id, d.device.device_sn AS sn, d.device.parent_type_id AS category;", map[string]interface{}{"source_id": source.ID}, []string{"vid", "source_id", "sn", "category"})
	require.NoError(t, err)
	deviceCache := map[string]uplinkDiagnosticFields{}
	for _, device := range devices {
		vid := device.text("vid")
		require.NotEmpty(t, vid)
		deviceCache[vid] = device
	}
	portCache := map[string]uplinkDiagnosticFields{}
	ownerCache := map[string][]uplinkDiagnosticFields{}
	report := uplinkDiagnosticReport{GeneratedAt: time.Now().UTC().Format(time.RFC3339), SourceID: source.ID, SourceDigest: collected.SourceDigest, Servers: collected.Servers, References: len(collected.References), PrimaryReasons: map[string]int{}, AllReasons: map[string]int{}, Skipped: []uplinkDiagnosticRow{}, TopToRs: []uplinkDiagnosticDistribution{}, ReferenceUnit: "每个 ports 元素计一次，保留重复；空 ToR 或空 ports 的整条 entry 计一次，与生产采集器一致", ConsistencyNote: "CMDB 服务器扫描一次；图查询非事务快照，期间数据变化可能产生 endpoint_guard_mismatch；未查询现有 server_uplink 边，valid 表示当前端点可写资格，不代表已落边"}
	validIDs := map[string]bool{}
	eligibility := map[string]bool{}
	for _, ref := range collected.References {
		row := uplinkDiagnosticRow{UplinkDiagnosticReference: ref}
		if ref.Reason != "" {
			report.CMDBUnresolved++
			row.AllCauses = []string{ref.Reason}
			report.Skipped = append(report.Skipped, row)
			continue
		}
		report.CMDBResolved++
		server := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: ref.ServerSN}
		tor := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: ref.TorSN}
		port := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityInterface, StableID: ref.PortUUID}
		link := model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgeNetwork, Kind: model.RelationServerUplink, From: server, To: port}
		row.RelationID, err = link.ID()
		require.NoError(t, err)
		key := row.RelationID + ":" + ref.TorSN
		valid, cached := eligibility[key]
		if !cached {
			valid, err = reader.ServerUplinkEndpointsValid(ctx, server, tor, port)
			require.NoError(t, err)
			eligibility[key] = valid
		}
		if valid {
			report.GraphValidReferences++
			validIDs[row.RelationID] = true
			continue
		}
		report.GraphInvalidReferences++
		serverVID, err := server.VID()
		require.NoError(t, err)
		torVID, err := tor.VID()
		require.NoError(t, err)
		portVID, err := port.VID()
		require.NoError(t, err)
		for _, vid := range []string{serverVID, torVID} {
			if _, cached := deviceCache[vid]; cached {
				continue
			}
			rows, err := uplinkDiagnosticQuery(ctx, client, fmt.Sprintf("FETCH PROP ON device %q YIELD id(vertex) AS vid, properties(vertex).source_id AS source_id, properties(vertex).device_sn AS sn, properties(vertex).parent_type_id AS category;", vid), nil, []string{"vid", "source_id", "sn", "category"})
			require.NoError(t, err)
			require.LessOrEqual(t, len(rows), 1)
			deviceCache[vid] = nil
			if len(rows) == 1 {
				deviceCache[vid] = rows[0]
			}
		}
		if _, cached := portCache[portVID]; !cached {
			rows, err := uplinkDiagnosticQuery(ctx, client, fmt.Sprintf("FETCH PROP ON interface %q YIELD id(vertex) AS vid, properties(vertex).source_id AS source_id, properties(vertex).port_uuid AS port_uuid, properties(vertex).port_type AS port_type;", portVID), nil, []string{"vid", "source_id", "port_uuid", "port_type"})
			require.NoError(t, err)
			require.LessOrEqual(t, len(rows), 1)
			portCache[portVID] = nil
			if len(rows) == 1 {
				portCache[portVID] = rows[0]
			}
			ownerCache[portVID], err = uplinkDiagnosticQuery(ctx, client, "MATCH (t:device)-[o:composition_relation]->(p:interface) WHERE id(p) == $vid AND o.relation_kind == $kind RETURN id(t) AS vid, t.device.source_id AS source_id, t.device.device_sn AS sn, t.device.parent_type_id AS category, o.source_id AS edge_source;", map[string]interface{}{"vid": portVID, "kind": "owns_interface"}, []string{"vid", "source_id", "sn", "category", "edge_source"})
			require.NoError(t, err)
		}
		observation := uplinkDiagnosticObservation{Server: deviceCache[serverVID], Tor: deviceCache[torVID], Port: portCache[portVID], Owners: ownerCache[portVID]}
		row.Graph = &observation
		row.AllCauses = uplinkDiagnosticCauses(source.ID, ref, observation)
		if len(row.AllCauses) == 0 {
			row.AllCauses = []string{"endpoint_guard_mismatch"}
		}
		row.Reason = row.AllCauses[0]
		report.Skipped = append(report.Skipped, row)
	}
	report.UniqueValidRelationIDs = len(validIDs)
	uplinkDiagnosticSummarize(&report)
	require.Equal(t, report.References, report.CMDBResolved+report.CMDBUnresolved)
	require.Equal(t, report.CMDBResolved, report.GraphValidReferences+report.GraphInvalidReferences)
	require.Equal(t, report.SkippedReferences, report.CMDBUnresolved+report.GraphInvalidReferences)
	encoded, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	csvData, err := uplinkDiagnosticCSV(report.Skipped)
	require.NoError(t, err)
	require.NoError(t, uplinkDiagnosticWrite(reportPath, append(encoded, byte(10))))
	require.NoError(t, uplinkDiagnosticWrite(csvPath, csvData))
	t.Logf("servers=%d references=%d cmdb_resolved=%d cmdb_unresolved=%d graph_valid_references=%d unique_valid_relations=%d graph_invalid=%d skipped=%d JSON=%s CSV=%s", report.Servers, report.References, report.CMDBResolved, report.CMDBUnresolved, report.GraphValidReferences, report.UniqueValidRelationIDs, report.GraphInvalidReferences, report.SkippedReferences, reportPath, csvPath)
}

func uplinkDiagnosticQuery(ctx context.Context, client graphclient.Client, query string, params map[string]interface{}, columns []string) ([]uplinkDiagnosticFields, error) {
	result, err := client.ExecuteParameter(ctx, query, params)
	if err != nil || result == nil || !result.IsSucceed() {
		return nil, fmt.Errorf("只读图诊断查询失败")
	}
	if result.GetColSize() != len(columns) {
		return nil, fmt.Errorf("图诊断列数不匹配")
	}
	rows := make([]uplinkDiagnosticFields, 0, result.GetRowSize())
	for i := 0; i < result.GetRowSize(); i++ {
		row, err := result.GetRowValuesByIndex(i)
		if err != nil {
			return nil, fmt.Errorf("图诊断行读取失败")
		}
		fields := uplinkDiagnosticFields{}
		for j, key := range columns {
			value, err := row.GetValueByIndex(j)
			if err != nil {
				return nil, fmt.Errorf("图诊断字段读取失败")
			}
			field := uplinkDiagnosticValue{}
			// NULL 和非预期类型不转换成字符串，分类器必须检测实际类型。
			if value.IsString() {
				text, err := value.AsString()
				if err != nil {
					return nil, fmt.Errorf("图诊断文本读取失败")
				}
				field.Text = &text
			} else if value.IsInt() {
				integer, err := value.AsInt()
				if err != nil {
					return nil, fmt.Errorf("图诊断整数读取失败")
				}
				field.Integer = &integer
			}
			fields[key] = field
		}
		rows = append(rows, fields)
	}
	return rows, nil
}

func uplinkDiagnosticCauses(source string, ref adapter.UplinkDiagnosticReference, graph uplinkDiagnosticObservation) []string {
	causes := []string{}
	for _, endpoint := range []struct {
		label, sn string
		category  int64
		fields    uplinkDiagnosticFields
	}{{"server", ref.ServerSN, 52, graph.Server}, {"tor", ref.TorSN, 54, graph.Tor}} {
		if endpoint.fields == nil {
			causes = append(causes, endpoint.label+"_missing")
			continue
		}
		if endpoint.fields.text("source_id") != source {
			causes = append(causes, endpoint.label+"_source_wrong")
		}
		if endpoint.fields.text("sn") != endpoint.sn {
			causes = append(causes, endpoint.label+"_identity_mismatch")
		}
		if !endpoint.fields.integerEquals("category", endpoint.category) {
			causes = append(causes, endpoint.label+"_wrong_category")
		}
	}
	if graph.Port == nil {
		causes = append(causes, "port_missing")
	} else {
		if graph.Port.text("source_id") != source {
			causes = append(causes, "port_source_wrong")
		}
		if graph.Port.text("port_uuid") != ref.PortUUID {
			causes = append(causes, "port_identity_mismatch")
		}
		if graph.Port.text("port_type") != "physics" {
			causes = append(causes, "port_wrong_type")
		}
	}
	owners := []uplinkDiagnosticFields{}
	for _, owner := range graph.Owners {
		if owner.text("edge_source") == source {
			owners = append(owners, owner)
		}
	}
	if len(owners) == 0 {
		causes = append(causes, "ownership_missing")
		if len(graph.Owners) > 0 {
			causes = append(causes, "ownership_edge_source_wrong")
		}
	} else if len(owners) != 1 {
		causes = append(causes, "ownership_multiple")
	}
	seen := map[string]bool{}
	for _, owner := range owners {
		for _, check := range []struct {
			mismatch bool
			reason   string
		}{{owner.text("source_id") != source, "owner_source_wrong"}, {owner.text("sn") != ref.TorSN, "owner_sn_wrong"}, {!owner.integerEquals("category", 54), "owner_wrong_category"}} {
			if check.mismatch && !seen[check.reason] {
				causes = append(causes, check.reason)
				seen[check.reason] = true
			}
		}
	}
	return causes
}

func uplinkDiagnosticSummarize(report *uplinkDiagnosticReport) {
	servers, tors := map[string]bool{}, map[string]int{}
	for _, row := range report.Skipped {
		servers[row.ServerSN] = true
		if row.TorSN != "" {
			tors[row.TorSN]++
		}
		report.PrimaryReasons[row.Reason]++
		for _, reason := range row.AllCauses {
			report.AllReasons[reason]++
		}
	}
	report.SkippedReferences = len(report.Skipped)
	report.DistinctSkippedServers, report.DistinctSkippedToRs = len(servers), len(tors)
	for tor, count := range tors {
		report.TopToRs = append(report.TopToRs, uplinkDiagnosticDistribution{TorSN: tor, References: count})
	}
	sort.Slice(report.TopToRs, func(i, j int) bool {
		if report.TopToRs[i].References != report.TopToRs[j].References {
			return report.TopToRs[i].References > report.TopToRs[j].References
		}
		return report.TopToRs[i].TorSN < report.TopToRs[j].TorSN
	})
	if len(report.TopToRs) > 10 {
		report.TopToRs = report.TopToRs[:10]
	}
}

func uplinkDiagnosticCSV(rows []uplinkDiagnosticRow) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{"server_sn", "server_instance_id", "tor_sn", "tor_instance_id", "port_name", "entry_port_names", "port_uuid", "relation_id", "reason", "all_causes", "graph_observation"}); err != nil {
		return nil, err
	}
	for _, row := range rows {
		names, err := json.Marshal(row.PortNames)
		if err != nil {
			return nil, err
		}
		observation, err := json.Marshal(row.Graph)
		if err != nil {
			return nil, err
		}
		if err := writer.Write([]string{row.ServerSN, strconv.FormatInt(row.ServerInstanceID, 10), row.TorSN, strconv.FormatInt(row.TorInstanceID, 10), row.PortName, string(names), row.PortUUID, row.RelationID, row.Reason, strings.Join(row.AllCauses, ";"), string(observation)}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return buffer.Bytes(), writer.Error()
}

func uplinkDiagnosticWrite(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func TestUplinkDiagnosticCauses(t *testing.T) {
	text := func(value string) uplinkDiagnosticValue { return uplinkDiagnosticValue{Text: &value} }
	integer := func(value int64) uplinkDiagnosticValue { return uplinkDiagnosticValue{Integer: &value} }
	ref := adapter.UplinkDiagnosticReference{ServerSN: "server", TorSN: "tor", PortUUID: "port"}
	device := func(sn string, category int64) uplinkDiagnosticFields {
		return uplinkDiagnosticFields{"source_id": text("source"), "sn": text(sn), "category": integer(category)}
	}
	valid := func() uplinkDiagnosticObservation {
		owner := device("tor", 54)
		owner["edge_source"] = text("source")
		return uplinkDiagnosticObservation{Server: device("server", 52), Tor: device("tor", 54), Port: uplinkDiagnosticFields{"source_id": text("source"), "port_uuid": text("port"), "port_type": text("physics")}, Owners: []uplinkDiagnosticFields{owner}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*uplinkDiagnosticObservation)
		want   []string
	}{
		{"valid", func(*uplinkDiagnosticObservation) {}, []string{}},
		{"missing_server", func(g *uplinkDiagnosticObservation) { g.Server = nil }, []string{"server_missing"}},
		{"null_category", func(g *uplinkDiagnosticObservation) { g.Server["category"] = uplinkDiagnosticValue{} }, []string{"server_wrong_category"}},
		{"wrong_tor_category", func(g *uplinkDiagnosticObservation) { g.Tor["category"] = integer(52) }, []string{"tor_wrong_category"}},
		{"missing_port_and_owner", func(g *uplinkDiagnosticObservation) { g.Port = nil; g.Owners = nil }, []string{"port_missing", "ownership_missing"}},
		{"wrong_port_type", func(g *uplinkDiagnosticObservation) { g.Port["port_type"] = text("virtual") }, []string{"port_wrong_type"}},
		{"multiple_owners", func(g *uplinkDiagnosticObservation) { g.Owners = append(g.Owners, g.Owners[0]) }, []string{"ownership_multiple"}},
		{"wrong_owner", func(g *uplinkDiagnosticObservation) {
			g.Owners[0]["sn"] = text("other")
			g.Owners[0]["source_id"] = text("other_source")
		}, []string{"owner_source_wrong", "owner_sn_wrong"}},
		{"wrong_edge_source", func(g *uplinkDiagnosticObservation) { g.Owners[0]["edge_source"] = text("other") }, []string{"ownership_missing", "ownership_edge_source_wrong"}},
		{"wrong_identity", func(g *uplinkDiagnosticObservation) { g.Port["port_uuid"] = text("other") }, []string{"port_identity_mismatch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := valid()
			tc.mutate(&graph)
			require.Equal(t, tc.want, uplinkDiagnosticCauses("source", ref, graph))
		})
	}
}

func TestUplinkDiagnosticSummaryRetainsDuplicateReferences(t *testing.T) {
	row := uplinkDiagnosticRow{UplinkDiagnosticReference: adapter.UplinkDiagnosticReference{ServerSN: "server", TorSN: "tor", Reason: "port_missing"}, AllCauses: []string{"port_missing", "ownership_missing"}}
	report := uplinkDiagnosticReport{PrimaryReasons: map[string]int{}, AllReasons: map[string]int{}, Skipped: []uplinkDiagnosticRow{row, row}}
	uplinkDiagnosticSummarize(&report)
	require.Equal(t, 2, report.SkippedReferences)
	require.Equal(t, 1, report.DistinctSkippedServers)
	require.Equal(t, 1, report.DistinctSkippedToRs)
	require.Equal(t, 2, report.PrimaryReasons["port_missing"])
	require.Equal(t, 2, report.AllReasons["ownership_missing"])
	encoded, err := uplinkDiagnosticCSV(report.Skipped)
	require.NoError(t, err)
	rows, err := csv.NewReader(bytes.NewReader(encoded)).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3)
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, uplinkDiagnosticWrite(path, []byte("first")))
	require.Error(t, uplinkDiagnosticWrite(path, []byte("second")))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "first", string(data))
}
