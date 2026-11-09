package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLiveTopologyHTTPReadOnly(t *testing.T) {
	path := os.Getenv("TOPOLOGY_BROWSER_HTTP_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读HTTP/图库验收，不采集或写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.auth.enabled", false)
	graph, cleanup, err := repository.NewTopologyReadRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	l := &log.Logger{Logger: zap.NewNop()}
	h := handler.NewHandler(l)
	r := gin.New()
	InitInventoryRouter(RouterDeps{JWT: jwt.NewJwt(conf), Logger: l, Config: conf, InventoryHandler: handler.NewInventoryHandler(h, &inventoryStub{}), InventoryRoundHandler: handler.NewInventoryRoundHandler(h, &roundInventoryStub{}), TopologyBrowseHandler: handler.NewTopologyBrowseHandler(h, service.NewTopologyBrowseService(graph, conf))}, r.Group("/v1"))
	server := httptest.NewServer(r)
	defer server.Close()
	query := url.Values{"source_id": {"00000000000000000000000000000238"}, "entity_type": {"device"}, "stable_id": {"A514359X5413481"}}
	get := func(endpoint string, status int) json.RawMessage {
		response, e := server.Client().Get(server.URL + "/v1/inventory/topology/" + endpoint)
		require.NoError(t, e)
		defer response.Body.Close()
		require.Equal(t, status, response.StatusCode)
		require.NotEmpty(t, response.Header.Get("X-Request-ID"))
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
		return envelope.Data
	}
	data := get("node?"+query.Encode(), http.StatusOK)
	var node repository.TopologyNode
	require.NoError(t, json.Unmarshal(data, &node))
	require.Equal(t, query.Get("stable_id"), node.StableID)
	query.Set("limit", "100")
	query.Set("direction", "both")
	data = get("neighbors?"+query.Encode(), http.StatusOK)
	var graphResult repository.TopologyNeighborhood
	require.NoError(t, json.Unmarshal(data, &graphResult))
	require.NotEmpty(t, graphResult.Edges)
	require.Equal(t, node.ID, graphResult.Root.ID)
	query.Set("limit", "201")
	get("neighbors?"+query.Encode(), http.StatusBadRequest)
	t.Logf("真实HTTP无JWT只读PASS root=%s nodes=%d edges=%d", node.StableID, len(graphResult.Nodes), len(graphResult.Edges))
}

func TestLiveTopologyBusinessDrilldownReadOnly(t *testing.T) {
	path := os.Getenv("TOPOLOGY_DRILLDOWN_HTTP_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读验收数据中心/POD下钻及设备电力路径，不写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.auth.enabled", false)
	graph, cleanup, err := repository.NewTopologyReadRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	l := &log.Logger{Logger: zap.NewNop()}
	h := handler.NewHandler(l)
	r := gin.New()
	InitInventoryRouter(RouterDeps{JWT: jwt.NewJwt(conf), Logger: l, Config: conf, InventoryHandler: handler.NewInventoryHandler(h, &inventoryStub{}), InventoryRoundHandler: handler.NewInventoryRoundHandler(h, &roundInventoryStub{}), TopologyBrowseHandler: handler.NewTopologyBrowseHandler(h, service.NewTopologyBrowseService(graph, conf))}, r.Group("/v1"))
	server := httptest.NewServer(r)
	defer server.Close()
	server.Client().Timeout = 20 * time.Second
	source := "00000000000000000000000000000238"
	get := func(endpoint string) json.RawMessage {
		response, e := server.Client().Get(server.URL + "/v1/inventory/topology/" + endpoint)
		require.NoError(t, e)
		defer response.Body.Close()
		var envelope struct {
			Data    json.RawMessage `json:"data"`
			Message string          `json:"message"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
		require.Equal(t, http.StatusOK, response.StatusCode, "endpoint=%s message=%s", endpoint, envelope.Message)
		return envelope.Data
	}
	roots := func(kind string) repository.TopologyDirectoryPage {
		data := get("roots?" + url.Values{"source_id": {source}, "entity_type": {kind}, "limit": {"2"}}.Encode())
		var page repository.TopologyDirectoryPage
		require.NoError(t, json.Unmarshal(data, &page))
		require.NotEmpty(t, page.Items)
		return page
	}
	dc := roots("data_center")
	pods := roots("pod")
	require.NotEmpty(t, pods.NextAfter, "POD目录必须可继续分页")
	children := func(parent repository.TopologyNode) repository.TopologyDirectoryPage {
		data := get("children?" + url.Values{"source_id": {source}, "entity_type": {parent.EntityType}, "stable_id": {parent.StableID}, "limit": {"100"}}.Encode())
		var page repository.TopologyDirectoryPage
		require.NoError(t, json.Unmarshal(data, &page))
		return page
	}
	cabinets := children(dc.Items[0])
	require.NotEmpty(t, cabinets.Items)
	require.Equal(t, "cabinet", cabinets.Items[0].EntityType)
	members := children(pods.Items[0])
	for _, n := range members.Items {
		require.Equal(t, "device", n.EntityType)
	}
	var devices repository.TopologyDirectoryPage
	for _, cabinet := range cabinets.Items {
		devices = children(cabinet)
		if len(devices.Items) > 0 {
			break
		}
	}
	require.NotEmpty(t, devices.Items)
	require.Equal(t, "device", devices.Items[0].EntityType)
	data := get("device-context?" + url.Values{"source_id": {source}, "stable_id": {"A514359X5413481"}}.Encode())
	var contextGraph struct {
		Root      *repository.TopologyNode  `json:"root"`
		Nodes     []repository.TopologyNode `json:"nodes"`
		Edges     []repository.TopologyEdge `json:"edges"`
		Truncated bool                      `json:"truncated"`
		Warnings  []string                  `json:"warnings"`
	}
	require.NoError(t, json.Unmarshal(data, &contextGraph))
	require.NotNil(t, contextGraph.Root)
	counts := map[string]int{}
	for _, n := range contextGraph.Nodes {
		counts[n.EntityType]++
	}
	require.Positive(t, counts["cabinet"])
	require.Positive(t, counts["gpu"])
	require.Positive(t, counts["rpp"])
	require.Positive(t, counts["ups_group"])
	require.Positive(t, counts["transformer"])
	paths := map[string]int{}
	for _, e := range contextGraph.Edges {
		if path, ok := e.Properties["power_path"].(string); ok {
			paths[path]++
		}
	}
	require.Positive(t, paths["A"])
	require.Positive(t, paths["B"])
	t.Logf("目录IDC=%d POD页=%d 机柜页=%d 设备页=%d；设备关联nodes=%d edges=%d types=%v A/B=%v warnings=%v truncated=%v", len(dc.Items), len(pods.Items), len(cabinets.Items), len(devices.Items), len(contextGraph.Nodes), len(contextGraph.Edges), counts, paths, contextGraph.Warnings, contextGraph.Truncated)
}
