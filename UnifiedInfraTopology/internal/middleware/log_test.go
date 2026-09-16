package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogsExcludeCredentialsAndBodies(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	l := &log.Logger{Logger: zap.New(core)}
	r := gin.New()
	r.Use(RequestLogMiddleware(l), ResponseLogMiddleware(l))
	r.POST("/login", func(c *gin.Context) { c.JSON(200, gin.H{"token": "response-secret"}) })
	req := httptest.NewRequest("POST", "/login?accessToken=query-secret", strings.NewReader(`{"password":"body-secret"}`))
	req.Header.Set("Authorization", "Bearer header-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	for _, entry := range logs.All() {
		for _, value := range entry.ContextMap() {
			if s, ok := value.(string); ok {
				for _, secret := range []string{"query-secret", "body-secret", "header-secret", "response-secret"} {
					require.NotContains(t, s, secret)
				}
			}
		}
		require.NotContains(t, entry.ContextMap(), "request_headers")
	}
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
	require.Equal(t, "response-secret", strings.Split(w.Body.String(), `"`)[3])
}
