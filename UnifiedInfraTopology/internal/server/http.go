package server

import (
	apiV1 "UnifiedInfraTopology/api/v1"
	"UnifiedInfraTopology/docs"
	"UnifiedInfraTopology/internal/middleware"
	"UnifiedInfraTopology/internal/router"
	"UnifiedInfraTopology/pkg/server/http"
	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func NewHTTPServer(
	deps router.RouterDeps,
) *http.Server {
	if deps.Config.GetString("env") == "prod" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	// 默认恢复器会打印请求和 panic 内容；这里只记录不含敏感载荷的事件。
	engine.Use(func(ctx *gin.Context) {
		defer func() {
			if recover() != nil {
				deps.Logger.WithContext(ctx).Error("request panic")
				ctx.AbortWithStatusJSON(500, apiV1.Response{Code: 50001, Message: "internal_error", Data: gin.H{}})
			}
		}()
		ctx.Next()
	})
	s := http.NewServer(
		engine,
		deps.Logger,
		http.WithServerHost(deps.Config.GetString("http.host")),
		http.WithServerPort(deps.Config.GetInt("http.port")),
	)

	// swagger doc
	docs.SwaggerInfo.BasePath = "/v1"
	s.GET("/swagger/*any", ginSwagger.WrapHandler(
		swaggerfiles.Handler,
		//ginSwagger.URL(fmt.Sprintf("http://localhost:%d/swagger/doc.json", deps.Config.GetInt("app.http.port"))),
		ginSwagger.DefaultModelsExpandDepth(-1),
		ginSwagger.PersistAuthorization(true),
	))

	s.Use(
		middleware.CORSMiddleware(),
		middleware.ResponseLogMiddleware(deps.Logger),
		middleware.RequestLogMiddleware(deps.Logger),
		//middleware.SignMiddleware(log),
	)
	s.GET("/", func(ctx *gin.Context) {
		deps.Logger.WithContext(ctx).Info("hello")
		apiV1.HandleSuccess(ctx, map[string]interface{}{
			":)": "Thank you for using nunu!",
		})
	})

	v1 := s.Group("/v1")
	router.InitUserRouter(deps, v1)
	router.InitInventoryRouter(deps, v1)

	return s
}
