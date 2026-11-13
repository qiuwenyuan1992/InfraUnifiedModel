package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// 只读探针：按设备SN核对设备、端口与端口间 links_to 边，诊断网络设备互联未展示的原因。
func TestLiveLinksToProbe(t *testing.T) {
	path := os.Getenv("LINKS_TO_PROBE_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读探测网络设备 links_to 边")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	c, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sns := []interface{}{"210235A53L525AL1000G", "210235A53L525CL100G4"}
	run := func(query string, params map[string]interface{}) [][]string {
		result, err := c.ExecuteParameter(ctx, query, params)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.IsSucceed(), "%s: %s", query, result.GetErrorMsg())
		return result.AsStringTable()
	}
	devices := run(`MATCH (d:device) WHERE d.device.device_sn IN $sns RETURN d.device.device_sn AS sn, d.device.source_id AS source_id, d.device.parent_type_id AS parent_type;`, map[string]interface{}{"sns": sns})
	t.Logf("devices_matched=%d", len(devices)-1)
	deviceVIDs := make([]interface{}, 0, len(devices)-1)
	quoted := func(values []interface{}) string {
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = fmt.Sprintf("%q", value)
		}
		return strings.Join(parts, ",")
	}
	for _, row := range devices[1:] {
		// AsStringTable 的字符串值带引号，先剥离再用于身份计算。
		trim := func(value string) string { return strings.Trim(value, "\"") }
		t.Logf("device sn=%s source=%s parent_type=%s", trim(row[0]), trim(row[1]), trim(row[2]))
		// VID 由身份模型确定性生成，仅用于 GO 起点，不拼接业务输入。
		vid, err := model.EntityIdentity{SourceID: trim(row[1]), EntityType: model.EntityDevice, StableID: trim(row[0])}.VID()
		require.NoError(t, err)
		deviceVIDs = append(deviceVIDs, vid)
	}
	if len(deviceVIDs) == 0 {
		return
	}
	// 校验 VID 命中：FETCH 点查设备节点。
	for _, vid := range deviceVIDs {
		rows := run(fmt.Sprintf(`FETCH PROP ON device %q YIELD properties(vertex).device_sn AS sn;`, vid), nil)
		for _, row := range rows[1:] {
			t.Logf("fetch device vid=%q sn=%s", vid, strings.Trim(row[0], "\""))
		}
	}
	// 不带 WHERE 的定向遍历，观察两台设备的全部 composition 出边类型。
	allComposition := run(fmt.Sprintf(`GO FROM %s OVER composition_relation YIELD properties(edge).relation_kind AS kind, dst(edge) AS dst;`, quoted(deviceVIDs)), nil)
	t.Logf("composition_out_edges=%d", len(allComposition)-1)
	for _, row := range allComposition[1:] {
		t.Logf("composition kind=%s dst=%s", row[0], row[1])
	}
	// 从设备 VID 定向遍历 owns_interface 取端口 VID，避免多跳 MATCH 全图扫描。
	ports := run(fmt.Sprintf(`GO FROM %s OVER composition_relation WHERE properties(edge).relation_kind == "owns_interface" YIELD dst(edge) AS port_vid, $$.interface.port_uuid AS port_uuid, $$.interface.port_type AS port_type;`, quoted(deviceVIDs)), nil)
	t.Logf("ports=%d", len(ports)-1)
	portUUIDByVID := map[string]string{}
	portVIDs := make([]interface{}, 0, len(ports)-1)
	for _, row := range ports[1:] {
		vid := strings.Trim(row[0], "\"")
		t.Logf("port uuid=%s type=%s", strings.Trim(row[1], "\""), strings.Trim(row[2], "\""))
		portUUIDByVID[vid] = strings.Trim(row[1], "\"")
		portVIDs = append(portVIDs, vid)
	}
	if len(portVIDs) == 0 {
		return
	}
	// 端口间 links_to 边：从端口 VID 双向定向遍历，不扫全图。
	links := run(fmt.Sprintf(`GO FROM %s OVER network_relation BIDIRECT WHERE properties(edge).relation_kind == "links_to" YIELD src(edge) AS src_vid, dst(edge) AS dst_vid;`, quoted(portVIDs)), nil)
	t.Logf("links_to_edges_touching_ports=%d", len(links)-1)
	endpointVIDs := make([]interface{}, 0, len(links)-1)
	for _, row := range links[1:] {
		src, dst := strings.Trim(row[0], "\""), strings.Trim(row[1], "\"")
		t.Logf("link port=%s vid=%s -> port=%s vid=%s", portUUIDByVID[src], src, portUUIDByVID[dst], dst)
		endpointVIDs = append(endpointVIDs, src, dst)
	}
	if len(endpointVIDs) == 0 {
		return
	}
	// 端点端口归属设备：按 VID 点查，确认对端网络设备身份。
	owners := run(`MATCH (d:device)-[o:composition_relation]->(p:interface) WHERE id(p) IN $vids AND o.relation_kind == "owns_interface" RETURN d.device.device_sn AS owner_sn, d.device.parent_type_id AS owner_type, id(p) AS port_vid;`, map[string]interface{}{"vids": endpointVIDs})
	for _, row := range owners[1:] {
		t.Logf("port %s owner sn=%s type=%s", portUUIDByVID[strings.Trim(row[2], "\"")], strings.Trim(row[0], "\""), strings.Trim(row[1], "\""))
	}
}
