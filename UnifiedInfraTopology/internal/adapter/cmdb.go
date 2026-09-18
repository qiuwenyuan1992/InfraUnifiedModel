package adapter

import (
	"context"

	"UnifiedInfraTopology/internal/model"
)

// CMDB 是待接入的来源边界；当前不会发起网络请求或返回虚构资产。
type CMDB struct{}

func NewCMDB() *CMDB { return &CMDB{} }

func (*CMDB) Validate(ctx context.Context, _ model.Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrNotImplemented
}
