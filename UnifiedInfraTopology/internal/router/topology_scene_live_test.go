package router

import (
	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestLiveCompactSceneAPIReadOnly(t *testing.T) {
	path := os.Getenv("COMPACT_SCENE_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读验证层级场景，不同步或写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.auth.enabled", false)
	repo, cleanup, err := repository.NewTopologyReadRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	l := &log.Logger{Logger: zap.NewNop()}
	h := handler.NewHandler(l)
	r := gin.New()
	InitInventoryRouter(RouterDeps{JWT: jwt.NewJwt(conf), Logger: l, Config: conf, InventoryHandler: handler.NewInventoryHandler(h, &inventoryStub{}), InventoryRoundHandler: handler.NewInventoryRoundHandler(h, &roundInventoryStub{}), TopologyBrowseHandler: handler.NewTopologyBrowseHandler(h, service.NewTopologyBrowseService(repo, conf))}, r.Group("/v1"))
	server := httptest.NewServer(r)
	defer server.Close()
	server.Client().Timeout = 20 * time.Second
	source := "00000000000000000000000000000238"
	get := func(path string, status int) json.RawMessage {
		response, e := server.Client().Get(server.URL + "/v1/inventory/topology/" + path)
		require.NoError(t, e)
		defer response.Body.Close()
		var envelope struct {
			Data    json.RawMessage `json:"data"`
			Message string          `json:"message"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
		require.Equal(t, status, response.StatusCode, "%s %s", path, envelope.Message)
		return envelope.Data
	}
	for _, kind := range []string{"data_center", "pod"} {
		if os.Getenv("COMPACT_SCENE_POD_ID") != "" && kind != "pod" {
			continue
		}
		var roots repository.TopologyDirectoryPage
		require.NoError(t, json.Unmarshal(get("roots?"+url.Values{"source_id": {source}, "entity_type": {kind}, "limit": {"1"}}.Encode(), 200), &roots))
		require.NotEmpty(t, roots.Items)
		if id := os.Getenv("COMPACT_SCENE_POD_ID"); id != "" {
			var selected repository.TopologyNode
			require.NoError(t, json.Unmarshal(get("node?"+url.Values{"source_id": {source}, "entity_type": {"pod"}, "stable_id": {id}}.Encode(), 200), &selected))
			roots.Items = []repository.TopologyNode{selected}
		}
		query := url.Values{"source_id": {source}, "entity_type": {kind}, "stable_id": {roots.Items[0].StableID}, "limit": {"5"}}
		began := time.Now()
		raw := get("scene?"+query.Encode(), 200)
		var scene service.TopologyScene
		require.NoError(t, json.Unmarshal(raw, &scene))
		require.Equal(t, roots.Items[0].ID, scene.Root.ID)
		require.NotContains(t, string(raw), "properties")
		require.Contains(t, string(raw), "\"component_owners\"")
		present := map[string]service.SceneNode{}
		for _, node := range scene.Nodes {
			present[node.ID] = node
		}
		for _, group := range scene.ComponentOwners {
			require.Equal(t, "device", present[group.ParentID].EntityType)
			for _, id := range group.ChildIDs {
				require.Contains(t, present, id)
				require.Contains(t, []string{"interface", "gpu"}, present[id].EntityType)
				for _, unresolved := range scene.Unassigned {
					require.NotEqual(t, id, unresolved.Node.ID)
				}
			}
		}
		require.LessOrEqual(t, len(scene.Nodes), 400)
		require.LessOrEqual(t, len(scene.Edges), 600)
		t.Logf("scene=%s cabinets=%d nodes=%d edges=%d levels=%d unassigned=%d truncated=%v duration=%s", kind, len(scene.Cabinets), len(scene.Nodes), len(scene.Edges), len(scene.NetworkLevels), len(scene.Unassigned), scene.Truncated, time.Since(began))
		if scene.NextAfter != "" {
			query.Set("after", scene.NextAfter)
			get("scene?"+query.Encode(), 200)
		}
		query.Set("limit", "11")
		get("scene?"+query.Encode(), 400)
	}
}
