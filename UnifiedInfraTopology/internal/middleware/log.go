package middleware

import (
	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"time"
)

func RequestLogMiddleware(logger *log.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestID := uuid.NewString()
		ctx.Header("X-Request-ID", requestID)
		logger.WithValue(ctx, zap.String("request_id", requestID))
		logger.WithValue(ctx, zap.String("request_method", ctx.Request.Method))
		// 不记录查询参数、请求头或载荷，避免凭据及上游数据泄漏。
		logger.WithValue(ctx, zap.String("request_path", ctx.Request.URL.Path))
		logger.WithContext(ctx).Info("Request")
		ctx.Next()
	}
}
func ResponseLogMiddleware(logger *log.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		startTime := time.Now()
		ctx.Next()
		duration := time.Since(startTime).String()
		logger.WithContext(ctx).Info("Response", zap.Int("status", ctx.Writer.Status()), zap.String("time", duration))
	}
}
