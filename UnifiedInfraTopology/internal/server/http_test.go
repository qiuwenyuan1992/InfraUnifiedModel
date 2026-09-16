package server

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/router"
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
	s := NewHTTPServer(router.RouterDeps{Config: conf, Logger: l, JWT: jwt.NewJwt(conf), UserHandler: handler.NewUserHandler(h, nil), InventoryHandler: handler.NewInventoryHandler(h, nil)})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/?accessToken=query-secret", nil))
	require.Equal(t, 200, w.Code)
	require.NotContains(t, output.String(), "query-secret")
	s.GET("/panic-test", func(c *gin.Context) { panic("panic-secret") })
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/panic-test?secret=query-secret", nil))
	require.Equal(t, 500, w.Code)
	require.NotContains(t, output.String(), "panic-secret")
	require.NotContains(t, output.String(), "query-secret")
}
