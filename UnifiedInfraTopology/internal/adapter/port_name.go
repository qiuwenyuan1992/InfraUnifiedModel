package adapter

import (
	"strings"
	"unicode"
)

// NormalizePortName统一端口匹配与保存：CMDB上联可能写FHGigabitEthernet 0/35，
// 端口目录却写FHGigabitEthernet0/35，故去除全部空白；SN、UUID及槽位不变。
func NormalizePortName(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}
