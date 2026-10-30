package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

type uplinkScopeReference struct{ ServerSN, ToRSN, PortName string }
type uplinkScopeDevice struct{ InstanceID, IDC int64 }
type uplinkScopeTarget struct {
	Devices []uplinkScopeDevice
	Ports   map[string]string
	Reasons map[string]string
}

func TestLiveServerUplinkScopeReadOnly(t *testing.T) {
	path := os.Getenv("SERVER_UPLINK_SCOPE_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后对比全局与 IDC1030 解析；仅查询 CMDB，不写数据库")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	source := model.Source{ID: "00000000000000000000000000001030", ConfigRef: "idc1030"}
	cfg, err := c.configFor(source)
	require.NoError(t, err)
	cfg.PageSize = 200
	cfg.DeviceCondition = map[string]any{"idc_id": 1030, "parent_type_id": 52}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	servers, err := uplinkScopeReadPages(ctx, c, cfg, deviceViewEndpoint, cfg.DeviceCondition, []string{"inst_id", "device_sn", "parent_type_id", "server_tor_ports"})
	require.NoError(t, err)
	seenServers, tors := map[string]bool{}, map[string]bool{}
	var references []uplinkScopeReference
	invalidEntries := 0
	for _, server := range servers {
		sn, err := uplinkText(server, "device_sn")
		require.NoError(t, err)
		category, err := uplinkScopeInteger(server, "parent_type_id")
		require.NoError(t, err)
		require.EqualValues(t, 52, category)
		require.NotEmpty(t, sn)
		require.False(t, seenServers[sn], "duplicate server SN %q", sn)
		seenServers[sn] = true
		var entries []map[string]json.RawMessage
		if raw := server["server_tor_ports"]; len(raw) > 0 {
			require.NoError(t, json.Unmarshal(raw, &entries))
		}
		for _, entry := range entries {
			tor, err := uplinkText(entry, "sn")
			require.NoError(t, err)
			var names []*string
			if raw := entry["ports"]; len(raw) > 0 {
				require.NoError(t, json.Unmarshal(raw, &names))
			}
			if tor != "" {
				tors[tor] = true
			}
			if tor == "" || len(names) == 0 {
				invalidEntries++
				continue
			}
			for _, name := range names {
				value := ""
				if name != nil {
					value = strings.TrimSpace(*name)
				}
				references = append(references, uplinkScopeReference{sn, tor, value})
			}
		}
	}
	ordered := make([]string, 0, len(tors))
	for tor := range tors {
		ordered = append(ordered, tor)
	}
	sort.Strings(ordered)
	global, scoped := map[string]uplinkScopeTarget{}, map[string]uplinkScopeTarget{}
	zero, unique, multiple, introduced, outside := 0, 0, 0, 0, 0
	for _, tor := range ordered {
		g, err := uplinkScopeResolve(ctx, c, cfg, tor, 0)
		require.NoError(t, err, "global ToR %q", tor)
		s, err := uplinkScopeResolve(ctx, c, cfg, tor, 1030)
		require.NoError(t, err, "scoped ToR %q", tor)
		global[tor], scoped[tor] = g, s
		t.Logf("tor_resolution tor_sn=%q global_device_count=%d scoped_device_count=%d devices=%+v", tor, len(g.Devices), len(s.Devices), g.Devices)
		inside := 0
		for _, device := range g.Devices {
			if device.IDC == 1030 {
				inside++
			}
		}
		switch len(g.Devices) {
		case 0:
			zero++
		case 1:
			unique++
			if inside == 0 {
				outside++
				t.Logf("outside_idc tor_sn=%q devices=%+v", tor, g.Devices)
			}
		default:
			multiple++
			if inside == 1 {
				introduced++
			}
			t.Logf("duplicate_network_sn tor_sn=%q devices=%+v idc1030_matches=%d global_ambiguity_introduced=%t", tor, g.Devices, inside, inside == 1)
		}
	}
	candidates, globalIDs, scopedIDs := map[uplinkScopeReference]bool{}, map[string]bool{}, map[string]bool{}
	changed, reasons := map[string]bool{}, map[string]int{}
	outsideRefs, samples := 0, 0
	for _, ref := range references {
		if candidates[ref] {
			continue
		}
		candidates[ref] = true
		g, s := global[ref.ToRSN], scoped[ref.ToRSN]
		if len(g.Devices) == 1 && g.Devices[0].IDC != 1030 {
			outsideRefs++
		}
		guuid, suuid := g.Ports[ref.PortName], s.Ports[ref.PortName]
		for _, resolved := range []struct {
			uuid string
			ids  map[string]bool
		}{{guuid, globalIDs}, {suuid, scopedIDs}} {
			if resolved.uuid == "" {
				continue
			}
			identity := model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgeNetwork, Kind: model.RelationServerUplink, From: model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: ref.ServerSN}, To: model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityInterface, StableID: resolved.uuid}}
			id, err := identity.ID()
			require.NoError(t, err)
			resolved.ids[id] = true
		}
		if guuid == suuid {
			continue
		}
		changed[ref.ToRSN] = true
		reason := "different_port_uuid"
		if len(g.Devices) > 1 {
			reason = "global_network_sn_ambiguous"
		} else if len(g.Devices) == 1 && g.Devices[0].IDC != 1030 {
			reason = "outside_idc"
		} else if guuid == "" {
			reason = "global_" + uplinkScopeReason(g, ref.PortName)
		} else if suuid == "" {
			reason = "scoped_" + uplinkScopeReason(s, ref.PortName)
		}
		reasons[reason]++
		if samples < 30 {
			t.Logf("difference server_sn=%q tor_sn=%q port_name=%q global_port_uuid=%q scoped_port_uuid=%q reason=%s", ref.ServerSN, ref.ToRSN, ref.PortName, guuid, suuid, reason)
			samples++
		}
	}
	newOnly, oldOnly := 0, 0
	for id := range globalIDs {
		if !scopedIDs[id] {
			newOnly++
		}
	}
	for id := range scopedIDs {
		if !globalIDs[id] {
			oldOnly++
		}
	}
	changedSNs := make([]string, 0, len(changed))
	for sn := range changed {
		changedSNs = append(changedSNs, sn)
	}
	sort.Strings(changedSNs)
	t.Logf("summary servers=%d raw_references=%d invalid_entries=%d unique_tor_sns=%d global_devices_zero=%d global_devices_one=%d global_devices_multiple=%d global_ambiguity_introduced=%d outside_idc_tors=%d outside_idc_unique_references=%d unique_candidates=%d global_resolved=%d scoped_resolved=%d new_only=%d old_only=%d", len(servers), len(references), invalidEntries, len(ordered), zero, unique, multiple, introduced, outside, outsideRefs, len(candidates), len(globalIDs), len(scopedIDs), newOnly, oldOnly)
	t.Logf("changed_tor_sns=%q reasons=%v", changedSNs, reasons)
}

