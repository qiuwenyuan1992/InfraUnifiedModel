package router

import (
	"context"
	"time"

	v1 "UnifiedInfraTopology/api/v1"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func InitInventoryRouter(deps RouterDeps, r *gin.RouterGroup) {
	g := r.Group("/scopes")
	g.Use(func(c *gin.Context) {
		if c.Writer.Header().Get("X-Request-ID") == "" {
			c.Header("X-Request-ID", uuid.NewString())
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		claims, err := deps.JWT.ParseToken(c.GetHeader("Authorization"))
		if err != nil || claims == nil || claims.UserId == "" {
			c.AbortWithStatusJSON(401, v1.Response{Code: 40101, Message: "unauthenticated", Data: gin.H{}})
			return
		}
		c.Set("claims", claims)
		c.Next()
	})
	h := deps.InventoryHandler
	g.GET("", h.List("scopes"))
	g.GET("/:scope_id/devices", h.List("devices"))
	g.GET("/:scope_id/devices/:device_id", h.GetDevice)
	g.GET("/:scope_id/devices/:device_id/interfaces", h.List("interfaces"))
	g.GET("/:scope_id/devices/:device_id/addresses", h.List("addresses"))
	g.GET("/:scope_id/sources", h.List("sources"))
	g.GET("/:scope_id/generations", h.List("generations"))
	g.GET("/:scope_id/sync-runs", h.List("sync-runs"))
	g.POST("/:scope_id/sync-runs", h.Enqueue)
	g.GET("/:scope_id/sync-runs/:run_id", h.GetRun)
	g.POST("/:scope_id/sync-runs/:run_id/cancel", h.CancelRun)
}
