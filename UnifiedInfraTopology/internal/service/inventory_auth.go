package service

import "github.com/spf13/viper"

// 免鉴权模式仍记录固定操作主体，不接受客户端伪造身份。
const InventoryAnonymousUser = "inventory-local-operator"

func InventoryAuthDisabled(conf *viper.Viper) bool {
	if conf == nil {
		return false
	}
	enabled, valid := conf.Get("inventory.auth.enabled").(bool)
	return valid && !enabled
}
