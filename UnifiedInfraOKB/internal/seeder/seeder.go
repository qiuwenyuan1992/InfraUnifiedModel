// Package seeder 把《UnifiedInfrastructureModel/因果边清单-draft.md》的结构化种子
// 经摄取接口灌入 OKB。产物全部为 draft（AI 提候选、人定稿）；证据级按清单 §八 映射：
// runtime → alert_event 源（runtime_verified）/ ind → industry_report 源（doc_explicit）/
// infer → 空 kind（inferred，结构推断无出处）。
// 摄取顺序：infer 组（全部节点定义，策略声明等级）→ runtime 组（实测等级，演示样本优先仲裁）
// → ind 组（行业案例级联）。内容寻址幂等：重复执行不虚增。
package seeder

import (
	"context"
	"strings"

	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/service"
)

// Report 灌库统计。
type Report struct {
	Sources   int `json:"sources"`
	Anomalies int `json:"anomalies"`
	Rules     int `json:"rules"`
	Mappings  int `json:"mappings"`
}

// Seed 灌入全部种子；返回新建统计（重复内容跳过）。
func Seed(svc *service.Service) (*Report, error) {
	rep := &Report{}
	groups := []struct {
		kind  model.SourceKind
		title string
		frags []string
	}{
		{kind: "", title: "种子-结构推断草稿（因果边清单：节点定义+电力/制冷/液冷/网络/服务器 infer 边）", frags: inferFragments()},
		{kind: model.SourceAlertEvent, title: "种子-真实告警实证（因果边清单 runtime 行+实测等级仲裁）", frags: runtimeFragments()},
		{kind: model.SourceIndustryReport, title: "种子-行业案例推导（蓄冷缓冲与温升次生火灾）", frags: industryFragments()},
	}
	for _, g := range groups {
		out, err := svc.Ingest(context.Background(), service.IngestInput{
			Kind: g.kind, Title: g.title,
			Content:    strings.Join(g.frags, "\n\n"),
			OperatorID: "seeder",
		})
		if err != nil {
			return nil, err
		}
		if out.DuplicateContent {
			continue
		}
		rep.Sources++
		rep.Anomalies += out.NewAnomalies
		rep.Rules += out.NewRules
		rep.Mappings += out.NewMappings
	}
	return rep, nil
}

// ---------- infer 组：节点定义 + 结构推断边（清单 §一 infer 行 + §三—§七 infer 行） ----------

