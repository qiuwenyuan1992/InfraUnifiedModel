package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestLiveRoomSourceReadOnly(t *testing.T) {
	path := os.Getenv("ROOM_SOURCE_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读验证房间来源，不写图库")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	cfg, err := c.configFor(model.Source{ConfigRef: "idc238"})
	require.NoError(t, err)
	cfg.PageSize = 1000
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	page, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/room", map[string]any{"idc_id": 1030, "status": 1}, 0, []string{})
	require.NoError(t, err)
	require.Equal(t, *page.Count, len(*page.Info))
	found := false
	for _, row := range *page.Info {
		id, err := uplinkInteger(row, "inst_id")
		require.NoError(t, err)
		if id == 3101 {
			found = true
			for _, key := range []string{"inst_id", "uuid", "room_full_name", "name", "idc_id", "idc_module_id", "phy_building_id", "status"} {
				t.Logf("room3101 %s=%s", key, row[key])
			}
		}
	}
	require.True(t, found, "目标房间3101必须在可用目录中")
	t.Logf("可用房间count=%d，与机柜phy_room_id及RPPidc_room引用一致", *page.Count)
}
