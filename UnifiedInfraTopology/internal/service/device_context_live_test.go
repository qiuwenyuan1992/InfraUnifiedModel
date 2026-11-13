package service

import (
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

// 只读验收：网络设备的设备上下文应返回端口 links_to 互联与对端网络设备。
func TestLiveNetworkDeviceContextLinksTo(t *testing.T) {
	path := os.Getenv("DEVICE_CONTEXT_LIVE_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读验收网络设备 links_to 上下文")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	// 免鉴权模式使用固定操作主体；启用鉴权时在内存配置中授予读取权限，不修改配置文件。
	user := "reader"
	if InventoryAuthDisabled(conf) {
		user = InventoryAnonymousUser
	} else {
		conf.Set("inventory.grants", []map[string]any{{"user_id": user, "permissions": []string{"sync:read"}}})
	}
	repo, cleanup, err := repository.NewTopologyReadRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	identity := model.EntityIdentity{SourceID: "00000000000000000000000000000238", EntityType: model.EntityDevice, StableID: "210235A53L525AL1000G"}
	page, err := NewTopologyBrowseService(repo, conf).DeviceContext(ctx, user, identity)
	require.NoError(t, err)
	require.NotNil(t, page.Root)
	peers, links, ports := map[string]bool{}, 0, 0
	for _, node := range page.Nodes {
		if node.EntityType == "interface" {
			ports++
		}
		if node.EntityType == "device" && node.StableID != identity.StableID {
			peers[node.StableID] = true
		}
	}
	for _, edge := range page.Edges {
		if edge.RelationKind == "links_to" {
			links++
		}
	}
	t.Logf("device=%s nodes=%d edges=%d ports=%d links_to=%d peer_devices=%d warnings=%v truncated=%v", identity.StableID, len(page.Nodes), len(page.Edges), ports, links, len(peers), page.Warnings, page.Truncated)
	require.NotZero(t, links, "网络设备必须返回 links_to 互联边")
	require.NotZero(t, len(peers), "网络设备必须返回对端网络设备")
	require.Contains(t, peers, "210235A53L525CL100G4", "对端网络设备 210235A53L525CL100G4 必须出现")
}