func inferFragments() []string {
	frags := []string{
		// 节点定义（severity 为策略/结构声明值；实测值由 runtime 组仲裁覆盖）
		`{"anomaly": "power.grid.mains.abnormal", "code": "power.grid.mains.abnormal", "name": "市电中断"}`,
		`{"anomaly": "power.ups.input_lost", "code": "power.ups.input_lost", "name": "UPS 输入丢失"}`,
		`{"anomaly": "power.battery.discharge", "code": "power.battery.discharge", "name": "电池放电", "node_kind": "event"}`,
		`{"anomaly": "power.genset.start", "code": "power.genset.start", "name": "柴发启动", "node_kind": "event"}`,
		`{"anomaly": "power.genset.fuel_low", "code": "power.genset.fuel_low", "name": "柴发燃油低"}`,
		`{"anomaly": "power.genset.start_fail", "code": "power.genset.start_fail", "name": "柴发启动失败（预留）", "node_kind": "placeholder"}`,
		`{"anomaly": "power.ups.output_lost", "code": "power.ups.output_lost", "name": "UPS 输出丢失"}`,
		`{"anomaly": "power.rack.busbar.input_lost", "code": "power.rack.busbar.input_lost", "name": "机柜母线掉电"}`,
		`{"anomaly": "power.rack.pdu.output_lost_dual", "code": "power.rack.pdu.output_lost_dual", "name": "PDU 双路掉电"}`,
		`{"anomaly": "power.rack.pdu.output_lost_single", "code": "power.rack.pdu.output_lost_single", "name": "PDU 单路掉电"}`,
		`{"anomaly": "power.rack.cabinet.over_tech", "code": "power.rack.cabinet.over_tech", "name": "机柜超技术规格（上电涌流）"}`,
		`{"anomaly": "power.psu.fault", "code": "power.psu.fault", "name": "动环电源故障", "severity_online": "P4"}`,
		`{"anomaly": "server.psu.input_lost", "code": "server.psu.input_lost", "name": "服务器电源输入丢失", "severity_online": "P2", "clock_hint": "seconds"}`,
		`{"anomaly": "server.psu.redundancy_lost", "code": "server.psu.redundancy_lost", "name": "服务器电源冗余丢失"}`,
		`{"anomaly": "server.sys.crash.unexpected", "code": "server.sys.crash.unexpected", "name": "服务器异常宕机", "clock_hint": "seconds"}`,
		`{"anomaly": "net.port.down", "code": "net.port.down", "name": "交换机端口 Down"}`,
		`{"anomaly": "server.nic.link_down", "code": "server.nic.link_down", "name": "网卡链路 Down"}`,
		`{"anomaly": "server.nic.ib.fabric", "code": "server.nic.ib.fabric", "name": "IB Fabric 异常"}`,
		`{"anomaly": "net.ib.link.down", "code": "net.ib.link.down", "name": "IB 链路 Down"}`,
		`{"anomaly": "net.device.psu.fault", "code": "net.device.psu.fault", "name": "交换机电源故障"}`,
		`{"anomaly": "server.optical.rx_power.abnormal", "code": "server.optical.rx_power.abnormal", "name": "光模块接收功率异常"}`,
		`{"anomaly": "net.port.optical_power.abnormal", "code": "net.port.optical_power.abnormal", "name": "端口光功率异常（预留）", "node_kind": "placeholder"}`,
		`{"anomaly": "server.nic.crc_error", "code": "server.nic.crc_error", "name": "网卡 CRC 错误"}`,
		`{"anomaly": "net.port.crc_error", "code": "net.port.crc_error", "name": "端口 CRC 错误"}`,
		`{"anomaly": "power.cdu.leak", "code": "power.cdu.leak", "name": "CDU 漏液"}`,
		`{"anomaly": "server.liquid.leakage", "code": "server.liquid.leakage", "name": "服务器漏液"}`,
		`{"anomaly": "server.sys.heartbeat_lost", "code": "server.sys.heartbeat_lost", "name": "服务器失联（探活预留）", "node_kind": "placeholder"}`,
		`{"anomaly": "net.ping.reachable_fail", "code": "net.ping.reachable_fail", "name": "Ping 不可达（探活预留）", "node_kind": "placeholder"}`,
		`{"anomaly": "cooling.chilled_water.pump_fault", "code": "cooling.chilled_water.pump_fault", "name": "冷冻水泵故障"}`,
		`{"anomaly": "cooling.cooling_water.pump_fault", "code": "cooling.cooling_water.pump_fault", "name": "冷却水泵故障"}`,
		`{"anomaly": "cooling.chiller.fault", "code": "cooling.chiller.fault", "name": "冷机故障"}`,
		`{"anomaly": "cooling.it_room.temp_mid", "code": "cooling.it_room.temp_mid", "name": "机房温度中限告警"}`,
		`{"anomaly": "cooling.it_room.temp_high", "code": "cooling.it_room.temp_high", "name": "机房温度上限告警"}`,
		`{"anomaly": "cooling.it_room.temp_critical", "code": "cooling.it_room.temp_critical", "name": "机房温度严重告警", "clock_hint": "minutes"}`,
		`{"anomaly": "server.thermal.temp.critical", "code": "server.thermal.temp.critical", "name": "服务器过温严重告警", "clock_hint": "seconds"}`,
		`{"anomaly": "env.room.smoke", "code": "env.room.smoke", "name": "机房烟雾（预留）", "node_kind": "placeholder", "clock_hint": "hours"}`,
		`{"anomaly": "net.device.thermal.alarm", "code": "net.device.thermal.alarm", "name": "交换机温度告警"}`,
		`{"anomaly": "gpu.perf.degrade", "code": "gpu.perf.degrade", "name": "GPU 降频"}`,
		`{"anomaly": "power.cdu.power.abnormal", "code": "power.cdu.power.abnormal", "name": "CDU 供电异常"}`,
		`{"anomaly": "power.cdu.pump.fault_secondary", "code": "power.cdu.pump.fault_secondary", "name": "CDU 二次泵故障"}`,
		`{"anomaly": "power.cdu.supply_temp.abnormal_secondary", "code": "power.cdu.supply_temp.abnormal_secondary", "name": "CDU 一次侧供液温度异常"}`,
		`{"anomaly": "net.protocol.bfd.down", "code": "net.protocol.bfd.down", "name": "BFD Down"}`,
		`{"anomaly": "net.protocol.bgp.peer_down", "code": "net.protocol.bgp.peer_down", "name": "BGP 邻居 Down"}`,
		`{"anomaly": "net.port.flapping", "code": "net.port.flapping", "name": "端口震荡"}`,
		`{"anomaly": "net.traffic.sudden_drop", "code": "net.traffic.sudden_drop", "name": "流量突降"}`,
		`{"anomaly": "net.traffic.zero", "code": "net.traffic.zero", "name": "流量归零"}`,
		`{"anomaly": "server.sys.bmc.unreachable", "code": "server.sys.bmc.unreachable", "name": "BMC 失联（预留）", "node_kind": "placeholder"}`,
		`{"anomaly": "server.cpu.tsc", "code": "server.cpu.tsc", "name": "TSC 时钟源异常"}`,
		`{"anomaly": "server.cpu.clock.switch.hpet", "code": "server.cpu.clock.switch.hpet", "name": "时钟源切换至 HPET"}`,
		`{"anomaly": "storage.disk.fault", "code": "storage.disk.fault", "name": "硬盘故障"}`,
		`{"anomaly": "storage.vd.degraded", "code": "storage.vd.degraded", "name": "虚拟盘降级"}`,
		`{"anomaly": "server.board.backplane.fault", "code": "server.board.backplane.fault", "name": "背板故障"}`,
		`{"anomaly": "server.fan.underspeed", "code": "server.fan.underspeed", "name": "风扇转速低"}`,
		`{"anomaly": "gpu.xid.154", "code": "gpu.xid.154", "name": "GPU Xid 154（需恢复动作）"}`,
		`{"anomaly": "server.sys.reboot.unexpected", "code": "server.sys.reboot.unexpected", "name": "服务器非计划重启"}`,
		`{"anomaly": "gpu.reset.fail", "code": "gpu.reset.fail", "name": "GPU Reset 失败"}`,

		// §一 same_event（infer 行）
		`{"source": "server.nic.link_down", "target": "net.port.down", "edge_kind": "same_event"}`,
		`{"source": "server.psu.input_lost", "target": "power.rack.pdu.output_lost_dual", "edge_kind": "same_event"}`,
		`{"source": "server.psu.redundancy_lost", "target": "power.rack.pdu.output_lost_single", "edge_kind": "same_event"}`,
		`{"source": "net.device.psu.fault", "target": "power.rack.pdu.output_lost_dual", "edge_kind": "same_event"}`,
		// §二 same_family（infer 行）
		`{"source": "server.optical.rx_power.abnormal", "target": "net.port.optical_power.abnormal", "edge_kind": "same_family"}`,
		`{"source": "server.nic.crc_error", "target": "net.port.crc_error", "edge_kind": "same_family"}`,
		`{"source": "server.sys.heartbeat_lost", "target": "net.ping.reachable_fail", "edge_kind": "same_family"}`,

		// §三 电力链（秒级）
		`{"source": "power.grid.mains.abnormal", "target": "power.ups.input_lost", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.ups.input_lost", "target": "power.battery.discharge", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.grid.mains.abnormal", "target": "power.genset.start", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.battery.discharge", "target": "power.ups.output_lost", "conditions": [{"fact_key": "backup_power_available", "op": "eq", "value": "no"}], "time_constraints": "{\"delay_tier\":\"hours\"}"}`,
		`{"source": "power.genset.fuel_low", "target": "power.ups.output_lost", "conditions": [{"fact_key": "mains_abnormal", "op": "eq", "value": "yes"}], "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		`{"source": "power.grid.mains.abnormal", "target": "power.ups.output_lost", "conditions": [{"fact_key": "ups_mode", "op": "eq", "value": "bypass"}], "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.genset.start_fail", "target": "power.ups.output_lost", "conditions": [{"fact_key": "mains_abnormal", "op": "eq", "value": "yes"}], "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.ups.output_lost", "target": "power.rack.busbar.input_lost", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.rack.busbar.input_lost", "target": "power.rack.pdu.output_lost_dual", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.rack.pdu.output_lost_dual", "target": "net.port.down", "conditions": [{"fact_key": "same_rack_tor_batch", "op": "eq", "value": "yes"}], "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.rack.pdu.output_lost_dual", "target": "power.rack.cabinet.over_tech", "conditions": [{"fact_key": "power_restore_window", "op": "eq", "value": "yes"}], "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		// §四 制冷链（ind 行在 industryFragments；此处为 infer 行）
		`{"source": "cooling.chilled_water.pump_fault", "target": "cooling.chiller.fault", "conditions": [{"fact_key": "chilled_water_interlock", "op": "eq", "value": "tripped"}], "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "cooling.cooling_water.pump_fault", "target": "cooling.chiller.fault", "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		`{"source": "cooling.it_room.temp_mid", "target": "cooling.it_room.temp_high", "conditions": [{"fact_key": "no_intervention", "op": "eq", "value": "yes"}], "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		`{"source": "cooling.it_room.temp_high", "target": "cooling.it_room.temp_critical", "conditions": [{"fact_key": "no_intervention", "op": "eq", "value": "yes"}]}`,
		`{"source": "cooling.it_room.temp_critical", "target": "server.thermal.temp.critical", "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		`{"source": "cooling.it_room.temp_critical", "target": "net.device.thermal.alarm", "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		// §五 液冷链（分钟级）
		`{"source": "power.cdu.power.abnormal", "target": "power.cdu.pump.fault_secondary", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "power.cdu.pump.fault_secondary", "target": "power.cdu.supply_temp.abnormal_secondary", "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		`{"source": "power.cdu.supply_temp.abnormal_secondary", "target": "server.thermal.temp.critical", "conditions": [{"fact_key": "air_cooling_backup", "op": "eq", "value": "no"}], "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		// §六 网络域（秒级）
		`{"source": "net.device.psu.fault", "target": "net.port.down", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "net.protocol.bfd.down", "target": "net.protocol.bgp.peer_down", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "net.port.flapping", "target": "net.port.down", "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		`{"source": "net.port.down", "target": "net.traffic.sudden_drop", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "net.port.down", "target": "net.traffic.zero", "conditions": [{"fact_key": "single_uplink", "op": "eq", "value": "yes"}], "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "net.device.psu.fault", "target": "server.sys.bmc.unreachable", "conditions": [{"fact_key": "device_role", "op": "eq", "value": "MGT"}], "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		// §七 服务器域（infer 行）
		`{"source": "server.board.backplane.fault", "target": "storage.disk.fault", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		`{"source": "server.fan.underspeed", "target": "server.thermal.temp.critical", "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		`{"source": "gpu.xid.154", "target": "gpu.reset.fail", "conditions": [{"fact_key": "recovery_action", "op": "eq", "value": "0x1"}], "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		// 告警入口（infer 级）
		`{"anomaly": "net.port.down", "alert": "ALERT-LINK-DOWN"}`,
	}
	return frags
}

// ---------- runtime 组：真实告警实证（清单 runtime 行 + 实测等级仲裁） ----------

func runtimeFragments() []string {
	return []string{
		// 实测等级覆盖策略声明：P4 → P3（样本优先仲裁，仲裁走新修订留痕）
		`{"anomaly": "power.psu.fault", "code": "power.psu.fault", "name": "动环电源故障", "severity_online": "P3", "clock_hint": "minutes", "alert": "T30alertPsuFault"}`,
		// 电池放电：实测等级补位（事件类，分钟时钟）
		`{"anomaly": "power.battery.discharge", "code": "power.battery.discharge", "name": "电池放电", "severity_online": "P3", "clock_hint": "minutes", "alert": "T30alertUpsBatteryDischarge"}`,
		// §一 same_event（runtime 行）：事件同窗实证
		`{"source": "power.battery.discharge", "target": "power.ups.input_lost", "edge_kind": "same_event"}`,
		`{"source": "server.nic.ib.fabric", "target": "net.ib.link.down", "edge_kind": "same_event"}`,
		// §二 same_family（runtime 行）：CDU 漏液样本
		`{"source": "power.cdu.leak", "target": "server.liquid.leakage", "edge_kind": "same_family"}`,
		// 掉电聚合实证：双路失 → 宕机（秒级）
		`{"source": "server.psu.input_lost", "target": "server.sys.crash.unexpected", "conditions": [{"fact_key": "psu_redundancy", "op": "eq", "value": "lost"}], "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		// GPU Xid 154 样本实证：恢复动作 0x2 → 节点重启（分钟级）
		`{"anomaly": "gpu.xid.154", "code": "gpu.xid.154", "name": "GPU Xid 154（需恢复动作）", "alert": "GPU_XID_154"}`,
		`{"source": "gpu.xid.154", "target": "server.sys.reboot.unexpected", "conditions": [{"fact_key": "recovery_action", "op": "eq", "value": "0x2"}], "time_constraints": "{\"delay_tier\":\"minutes\"}"}`,
		// CPU 字典因果对（秒级）
		`{"source": "server.cpu.tsc", "target": "server.cpu.clock.switch.hpet", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
		// 存储状态机实证：PD → VD（秒级）
		`{"source": "storage.disk.fault", "target": "storage.vd.degraded", "time_constraints": "{\"delay_tier\":\"seconds\"}"}`,
	}
}

// ---------- ind 组：公开行业案例推导（doc_explicit） ----------

func industryFragments() []string {
	return []string{
		// 蓄冷缓冲消耗：集中制冷失效后温升呈小时级（行业案例：蓄冷 10 小时级）
		`{"source": "cooling.chiller.fault", "target": "cooling.it_room.temp_mid", "time_constraints": "{\"delay_tier\":\"hours\"}"}`,
		// 温升失控次生火灾（行业案例实证：漏液→冷机停→温升→火灾）
		`{"source": "cooling.it_room.temp_critical", "target": "env.room.smoke", "conditions": [{"fact_key": "temp_extreme_sustained", "op": "eq", "value": "yes"}], "time_constraints": "{\"delay_tier\":\"hours\"}"}`,
	}
}
