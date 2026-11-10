package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type topologyBrowseStub struct {
	calls          int
	user           string
	identity       model.EntityIdentity
	query          repository.TopologyNeighborQuery
	directoryQuery repository.TopologyDirectoryQuery
	err            error
}

func (s *topologyBrowseStub) Scene(_ context.Context, user string, i model.EntityIdentity, q repository.TopologyDirectoryQuery) (*service.TopologyScene, error) {
	s.calls++
	s.user, s.identity, s.directoryQuery = user, i, q
	return &service.TopologyScene{Root: service.SceneNode{ID: "root"}, Cabinets: []service.SceneCabinet{}, Unassigned: []service.SceneUnassigned{}, NetworkLevels: []service.SceneNetworkLevel{}, Nodes: []service.SceneNode{}, Edges: []service.SceneEdge{}, Warnings: []string{}}, s.err
}

func (s *topologyBrowseStub) Roots(_ context.Context, user, source string, kind model.EntityType, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	s.calls++
	s.user = user
	s.identity = model.EntityIdentity{SourceID: source, EntityType: kind}
	s.directoryQuery = q
	return &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{}, NextAfter: "opaque"}, s.err
}
func (s *topologyBrowseStub) Children(ctx context.Context, user string, i model.EntityIdentity, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	p, err := s.Roots(ctx, user, i.SourceID, i.EntityType, q)
	s.identity = i
	return p, err
}
func (s *topologyBrowseStub) DeviceContext(_ context.Context, user string, i model.EntityIdentity) (*service.TopologyDeviceContext, error) {
	s.calls++
	s.user = user
	s.identity = i
	return &service.TopologyDeviceContext{Root: &repository.TopologyNode{ID: "device"}, Nodes: []repository.TopologyNode{}, Edges: []repository.TopologyEdge{}, Warnings: []string{"missing_cabinet"}, Limit: 200}, s.err
}

