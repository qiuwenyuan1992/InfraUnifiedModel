package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveResetIDC238Graph(t *testing.T) {
	path := os.Getenv("GRAPH_RESET_CONFIG")
	if path == "" {
		t.Skip("默认不访问图库；写操作须独立确认")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	require.Equal(t, "unified_infra_topology", cfg.Space)
	for _, host := range cfg.Hosts {
		require.Equal(t, "127.0.0.1", host.Host)
	}
	source := "00000000000000000000000000000238"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	require.NoError(t, err)
	defer db.Close()
	var active int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM sync_runs WHERE source_id = ? AND status IN (\x27queued\x27,\x27running\x27,\x27validating\x27,\x27publishing\x27)", source).Scan(&active))
	require.Zero(t, active, "活动同步期间不能清空")
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	read := func(query string, params map[string]interface{}) [][]string {
		result, err := client.ExecuteParameter(ctx, query, params)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.IsSucceed(), "query=%s error=%s", query, result.GetErrorMsg())
		return result.AsStringTable()
	}
	tags := []string{"data_center", "cabinet", "device", "transformer", "ups_group", "ups", "rpp", "pod", "interface", "gpu"}
	edgeTypes := []string{"spatial_relation", "composition_relation", "network_relation", "power_relation"}
	schemaBefore := map[string][][]string{}
	for _, tag := range tags {
		schemaBefore["tag:"+tag] = read("SHOW CREATE TAG "+tag+";", nil)
	}
	for _, edge := range edgeTypes {
		schemaBefore["edge:"+edge] = read("SHOW CREATE EDGE "+edge+";", nil)
	}
	schemaBefore["indexes"] = read("SHOW TAG INDEXES;", nil)
	counts := func() map[string]int {
		out := map[string]int{}
		for _, tag := range tags {
			rows := read(fmt.Sprintf("LOOKUP ON %s WHERE %s.source_id == $source YIELD id(vertex) AS vid | YIELD count(*) AS total;", tag, tag), map[string]interface{}{"source": source})
			count, e := standaloneGraphCount(rows)
			require.NoError(t, e)
			out[tag] = int(count)
		}
		for _, edge := range edgeTypes {
			rows := read(fmt.Sprintf("LOOKUP ON %s WHERE %s.source_id == $source YIELD src(edge) AS src | YIELD count(*) AS total;", edge, edge), map[string]interface{}{"source": source})
			count, e := standaloneGraphCount(rows)
			require.NoError(t, e)
			out[edge] = int(count)
		}
		return out
	}
	before := counts()
	t.Logf("source=%s preflight counts=%v", source, before)
	if os.Getenv("GRAPH_RESET_CONFIRM") == "" {
		return
	}
	require.Equal(t, "DELETE_IDC238_SYNC_DATA_ONLY", os.Getenv("GRAPH_RESET_CONFIRM"))
	reportPath := os.Getenv("GRAPH_RESET_REPORT")
	require.NotEmpty(t, reportPath)
	vertices := map[string]bool{}
	for _, tag := range tags {
		rows := read(fmt.Sprintf("LOOKUP ON %s WHERE %s.source_id == $source YIELD id(vertex) AS vid;", tag, tag), map[string]interface{}{"source": source})
		for _, row := range rows[1:] {
			require.Len(t, row, 1)
			require.NotEmpty(t, row[0])
			var id string
			require.NoError(t, json.Unmarshal([]byte(row[0]), &id))
			require.Len(t, id, 64)
			vertices[id] = true
		}
	}
	ids := []string{}
	for id := range vertices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	// 仅删除当前来源的同步节点及其关联边；Schema及MySQL任务历史不变。
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		quoted := []string{}
		for _, id := range ids[start:end] {
			encoded, e := json.Marshal(id)
			require.NoError(t, e)
			quoted = append(quoted, string(encoded))
		}
		read("DELETE VERTEX "+strings.Join(quoted, ",")+" WITH EDGE;", nil)
	}
	after := counts()
	for name, count := range after {
		require.Zero(t, count, "清空后仍有当前来源数据: %s", name)
	}
	for _, tag := range tags {
		require.Equal(t, schemaBefore["tag:"+tag], read("SHOW CREATE TAG "+tag+";", nil))
	}
	for _, edge := range edgeTypes {
		require.Equal(t, schemaBefore["edge:"+edge], read("SHOW CREATE EDGE "+edge+";", nil))
	}
	require.Equal(t, schemaBefore["indexes"], read("SHOW TAG INDEXES;", nil))
	report := map[string]any{"source_id": source, "space": cfg.Space, "cleared_at": time.Now().UTC(), "before": before, "after": after, "deleted_vertices": len(ids), "schema_and_indexes_preserved": true, "mysql_history_preserved": true}
	data, e := json.MarshalIndent(report, "", "  ")
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(reportPath, append(data, 10), 0600))
	t.Logf("cleared vertices=%d after=%v report=%s", len(ids), after, reportPath)
}
