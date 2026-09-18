// Package adapter 定义外部数据源边界，不负责本项目的持久化或发布。
package adapter

import (
	"context"
	"errors"

	"UnifiedInfraTopology/internal/model"
)

var ErrNotImplemented = errors.New("source adapter is not implemented")

// SourceAdapter 仅定义接入前检查；采集契约在真实来源接入时补充。
// 实现不得在错误或日志中暴露 Source.ConfigRef 对应的凭据。
type SourceAdapter interface {
	Validate(context.Context, model.Source) error
}
