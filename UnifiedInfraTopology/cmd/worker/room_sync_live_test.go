package main

import (
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	"bytes"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveRoom3101RelationsReadOnly(t *testing.T) {
	path := os.Getenv("ROOM_RELATIONS_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后独立只读验收房间3101的机柜和RPP，不写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	reader, cleanup, err := repository.NewTopologyReadRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cabinet := model.EntityIdentity{SourceID: "00000000000000000000000000000238", EntityType: model.EntityCabinet, StableID: "aa4df5a7-1295-a83d-f3df-34504ab3fb9f"}
	cabinetNode, err := reader.Node(ctx, cabinet)
	require.NoError(t, err)
	// 机柜来源计数快照重同步后必须存在；缺失说明属性未写入。
	for _, key := range []string{"device_num", "server_num", "network_device_num"} {
		require.NotNil(t, cabinetNode.Properties[key], "机柜缺少来源计数 %s", key)
	}
	t.Logf("cabinet %s counts device=%v server=%v network=%v", cabinetNode.Label, cabinetNode.Properties["device_num"], cabinetNode.Properties["server_num"], cabinetNode.Properties["network_device_num"])
	location, err := reader.Neighbors(ctx, cabinet, repository.TopologyNeighborQuery{Limit: 10, Direction: "out", EdgeType: model.EdgeSpatial, RelationKind: model.RelationLocatedIn, PeerEntityType: model.EntityRoom})
	require.NoError(t, err)
	var room *repository.TopologyNode
	for i := range location.Nodes {
		if location.Nodes[i].EntityType == "room" {
			require.Nil(t, room, "机柜房间归属应唯一")
			room = &location.Nodes[i]
		}
	}
	require.NotNil(t, room)
	identity := model.EntityIdentity{SourceID: cabinet.SourceID, EntityType: model.EntityRoom, StableID: room.StableID}
	require.Equal(t, int64(3101), room.Properties["inst_id"])
	// 来源计数快照重同步后必须存在；缺失说明属性未写入。
	for _, key := range []string{"cabinet_num", "server_num", "network_device_num"} {
		require.NotNil(t, room.Properties[key], "房间3101缺少来源计数 %s", key)
	}
	t.Logf("room3101 counts cabinet=%v server=%v network=%v", room.Properties["cabinet_num"], room.Properties["server_num"], room.Properties["network_device_num"])
	for _, target := range []struct {
		kind     model.EntityType
		expected string
	}{{model.EntityCabinet, "aa4df5a7-1295-a83d-f3df-34504ab3fb9f"}, {model.EntityRPP, "10934"}} {
		graph, err := reader.Neighbors(ctx, identity, repository.TopologyNeighborQuery{Limit: 200, Direction: "in", EdgeType: model.EdgeSpatial, RelationKind: model.RelationLocatedIn, PeerEntityType: target.kind})
		require.NoError(t, err)
		found := false
		for _, node := range graph.Nodes {
			if node.EntityType == string(target.kind) && node.StableID == target.expected {
				found = true
			}
		}
		require.True(t, found, "房间3101应含%s %s", target.kind, target.expected)
		t.Logf("room3101 %s edges=%d truncated=%v", target.kind, len(graph.Edges), graph.Truncated)
	}
}

func TestLiveRoomAndSpatialPublish(t *testing.T) {
	path := os.Getenv("ROOM_SYNC_CONFIG")
	if path == "" {
		t.Skip("只在独立授权后发布room/RPP房间属性及空间边，不清理已有图")
	}
	require.Equal(t, "ROOM_SPATIAL_ONLY_RETAIN_DATA", os.Getenv("ROOM_SYNC_CONFIRM"))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	at := serverUplinkNow()
	ctx = service.WithSyncSnapshot(ctx, at)
	source := "00000000000000000000000000000238"
	logger := &log.Logger{Logger: zap.NewNop()}
	phases := []string{string(model.EntityRoom), string(model.EntityRPP), "spatial_relations"}
	if os.Getenv("ROOM_SYNC_SPATIAL_ONLY") == "1" {
		phases = []string{"spatial_relations"}
	}
	// 本地开发环境可用逗号分隔自选阶段，例如 data_center,room。
	if custom := os.Getenv("ROOM_SYNC_PHASES"); custom != "" {
		phases = strings.Split(custom, ",")
	}
	for _, phase := range phases {
		stage, e := cloneRoundConfig(conf)
		require.NoError(t, e)
		var output bytes.Buffer
		e = runSyncRoundStage(ctx, stage, logger, source, phase, &output)
		t.Logf("phase=%s snapshot=%s result=%s", phase, at.Format(time.RFC3339Nano), output.String())
		require.NoError(t, e)
	}
}