func uplinkScopeReason(target uplinkScopeTarget, name string) string {
	if len(target.Devices) == 0 {
		return "network_sn_unavailable"
	}
	if len(target.Devices) > 1 {
		return "network_sn_ambiguous"
	}
	if name == "" {
		return "empty_port_name"
	}
	if reason := target.Reasons[name]; reason != "" {
		return reason
	}
	return "physical_port_name_unavailable"
}

// idc=0 表示全局；非零值仅用于复现旧查询条件，不修改生产解析器。
func uplinkScopeResolve(ctx context.Context, c *CMDB, cfg cmdbConfig, tor string, idc int64) (uplinkScopeTarget, error) {
	target := uplinkScopeTarget{Ports: map[string]string{}, Reasons: map[string]string{}}
	condition := map[string]any{"device_sn": tor, "parent_type_id": 54}
	if idc != 0 {
		condition["idc_id"] = idc
	}
	devices, err := uplinkScopeReadPages(ctx, c, cfg, deviceViewEndpoint, condition, []string{"inst_id", "device_sn", "parent_type_id", "idc_id"})
	if err != nil {
		return target, err
	}
	for _, device := range devices {
		sn, err := uplinkText(device, "device_sn")
		if err != nil {
			return target, err
		}
		category, err := uplinkScopeInteger(device, "parent_type_id")
		if err != nil {
			return target, err
		}
		instance, err := uplinkScopeInteger(device, "inst_id")
		if err != nil {
			return target, err
		}
		location, err := uplinkScopeInteger(device, "idc_id")
		if err != nil {
			return target, err
		}
		if sn != tor || category != 54 || idc != 0 && location != idc {
			return target, fmt.Errorf("%w: ToR violates requested condition", ErrCMDBRecord)
		}
		target.Devices = append(target.Devices, uplinkScopeDevice{instance, location})
	}
	if len(target.Devices) != 1 {
		return target, nil
	}
	condition = map[string]any{"sn": tor, "port_type": "physics"}
	if idc != 0 {
		condition["idc_id"] = idc
	}
	ports, err := uplinkScopeReadPages(ctx, c, cfg, "/pub/api/v3/find/instance/object/port_view", condition, []string{"inst_id", "port_uuid", "port_name", "port_type", "local_device_sn"})
	if err != nil {
		return target, err
	}
	seenNames, seenUUID, ambiguousUUID := map[string]bool{}, map[string]string{}, map[string]bool{}
	for _, port := range ports {
		values := map[string]string{}
		for _, field := range []string{"port_uuid", "port_name", "port_type", "local_device_sn"} {
			value, err := uplinkText(port, field)
			if err != nil {
				return target, err
			}
			values[field] = value
		}
		name, uuid := values["port_name"], values["port_uuid"]
		if values["port_type"] != "physics" || values["local_device_sn"] != "" && values["local_device_sn"] != tor {
			return target, fmt.Errorf("%w: invalid physical ToR port", ErrCMDBRecord)
		}
		if seenNames[name] {
			target.Ports[name] = ""
			target.Reasons[name] = "physical_port_name_ambiguous"
		} else {
			target.Ports[name] = uuid
		}
		seenNames[name] = true
		if uuid != "" {
			if previous, ok := seenUUID[uuid]; ok && previous != name {
				ambiguousUUID[uuid] = true
			}
			seenUUID[uuid] = name
		}
	}
	for name, uuid := range target.Ports {
		if ambiguousUUID[uuid] {
			target.Ports[name] = ""
			target.Reasons[name] = "physical_port_uuid_ambiguous"
		}
		if name == "" {
			target.Ports[name] = ""
		}
	}
	return target, nil
}

