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

func TestLiveReferencedPODProperties(t *testing.T) {
	path := os.Getenv("POD_PROPERTY_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读比对IDC238引用POD与图库，实体不建关系")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	source := model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"}
	stream, err := adapter.NewCMDB(conf).OpenEntityPass(ctx, source, model.EntityPod)
	require.NoError(t, err)
	if info, ok := stream.(interface{ ScopeSummary() map[string]int }); ok {
		t.Logf("scope=%v", info.ScopeSummary())
	}
	page, err := stream.Page(ctx, 0)
	require.NoError(t, err)
	t.Logf("resolved_pods=%d", page.Total)
	require.NotEmpty(t, page.Records)
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	fields := []string{"inst_id", "basic_code", "name", "full_name", "plane", "mode", "rdma", "phy_building_id", "idc_logic_id", "is_delete", "is_sync"}
	for index, record := range page.Records {
		if index == 5 {
			break
		}
		vid, err := (model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityPod, StableID: record.Identity}).VID()
		require.NoError(t, err)
		q := fmt.Sprintf("FETCH PROP ON pod %q YIELD properties(vertex).uuid AS uuid", vid)
		for _, field := range fields {
			q += ", properties(vertex)." + field + " AS " + field
		}
		r, err := client.ExecuteParameter(ctx, q+";", nil)
		require.NoError(t, persistenceCheckGraphResult(r, err))
		require.Equal(t, 1, r.GetRowSize())
		rows, err := persistenceTable(r)
		require.NoError(t, err)
		row := rows[1]
		require.Equal(t, record.Identity, persistenceString(row[0]))
		for i, field := range fields {
			expected := record.Properties[field]
			if expected == nil {
				require.Equal(t, "null", row[i+1], field)
				continue
			}
			switch v := expected.(type) {
			case string:
				require.Equal(t, v, persistenceString(row[i+1]), field)
			case int64:
				n, err := strconv.ParseInt(row[i+1], 10, 64)
				require.NoError(t, err)
				require.Equal(t, v, n, field)
			default:
				t.Fatalf("unexpected field %s type %T", field, expected)
			}
		}
		t.Logf("matched pod uuid=%s inst_id=%d basic_code=%v plane=%v", record.Identity, record.SourceInstanceID, record.Properties["basic_code"], record.Properties["plane"])
	}
}
