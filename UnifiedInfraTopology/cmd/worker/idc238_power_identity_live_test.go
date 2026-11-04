package main

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"fmt"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLiveIDC238PowerInstIDAndPaths(t *testing.T) {
	path := os.Getenv("IDC238_POWER_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读比较四类电力inst_id身份和IDC238机柜供电路径")
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
	source := "00000000000000000000000000000238"
	for _, entity := range []model.EntityType{model.EntityTransformer, model.EntityUPSGroup, model.EntityUPS, model.EntityRPP} {
		page, err := adapter.NewCMDB(conf).CollectEntityPage(ctx, model.Source{ConfigRef: "idc238"}, entity, 0)
		require.NoError(t, err)
		require.NotEmpty(t, page.Records)
		for index, record := range page.Records {
			if index == 5 {
				break
			}
			require.Equal(t, strconv.FormatInt(record.SourceInstanceID, 10), record.Identity)
			vid, err := (model.EntityIdentity{SourceID: source, EntityType: entity, StableID: record.Identity}).VID()
			require.NoError(t, err)
			fields := []string{"inst_id", "code", "idc_id"}
			switch entity {
			case model.EntityTransformer:
				fields = append(fields, "standby_transformer_id", "brand", "model", "rated_capacity")
			case model.EntityUPSGroup:
				fields = append(fields, "transformer_id_up")
			case model.EntityUPS:
				fields = append(fields, "ups_group_id", "transformer_id_up", "brand", "model", "rated_capacity")
			case model.EntityRPP:
				fields = append(fields, "ups_group_id", "transformer_group_a_id")
			}
			q := fmt.Sprintf("FETCH PROP ON %s %q YIELD properties(vertex).inst_id AS identity", entity, vid)
			for _, field := range fields {
				q += ", properties(vertex)." + field + " AS " + field
			}
			r, err := client.ExecuteParameter(ctx, q+";", nil)
			require.NoError(t, persistenceCheckGraphResult(r, err))
			require.Equal(t, 1, r.GetRowSize())
			rows, err := persistenceTable(r)
			require.NoError(t, err)
			row := rows[1]
			require.Equal(t, record.Identity, row[0])
			for i, field := range fields {
				expected := record.Properties[field]
				value := row[i+1]
				if expected == nil {
					require.Equal(t, "null", value, field)
					continue
				}
				switch v := expected.(type) {
				case string:
					require.Equal(t, v, persistenceString(value), field)
				case int64:
					actual, err := strconv.ParseInt(value, 10, 64)
					require.NoError(t, err, field)
					require.Equal(t, v, actual, field)
				default:
					t.Fatalf("unexpected type %s %T", field, expected)
				}
			}
			t.Logf("matched %s inst_id=%s code=%v", entity, record.Identity, record.Properties["code"])
		}
	}
	q := `MATCH (c:cabinet)-[a:power_relation]->(r:rpp)-[b:power_relation]->(g:ups_group)-[d:power_relation]->(t:transformer) WHERE c.cabinet.source_id == $source AND r.rpp.source_id == $source AND g.ups_group.source_id == $source AND t.transformer.source_id == $source AND a.relation_kind == "power_upstream" AND b.relation_kind == "power_upstream" AND d.relation_kind == "power_upstream" RETURN c.cabinet.code AS cabinet, a.power_path AS power_path, r.rpp.inst_id AS rpp, g.ups_group.inst_id AS ups_group, t.transformer.inst_id AS transformer LIMIT 10;`
	r, err := client.ExecuteParameter(ctx, q, map[string]interface{}{"source": source})
	require.NoError(t, persistenceCheckGraphResult(r, err))
	require.Positive(t, r.GetRowSize())
	rows, err := persistenceTable(r)
	require.NoError(t, err)
	for _, row := range rows[1:] {
		t.Logf("cabinet power chain %v", row)
	}
}