// 只保留请求字段，拒绝总数变化、空页停滞及跨页乱序，避免诊断无限分页。
func uplinkScopeReadPages(ctx context.Context, c *CMDB, cfg cmdbConfig, endpoint string, condition map[string]any, fields []string) ([]map[string]json.RawMessage, error) {
	var items []map[string]json.RawMessage
	total, last := -1, int64(0)
	for start := 0; ; {
		var raw *cmdbEntityData
		var err error
		if endpoint == deviceViewEndpoint {
			deviceCfg := cfg
			deviceCfg.DeviceCondition = condition
			raw, err = c.collectDeviceViewMapPage(ctx, deviceCfg, start, fields)
		} else {
			raw, err = c.collectUplinkMapPage(ctx, cfg, endpoint, condition, start, fields)
		}
		if err != nil {
			return nil, err
		}
		if total == -1 {
			total = *raw.Count
		}
		if total != *raw.Count {
			return nil, fmt.Errorf("%w: diagnostic count changed", ErrCMDBResponse)
		}
		for _, item := range *raw.Info {
			instance, err := uplinkScopeInteger(item, "inst_id")
			if err != nil {
				return nil, err
			}
			if instance <= last {
				return nil, fmt.Errorf("%w: diagnostic inst_id is not strictly increasing", ErrCMDBResponse)
			}
			last = instance
			minimal := map[string]json.RawMessage{}
			for _, field := range fields {
				minimal[field] = item[field]
			}
			items = append(items, minimal)
		}
		start += len(*raw.Info)
		if start == total {
			return items, nil
		}
		if len(*raw.Info) == 0 || start > total {
			return nil, fmt.Errorf("%w: diagnostic paging stalled", ErrCMDBResponse)
		}
	}
}

func uplinkScopeInteger(item map[string]json.RawMessage, field string) (int64, error) {
	raw := strings.TrimSpace(string(item[field]))
	if strings.HasPrefix(raw, "\"") {
		if err := json.Unmarshal(item[field], &raw); err != nil {
			return 0, fmt.Errorf("%w: invalid diagnostic integer %s", ErrCMDBRecord, field)
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid diagnostic integer %s", ErrCMDBRecord, field)
	}
	return value, nil
}
