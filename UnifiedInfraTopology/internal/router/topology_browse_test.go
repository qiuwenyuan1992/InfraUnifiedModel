package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type topologyRouterStub struct {
	user     string
	deadline bool
}

func (s *topologyRouterStub) Node(ctx context.Context, user string, _ model.EntityIdentity) (*repository.TopologyNode, error) {
	s.user = user
	_, s.deadline = ctx.Deadline()
	return &repository.TopologyNode{ID: "node"}, nil
}
func (s *topologyRouterStub) Neighbors(ctx context.Context, user string, _ model.EntityIdentity, _ repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
	s.user = user
	_, s.deadline = ctx.Deadline()
	return &repository.TopologyNeighborhood{}, nil
}
func (s *topologyRouterStub) Roots(ctx context.Context, user, _ string, _ model.EntityType, _ repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	s.user = user
	_, s.deadline = ctx.Deadline()
	return &repository.TopologyDirectoryPage{}, nil
}
func (s *topologyRouterStub) Children(ctx context.Context, user string, _ model.EntityIdentity, _ repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	s.user = user
	_, s.deadline = ctx.Deadline()
	return &repository.TopologyDirectoryPage{}, nil
}
func (s *topologyRouterStub) DeviceContext(ctx context.Context, user string, _ model.EntityIdentity) (*service.TopologyDeviceContext, error) {
	s.user = user
	_, s.deadline = ctx.Deadline()
	return &service.TopologyDeviceContext{}, nil
}
func (s *topologyRouterStub) Scene(ctx context.Context, user string, _ model.EntityIdentity, _ repository.TopologyDirectoryQuery) (*service.TopologyScene, error) {
	s.user = user
	_, s.deadline = ctx.Deadline()
	return &service.TopologyScene{}, nil
}

func TestTopologyRoutesReuseInventoryMiddleware(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, endpoint := range []string{"node", "neighbors", "roots", "children", "device-context", "scene"} {
			c := viper.New()
			c.Set("inventory.auth.enabled", !disabled)
			l := &log.Logger{Logger: zap.NewNop()}
			stub := &topologyRouterStub{}
			base := handler.NewHandler(l)
			r := gin.New()
			InitInventoryRouter(RouterDeps{Config: c, Logger: l, JWT: jwt.NewJwt(c), InventoryHandler: handler.NewInventoryHandler(base, &inventoryStub{}), InventoryRoundHandler: handler.NewInventoryRoundHandler(base, &roundInventoryStub{}), TopologyBrowseHandler: handler.NewTopologyBrowseHandler(base, stub)}, r.Group("/v1"))
			args := "source_id=" + strings.Repeat("a", 32) + "&entity_type=gpu&stable_id=sn"
			if endpoint == "roots" {
				args = "source_id=" + strings.Repeat("a", 32) + "&entity_type=pod"
			}
			if endpoint == "children" || endpoint == "scene" {
				args = "source_id=" + strings.Repeat("a", 32) + "&entity_type=pod&stable_id=pod"
			}
			if endpoint == "device-context" {
				args = "source_id=" + strings.Repeat("a", 32) + "&stable_id=sn"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/inventory/topology/"+endpoint+"?"+args, nil))
			require.NotEmpty(t, w.Header().Get("X-Request-ID"))
			if disabled {
				require.Equal(t, 200, w.Code)
				require.Equal(t, service.InventoryAnonymousUser, stub.user)
				require.True(t, stub.deadline)
			} else {
				require.Equal(t, 401, w.Code)
				require.Empty(t, stub.user)
				token, err := jwt.NewJwt(c).GenToken("reader", time.Now().Add(time.Hour))
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodGet, "/v1/inventory/topology/"+endpoint+"?"+args, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				w = httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, 200, w.Code)
				require.Equal(t, "reader", stub.user)
				require.True(t, stub.deadline)
			}
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
				w = httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(method, "/v1/inventory/topology/"+endpoint, nil))
				require.Equal(t, 404, w.Code)
			}
		}
	}
}
