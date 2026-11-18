package main

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

// 只读诊断：按 topology_context.go 的 walk 顺序逐步复现设备上下文读取，
// 定位第一个失败步骤与真实错误（HTTP 层会包装成 topology_not_ready，丢失细节）。
func TestLiveDeviceContextWalkReadOnly(t *testing.T) {
	path := os.Getenv("WALK_CONFIG")
	sn := os.Getenv("WALK_SN")
	if path == "" || sn == "" {
		t.Skip("显式配置后只读诊断设备上下文 walk 步骤")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	repo, cleanup, err := repository.NewTopologyReadRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	identity := model.EntityIdentity{SourceID: "00000000000000000000000000000238", EntityType: model.EntityDevice, StableID: sn}
	step := func(name string, run func() error) {
		if err := run(); err != nil {
			t.Fatalf("步骤 %s 失败: %v", name, err)
		} else {
			t.Logf("步骤 %s OK", name)
		}
	}
	neighborStep := func(name string, node repository.TopologyNode, edgeType model.EdgeType, kind model.RelationKind, peer model.EntityType, direction string) []repository.TopologyNode {
		var peers []repository.TopologyNode
		step(name, func() error {
			page, err := repo.Neighbors(ctx, model.EntityIdentity{SourceID: node.SourceID, EntityType: model.EntityType(node.EntityType), StableID: node.StableID}, repository.TopologyNeighborQuery{Limit: 50, Direction: direction, EdgeType: edgeType, RelationKind: kind, PeerEntityType: peer})
			if err != nil {
				return err
			}
			if page == nil {
				return context.DeadlineExceeded
			}
			t.Logf("  %s: edges=%d truncated=%v", name, len(page.Edges), page.Truncated)
			for _, n := range page.Nodes {
				if n.ID != node.ID {
					peers = append(peers, n)
				}
			}
			return nil
		})
		return peers
	}

	root, err := repo.Node(ctx, identity)
	require.NoError(t, err)
	t.Logf("root=%s type=%s parent_type_id=%v", root.StableID, root.EntityType, root.Properties["parent_type_id"])

	cabinets := neighborStep("device→cabinet located_in out", *root, model.EdgeSpatial, model.RelationLocatedIn, model.EntityCabinet, "out")
	var rooms []repository.TopologyNode
	for _, cabinet := range cabinets {
		t.Logf("  cabinet=%s", cabinet.StableID)
		rooms = append(rooms, neighborStep("cabinet→room located_in out", cabinet, model.EdgeSpatial, model.RelationLocatedIn, model.EntityRoom, "out")...)
		neighborStep("cabinet→DC located_in out", cabinet, model.EdgeSpatial, model.RelationLocatedIn, model.EntityDataCenter, "out")
		rpps := neighborStep("cabinet→rpp power_upstream out", cabinet, model.EdgePower, model.RelationPowerUpstream, model.EntityRPP, "out")
		for _, rpp := range rpps {
			groups := neighborStep("rpp→ups_group power_upstream out", rpp, model.EdgePower, model.RelationPowerUpstream, model.EntityUPSGroup, "out")
			for _, group := range groups {
				neighborStep("ups_group→transformer power_upstream out", group, model.EdgePower, model.RelationPowerUpstream, model.EntityTransformer, "out")
				neighborStep("ups_group←ups member_of in", group, model.EdgePower, model.RelationMemberOf, model.EntityUPS, "in")
			}
		}
	}
	for _, room := range rooms {
		neighborStep("room→DC located_in out", room, model.EdgeSpatial, model.RelationLocatedIn, model.EntityDataCenter, "out")
	}
	neighborStep("device→pod member_of out", *root, model.EdgeSpatial, model.RelationMemberOf, model.EntityPod, "out")

	if id, ok := root.Properties["parent_type_id"].(int64); ok && id == 54 {
		// 与线上顺序一致：批量互联是网络设备最后一步，直接复验（阶段级诊断已证明各阶段查询正常）。
		step("SceneNetworkPeers 网络设备批量互联", func() error {
			page, err := repo.SceneNetworkPeers(ctx, []repository.TopologyNode{*root}, 400)
			if err != nil {
				return err
			}
			t.Logf("  SceneNetworkPeers: nodes=%d edges=%d truncated=%v", len(page.Nodes), len(page.Edges), page.Truncated)
			return nil
		})
	} else {
		gpus := neighborStep("device→gpu contains_gpu out", *root, model.EdgeComposition, model.RelationContainsGPU, model.EntityGPU, "out")
		for _, gpu := range gpus {
			ports := neighborStep("gpu→interface gpu_uplink out", gpu, model.EdgeNetwork, model.RelationGPUUplink, model.EntityInterface, "out")
			for _, port := range ports {
				neighborStep("interface←device owns_interface in", port, model.EdgeComposition, model.RelationOwnsInterface, model.EntityDevice, "in")
			}
		}
		ports := neighborStep("device→interface server_uplink out", *root, model.EdgeNetwork, model.RelationServerUplink, model.EntityInterface, "out")
		for _, port := range ports {
			neighborStep("interface←device owns_interface in", port, model.EdgeComposition, model.RelationOwnsInterface, model.EntityDevice, "in")
		}
	}
}
