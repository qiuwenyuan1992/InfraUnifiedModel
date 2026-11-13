package router

import (
	"context"
	"time"

	v1 "UnifiedInfraTopology/api/v1"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func InitInventoryRouter(deps RouterDeps, r *gin.RouterGroup) {
	authDisabled := service.InventoryAuthDisabled(deps.Config)
	if authDisabled {
		deps.Logger.Warn("同步管理API鉴权已关闭，仅可用于受控本地或内网环境")
	}
	g := r.Group("/inventory")
	g.Use(func(c *gin.Context) {
		if c.Writer.Header().Get("X-Request-ID") == "" {
			c.Header("X-Request-ID", uuid.NewString())
		}
		timeout := 5 * time.Second
		if c.Request.Method == "GET" && c.FullPath() == g.BasePath()+"/topology/scene" {
			timeout = service.TopologySceneTimeout
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if authDisabled {
			c.Set("claims", &jwt.MyCustomClaims{UserId: service.InventoryAnonymousUser})
			c.Next()
			return
		}
		claims, err := deps.JWT.ParseToken(c.GetHeader("Authorization"))
		if err != nil || claims == nil || claims.UserId == "" {
			c.AbortWithStatusJSON(401, v1.Response{Code: 40101, Message: "unauthenticated", Data: gin.H{}})
			return
		}
		c.Set("claims", claims)
		c.Next()
	})
	h := deps.InventoryHandler
	g.GET("/sources", h.List("sources"))
	g.GET("/sync-runs", h.List("sync-runs"))
	g.POST("/sync-runs", h.Enqueue)
	g.GET("/sync-runs/:run_id", h.GetRun)
	g.POST("/sync-runs/:run_id/cancel", h.CancelRun)
	rounds := deps.InventoryRoundHandler
	g.POST("/sync-rounds", rounds.Enqueue)
	g.GET("/sync-rounds", rounds.List)
	g.GET("/sync-rounds/:id", rounds.Get)
	g.POST("/sync-rounds/:id/cancel", rounds.Cancel)
	g.GET("/sync-rounds/:id/diagnostics", rounds.Diagnostics(true))
	g.GET("/sync-runs/:run_id/diagnostics", rounds.Diagnostics(false))
	if topology := deps.TopologyBrowseHandler; topology != nil {
		g.GET("/topology/node", topology.Node)
		g.GET("/topology/neighbors", topology.Neighbors)
		g.GET("/topology/roots", topology.Roots)
		g.GET("/topology/children", topology.Children)
		g.GET("/topology/device-context", topology.DeviceContext)
		g.GET("/topology/room-power", topology.RoomPower)
		g.GET("/topology/scene", topology.Scene)
	}
}
