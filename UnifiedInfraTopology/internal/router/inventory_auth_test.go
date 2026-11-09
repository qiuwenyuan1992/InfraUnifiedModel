package router

import (
	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInventoryAuthDisabledUsesFixedActor(t *testing.T) {
	stub := &inventoryStub{}
	conf := viper.New()
	conf.Set("inventory.auth.enabled", false)
	l := &log.Logger{Logger: zap.NewNop()}
	r := gin.New()
	InitInventoryRouter(RouterDeps{JWT: jwt.NewJwt(conf), Logger: l, Config: conf, InventoryHandler: handler.NewInventoryHandler(handler.NewHandler(l), stub), InventoryRoundHandler: handler.NewInventoryRoundHandler(handler.NewHandler(l), &roundInventoryStub{})}, r.Group("/v1"))
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/inventory/sources", nil))
	require.Equal(t, 200, response.Code)
	require.Equal(t, service.InventoryAnonymousUser, stub.user)
}
