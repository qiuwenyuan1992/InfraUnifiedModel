// scenarios.go 场景重建：按《跨域关联全景》12 场景（A-L）在新种子命名空间上重建。
// 老 demo 的 4 个场景（v2 命名空间）随 ResetAll 清除；场景由 seeder 显式定义与指派，
// 不做自动聚类臆造（与 service.UpsertScenario 语义一致）。
package seeder

import (
	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/service"
)

// ScenarioRebuildReport 场景重建统计。
type ScenarioRebuildReport struct {
	Scenarios int `json:"scenarios"`
	Assigned  int `json:"assigned"`
}

// scenarioDef 场景定义：code/name/domain/成员 stable_code 清单。
type scenarioDef struct {
	Code    string
	Name    string
	Domain  string
	Members []string
}

// scenarioDefs 全景 12 场景（A-L）→ 新命名空间成员。
var scenarioDefs = []scenarioDef{
	{
		Code: "scenario.power.rack", Name: "机柜掉电", Domain: "power",
		Members: []string{
			"power.grid.mains.abnormal", "power.ups.input_lost", "power.battery.discharge",
			"power.genset.fuel_low", "power.genset.start_fail", "power.ups.output_lost",
			"power.rack.busbar.input_lost", "power.rack.pdu.output_lost_dual", "power.rack.pdu.output_lost_single",
			"power.rack.cabinet.over_tech",
			"server.psu.input_lost", "server.psu.redundancy_lost", "server.sys.crash.unexpected",
			"net.port.down", "net.device.psu.fault",
		},
	},
	{
		Code: "scenario.cooling.chiller", Name: "制冷失效", Domain: "dci",
		Members: []string{
			"cooling.chilled_water.pump_fault", "cooling.cooling_water.pump_fault", "cooling.chiller.fault",
			"cooling.it_room.temp_mid", "cooling.it_room.temp_high", "cooling.it_room.temp_critical",
			"server.thermal.temp.critical", "net.device.thermal.alarm", "env.room.smoke",
			"gpu.perf.degrade", "server.fan.underspeed",
		},
	},
	{
		Code: "scenario.cooling.cdu", Name: "液冷 CDU 失效", Domain: "dci",
		Members: []string{
			"power.cdu.power.abnormal", "power.cdu.pump.fault_secondary", "power.cdu.supply_temp.abnormal_secondary",
			"power.cdu.leak", "server.liquid.leakage", "server.thermal.temp.critical",
		},
	},
	{
		Code: "scenario.net.link.port", Name: "网络链路与端口", Domain: "network",
		Members: []string{
			"net.port.down", "net.port.flapping", "net.port.crc_error", "net.port.optical_power.abnormal",
			"server.nic.link_down", "server.nic.crc_error", "server.optical.rx_power.abnormal",
			"net.traffic.sudden_drop", "net.traffic.zero",
		},
	},
	{
		Code: "scenario.net.ib", Name: "IB 网络中断", Domain: "network",
		Members: []string{"server.nic.ib.fabric", "net.ib.link.down"},
	},
	{
		Code: "scenario.net.protocol", Name: "路由协议震荡", Domain: "network",
		Members: []string{"net.protocol.bfd.down", "net.protocol.bgp.peer_down", "net.port.down"},
	},
	{
		Code: "scenario.server.thermal", Name: "服务器过温", Domain: "server",
		Members: []string{"server.fan.underspeed", "server.thermal.temp.critical", "gpu.perf.degrade", "cooling.it_room.temp_critical"},
	},
	{
		Code: "scenario.gpu.xid", Name: "GPU Xid 故障", Domain: "gpu",
		Members: []string{"gpu.xid.154", "gpu.reset.fail", "server.sys.reboot.unexpected", "gpu.perf.degrade"},
	},
	{
		Code: "scenario.storage.vd", Name: "存储链故障", Domain: "server",
		Members: []string{"server.board.backplane.fault", "storage.disk.fault", "storage.vd.degraded"},
	},
	{
		Code: "scenario.server.power", Name: "服务器电源冗余", Domain: "server",
		Members: []string{"server.psu.input_lost", "server.psu.redundancy_lost", "server.sys.crash.unexpected", "power.rack.pdu.output_lost_dual", "power.rack.pdu.output_lost_single"},
	},
	{
		Code: "scenario.net.device.psu", Name: "交换机电源故障", Domain: "network",
		Members: []string{"net.device.psu.fault", "net.port.down", "power.rack.pdu.output_lost_dual", "server.sys.bmc.unreachable"},
	},
	{
		Code: "scenario.recovery.storm", Name: "上电风暴（恢复期）", Domain: "power",
		Members: []string{"power.rack.pdu.output_lost_dual", "power.rack.cabinet.over_tech", "power.psu.fault"},
	},
}

// RebuildScenarios 在当前图上重建全部场景（先清场景表再指派；图数据不动）。
// 成员码不存在会报错不静默跳过（与 service.AssignScenarioNodes 同语义）。
func RebuildScenarios(svc *service.Service) (*ScenarioRebuildReport, error) {
	// 场景表先清（ResetAll 已清时无害；单独调用时保证幂等重建）
	for _, d := range scenarioDefs {
		if err := svc.UpsertScenario(&model.Scenario{Code: d.Code, Name: d.Name, Domain: d.Domain}); err != nil {
			return nil, err
		}
	}
	rep := &ScenarioRebuildReport{}
	for _, d := range scenarioDefs {
		n, err := svc.AssignScenarioNodes(d.Code, d.Members, "seeder")
		if err != nil {
			return nil, err
		}
		rep.Scenarios++
		rep.Assigned += n
	}
	return rep, nil
}
