package adapter

import (
	"context"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveServerUplinkSourceReadOnly(t *testing.T) {
	path := os.Getenv("SERVER_UPLINK_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读探测IDC服务器上联来源，不写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.page_size", 20)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	page, err := NewCMDB(conf).CollectServerUplinkPage(ctx, model.Source{ID: "00000000000000000000000000001030", ConfigRef: "idc1030"}, 0)
	require.NoError(t, err)
	t.Logf("servers_total=%d next_offset=%d resolved_links=%d skipped=%d", page.Total, page.NextOffset, len(page.Links), page.Skipped)
	for index, link := range page.Links {
		if index == 5 {
			break
		}
		require.Equal(t, model.RelationServerUplink, link.Kind)
		require.Equal(t, model.EntityDevice, link.From.EntityType)
		require.Equal(t, model.EntityInterface, link.To.EntityType)
		require.NotEmpty(t, page.Targets[link.To.StableID])
		t.Logf("server=%s tor=%s port_uuid=%s", link.From.StableID, page.Targets[link.To.StableID], link.To.StableID)
	}
}
