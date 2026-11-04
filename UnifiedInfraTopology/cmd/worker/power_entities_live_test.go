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

func TestLiveUPSAndRPPPropertiesReadOnly(t *testing.T) {
	path := os.Getenv("POWER_ENTITY_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读对比UPS/RPP来源与图节点属性")
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
	for _, entity := range []model.EntityType{model.EntityUPS, model.EntityRPP} {
		page, err := adapter.NewCMDB(conf).CollectEntityPage(ctx, model.Source{ConfigRef: "idc1030"}, entity, 0)
		require.NoError(t, err)
		require.NotEmpty(t, page.Records)
		for index, record := range page.Records {
			if index == 5 {
				break
			}
			fields := []string{"code", "inst_id", "idc_id", "ups_group_id"}
			if entity == model.EntityUPS {
				fields = append(fields, "brand", "model", "rated_capacity", "transformer_id_up")
			} else {
				fields = append(fields, "transformer_group_a_id")
			}
			instID, err := strconv.ParseInt(record.Identity, 10, 64)
			require.NoError(t, err)
			identity := strconv.FormatInt(instID, 10)
			vid, err := (model.EntityIdentity{SourceID: "00000000000000000000000000001030", EntityType: entity, StableID: identity}).VID()
			require.NoError(t, err)
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
			require.Equal(t, identity, persistenceString(row[0]))
			for i, field := range fields {
				expected := record.Properties[field]
				encoded := row[i+1]
				if expected == nil {
					require.Equal(t, "null", encoded, field)
					continue
				}
				if text, ok := expected.(string); ok {
					require.Equal(t, text, persistenceString(encoded), field)
				} else {
					number, err := strconv.ParseInt(encoded, 10, 64)
					require.NoError(t, err, field)
					require.Equal(t, expected, number, field)
				}
			}
			t.Logf("matched %s inst_id=%s code=%v ups_group=%v", entity, identity, record.Properties["code"], record.Properties["ups_group_id"])
		}
	}
}
