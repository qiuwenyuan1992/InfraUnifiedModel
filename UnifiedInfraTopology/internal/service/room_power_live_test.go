package service

import (
	"context"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// 只读验收：房间3101供电视图返回主供电链与缺失标记，缺失机柜不猜测补边。
func TestLiveRoom3101PowerContext(t *testing.T) {
	path := os.Getenv("ROOM_POWER_LIVE_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读验收房间供电视图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
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
	source := "00000000000000000000000000000238"
	// 从目标机柜的 located_in 边解析房间身份，不硬编码房间 UUID。
	cabinet := model.EntityIdentity{SourceID: source, EntityType: model.EntityCabinet, StableID: "aa4df5a7-1295-a83d-f3df-34504ab3fb9f"}
	location, err := repo.Neighbors(ctx, cabinet, repository.TopologyNeighborQuery{Limit: 10, Direction: "out", EdgeType: model.EdgeSpatial, RelationKind: model.RelationLocatedIn, PeerEntityType: model.EntityRoom})
	require.NoError(t, err)
	var room *repository.TopologyNode
	for i := range location.Nodes {
		if location.Nodes[i].EntityType == "room" {
			require.Nil(t, room, "机柜房间归属应唯一")
			room = &location.Nodes[i]
		}
	}
	require.NotNil(t, room)
	require.EqualValues(t, 3101, room.Properties["inst_id"])
	// 探针：目标机柜的供电上游边。真图上可能尚未同步电力阶段。
	feed, err := repo.Neighbors(ctx, cabinet, repository.TopologyNeighborQuery{Limit: 10, Direction: "out", EdgeType: model.EdgePower, RelationKind: model.RelationPowerUpstream, PeerEntityType: model.EntityRPP})
	require.NoError(t, err)
	t.Logf("cabinet feeds: edges=%d nodes=%d truncated=%v", len(feed.Edges), len(feed.Nodes), feed.Truncated)
	page, err := NewTopologyBrowseService(repo, conf).RoomPowerContext(ctx, user, model.EntityIdentity{SourceID: source, EntityType: model.EntityRoom, StableID: room.StableID})
	require.NoError(t, err)
	require.Equal(t, room.ID, page.Root.ID)
	require.False(t, page.Truncated, "房间3101供电链不应截断")
	counts := map[string]int{}
	cabinets := map[string]bool{}
	for _, node := range page.Nodes {
		counts[node.EntityType]++
		if node.EntityType == "cabinet" {
			cabinets[node.ID] = true
		}
	}
	for _, edge := range page.Edges {
		switch edge.RelationKind {
		case "located_in":
			counts["edge:located_in"]++
		case "power_upstream":
			counts["edge:power_upstream"]++
		case "member_of":
			counts["edge:member_of"]++
		default:
			t.Fatalf("供电视图不得包含非供电链关系 %s", edge.RelationKind)
		}
	}
	// 缺失标记必须全部指向房间内机柜。
	for _, id := range page.PowerMissing {
		require.True(t, cabinets[id], "缺失标记必须指向房间内机柜: %s", id)
	}
	t.Logf("room=%s nodes=%d edges=%d cabinets=%d feeds=%d missing=%d warnings=%v", room.StableID, len(page.Nodes), len(page.Edges), counts["cabinet"], counts["edge:power_upstream"], len(page.PowerMissing), page.Warnings)
	require.NotZero(t, counts["cabinet"])
	require.NotZero(t, counts["edge:located_in"])
	require.NotZero(t, counts["edge:power_upstream"])
	require.Empty(t, page.Warnings)
}
