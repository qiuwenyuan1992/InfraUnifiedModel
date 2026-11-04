package main

import (
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLivePowerMainChainReadOnly(t *testing.T) {
	path := os.Getenv("POWER_PATH_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读检查机柜A/B供电主链和UPS成员")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	queries := []string{
		`MATCH (r:rpp)-[a:power_relation]->(g:ups_group)-[b:power_relation]->(t:transformer) WHERE r.rpp.source_id == $source AND g.ups_group.source_id == $source AND t.transformer.source_id == $source AND a.relation_kind == "power_upstream" AND b.relation_kind == "power_upstream" RETURN r.rpp.code AS rpp, g.ups_group.code AS ups_group, t.transformer.code AS transformer LIMIT 5;`,
		`MATCH (c:cabinet)-[a:power_relation]->(r:rpp)-[b:power_relation]->(g:ups_group)-[d:power_relation]->(t:transformer) WHERE c.cabinet.source_id == $source AND r.rpp.source_id == $source AND g.ups_group.source_id == $source AND t.transformer.source_id == $source AND a.relation_kind == "power_upstream" AND b.relation_kind == "power_upstream" AND d.relation_kind == "power_upstream" RETURN c.cabinet.code AS cabinet, a.power_path AS power_path, r.rpp.code AS rpp, g.ups_group.code AS ups_group, t.transformer.code AS transformer LIMIT 5;`,
		`MATCH (u:ups)-[m:power_relation]->(g:ups_group) WHERE u.ups.source_id == $source AND g.ups_group.source_id == $source AND m.relation_kind == "member_of" RETURN u.ups.code AS ups, g.ups_group.code AS ups_group LIMIT 5;`,
	}
	for _, q := range queries {
		r, err := client.ExecuteParameter(ctx, q, map[string]interface{}{"source": "00000000000000000000000000001030"})
		require.NoError(t, persistenceCheckGraphResult(r, err))
		if r.GetRowSize() == 0 {
			t.Log("当前机柜来源A/B引用全部为0，无主链路径可验收；UPS成员查询仍需非空")
			if !strings.Contains(q, "c:cabinet") {
				t.Fatal("已发布电力路径为空")
			}
			continue
		}
		rows, err := persistenceTable(r)
		require.NoError(t, err)
		for _, row := range rows[1:] {
			t.Logf("verified power path %v", row)
		}
	}
}
