package server

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/router"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestHTTPServerDoesNotLogQueryOrPanicSecrets(t *testing.T) {
	var output bytes.Buffer
	old, oldError := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = &output, &output
	t.Cleanup(func() { gin.DefaultWriter, gin.DefaultErrorWriter = old, oldError })
	conf := viper.New()
	l := &log.Logger{Logger: zap.NewNop()}
	h := handler.NewHandler(l)
	s := NewHTTPServer(router.RouterDeps{Config: conf, Logger: l, JWT: jwt.NewJwt(conf), UserHandler: handler.NewUserHandler(h, nil), InventoryHandler: handler.NewInventoryHandler(h, nil), InventoryRoundHandler: handler.NewInventoryRoundHandler(h, nil)})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/?accessToken=query-secret", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "Location, X-Request-ID", w.Header().Get("Access-Control-Expose-Headers"))
	require.NotContains(t, output.String(), "query-secret")
	s.GET("/panic-test", func(c *gin.Context) { panic("panic-secret") })
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/panic-test?secret=query-secret", nil))
	require.Equal(t, 500, w.Code)
	require.NotContains(t, output.String(), "panic-secret")
	require.NotContains(t, output.String(), "query-secret")
}

func TestHTTPServerTopologyRoutesAuthAndUnavailableGraph(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		conf := viper.New()
		conf.Set("inventory.auth.enabled", !disabled)
		conf.Set("security.jwt.key", "topology-test-key")
		conf.Set("inventory.grants", []map[string]any{{"user_id": "reader", "permissions": []string{"sync:read"}}})
		l := &log.Logger{Logger: zap.NewNop()}
		h := handler.NewHandler(l)
		j := jwt.NewJwt(conf)
		s := NewHTTPServer(router.RouterDeps{Config: conf, Logger: l, JWT: j, UserHandler: handler.NewUserHandler(h, nil), InventoryHandler: handler.NewInventoryHandler(h, nil), InventoryRoundHandler: handler.NewInventoryRoundHandler(h, nil), TopologyBrowseHandler: handler.NewTopologyBrowseHandler(h, service.NewTopologyBrowseService(nil, conf))})
		for _, endpoint := range []string{"node", "neighbors"} {
			path := "/v1/inventory/topology/" + endpoint + "?source_id=" + strings.Repeat("a", 32) + "&entity_type=gpu&stable_id=SN"
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			if disabled {
				require.Equal(t, 503, w.Code)
			} else {
				require.Equal(t, 401, w.Code)
			}
			require.NotEmpty(t, w.Header().Get("X-Request-ID"))
			require.Equal(t, "Location, X-Request-ID", w.Header().Get("Access-Control-Expose-Headers"))
			if !disabled {
				token, err := j.GenToken("reader", time.Now().Add(time.Hour))
				require.NoError(t, err)
				req = httptest.NewRequest("GET", path, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				w = httptest.NewRecorder()
				s.ServeHTTP(w, req)
				require.Equal(t, 503, w.Code)
				require.Contains(t, w.Body.String(), "topology_not_ready")
				token, err = j.GenToken("ungranted", time.Now().Add(time.Hour))
				require.NoError(t, err)
				req = httptest.NewRequest("GET", path, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				w = httptest.NewRecorder()
				s.ServeHTTP(w, req)
				require.Equal(t, 403, w.Code)
			}
		}
	}
}