func (s *topologyBrowseStub) Node(_ context.Context, user string, i model.EntityIdentity) (*repository.TopologyNode, error) {
	s.calls++
	s.user = user
	s.identity = i
	return &repository.TopologyNode{ID: "vertex", SourceID: i.SourceID, EntityType: string(i.EntityType), StableID: i.StableID, Properties: map[string]any{"serial": "gpu-SN"}}, s.err
}
func (s *topologyBrowseStub) Neighbors(_ context.Context, user string, i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
	s.calls++
	s.user = user
	s.identity = i
	s.query = q
	return &repository.TopologyNeighborhood{Root: &repository.TopologyNode{ID: "vertex"}, Nodes: []repository.TopologyNode{}, Edges: []repository.TopologyEdge{}, Limit: q.Limit}, s.err
}
func topologyRequest(t *testing.T, path string, stub *topologyBrowseStub) *httptest.ResponseRecorder {
	t.Helper()
	g := gin.New()
	g.Use(func(c *gin.Context) { c.Set("claims", &jwt.MyCustomClaims{UserId: "reader"}) })
	h := NewTopologyBrowseHandler(nil, stub)
	g.GET("/node", h.Node)
	g.GET("/neighbors", h.Neighbors)
	g.GET("/roots", h.Roots)
	g.GET("/children", h.Children)
	g.GET("/device-context", h.DeviceContext)
	g.GET("/scene", h.Scene)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestTopologyDirectoryAndContextHandler(t *testing.T) {
	source := "source_id=" + strings.Repeat("a", 32)
	for _, path := range []string{"/roots?" + source + "&entity_type=pod", "/children?" + source + "&entity_type=cabinet&stable_id=cab&limit=100&after=opaque", "/device-context?" + source + "&stable_id=server"} {
		s := &topologyBrowseStub{}
		w := topologyRequest(t, path, s)
		require.Equal(t, 200, w.Code)
		require.Equal(t, "reader", s.user)
		if strings.HasPrefix(path, "/roots") {
			require.Equal(t, 50, s.directoryQuery.Limit)
			require.Contains(t, w.Body.String(), `"next_after":"opaque"`)
		}
		if strings.HasPrefix(path, "/children") {
			require.Equal(t, 100, s.directoryQuery.Limit)
			require.Equal(t, "opaque", s.directoryQuery.After)
			require.Equal(t, "cab", s.identity.StableID)
		}
		if strings.HasPrefix(path, "/device-context") {
			require.Equal(t, model.EntityDevice, s.identity.EntityType)
			require.Contains(t, w.Body.String(), `"warnings":["missing_cabinet"]`)
		}
	}
	for _, path := range []string{"/roots?" + source + "&entity_type=device", "/roots?" + source + "&entity_type=pod&limit=0", "/roots?" + source + "&entity_type=pod&limit=101", "/roots?" + source + "&entity_type=pod&after=", "/children?" + source + "&entity_type=gpu&stable_id=x", "/children?" + source + "&entity_type=pod&stable_id=x&after=" + strings.Repeat("x", 4097), "/device-context?" + source + "&stable_id=x&entity_type=device", "/device-context?" + source + "&stable_id=x&limit=1", "/device-context?" + source} {
		s := &topologyBrowseStub{}
		w := topologyRequest(t, path, s)
		require.Equal(t, 400, w.Code, path)
		require.Zero(t, s.calls)
	}
}

func TestTopologySceneHandlerContract(t *testing.T) {
	base := "/scene?source_id=" + strings.Repeat("a", 32) + "&entity_type=pod&stable_id=pod"
	s := &topologyBrowseStub{}
	w := topologyRequest(t, base, s)
	require.Equal(t, 200, w.Code)
	require.Equal(t, 5, s.directoryQuery.Limit)
	require.Equal(t, "reader", s.user)
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data, 9)
	for _, key := range []string{"root", "cabinets", "unassigned", "network_levels", "nodes", "edges", "next_after", "truncated", "warnings"} {
		require.Contains(t, body.Data, key)
	}
	for _, extra := range []string{"&limit=11", "&limit=0", "&limit=no", "&after=", "&limit=5&limit=6", "&properties=1"} {
		s = &topologyBrowseStub{}
		w = topologyRequest(t, base+extra, s)
		require.Equal(t, 400, w.Code)
		require.Zero(t, s.calls)
	}
	s = &topologyBrowseStub{}
	w = topologyRequest(t, strings.Replace(base, "entity_type=pod", "entity_type=device", 1), s)
	require.Equal(t, 400, w.Code)
	require.Zero(t, s.calls)
	s = &topologyBrowseStub{}
	w = topologyRequest(t, base+"&limit=10&after=opaque", s)
	require.Equal(t, 200, w.Code)
	require.Equal(t, 10, s.directoryQuery.Limit)
	require.Equal(t, "opaque", s.directoryQuery.After)
	s = &topologyBrowseStub{err: context.DeadlineExceeded}
	w = topologyRequest(t, base, s)
	require.Equal(t, 504, w.Code)
	s = &topologyBrowseStub{err: service.ErrInventoryNotReady}
	w = topologyRequest(t, base, s)
	require.Equal(t, 503, w.Code)
}
func topologyArgs() string {
	return "source_id=" + strings.Repeat("a", 32) + "&entity_type=gpu&stable_id=gpu-SN"
}
func TestTopologyBrowseHandlerStrictQuery(t *testing.T) {
	for _, path := range []string{"/node", "/node?" + topologyArgs() + "&unknown=x", "/node?" + topologyArgs() + "&stable_id=again", "/node?" + topologyArgs() + "&limit=100", "/node?source_id=&entity_type=gpu&stable_id=x", "/node?source_id=" + strings.Repeat("A", 32) + "&entity_type=gpu&stable_id=x", "/node?source_id=" + strings.Repeat("a", 32) + "&entity_type=ups&stable_id=01", "/neighbors?" + topologyArgs() + "&limit=0", "/neighbors?" + topologyArgs() + "&limit=201", "/neighbors?" + topologyArgs() + "&direction=other", "/neighbors?" + topologyArgs() + "&edge_type=other", "/neighbors?" + topologyArgs() + "&direction="} {
		t.Run(path, func(t *testing.T) {
			s := &topologyBrowseStub{}
			w := topologyRequest(t, path, s)
			require.Equal(t, 400, w.Code)
			require.Zero(t, s.calls)
		})
	}
}
func TestTopologyBrowseHandlerResponseAndDefaults(t *testing.T) {
	s := &topologyBrowseStub{}
	w := topologyRequest(t, "/node?"+topologyArgs(), s)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "reader", s.user)
	require.Equal(t, "gpu-SN", s.identity.StableID)
	var body struct {
		Code int                     `json:"code"`
		Data repository.TopologyNode `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Zero(t, body.Code)
	require.Equal(t, "vertex", body.Data.ID)
	require.Equal(t, "gpu-SN", body.Data.Properties["serial"])
	w = topologyRequest(t, "/neighbors?"+topologyArgs(), s)
	require.Equal(t, 200, w.Code)
	require.Equal(t, 100, s.query.Limit)
	require.Equal(t, "both", s.query.Direction)
	w = topologyRequest(t, "/neighbors?"+topologyArgs()+"&limit=200&direction=in&edge_type=network_relation", s)
	require.Equal(t, 200, w.Code)
	require.Equal(t, 200, s.query.Limit)
	require.Equal(t, "in", s.query.Direction)
	require.Equal(t, model.EdgeNetwork, s.query.EdgeType)
}
func TestTopologyBrowseHandlerErrorEnvelope(t *testing.T) {
	for _, tc := range []struct {
		err          error
		status, code int
	}{{service.ErrInventoryInvalid, 400, 40001}, {service.ErrInventoryForbidden, 403, 40301}, {service.ErrInventoryNotFound, 404, 40401}, {service.ErrInventoryNotReady, 503, 50301}, {context.DeadlineExceeded, 504, 50401}} {
		for _, endpoint := range []string{"/node", "/neighbors"} {
			w := topologyRequest(t, endpoint+"?"+topologyArgs(), &topologyBrowseStub{err: tc.err})
			require.Equal(t, tc.status, w.Code)
			var body struct {
				Code int `json:"code"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, tc.code, body.Code)
		}
	}
}
