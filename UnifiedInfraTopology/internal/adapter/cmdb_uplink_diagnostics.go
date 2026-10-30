package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"UnifiedInfraTopology/internal/model"
)

// UplinkDiagnosticReference 保留重复引用；Reason 为空表示 CMDB 身份已解析。
type UplinkDiagnosticReference struct {
	ServerSN         string   `json:"server_sn"`
	ServerInstanceID int64    `json:"server_instance_id"`
	TorSN            string   `json:"tor_sn"`
	TorInstanceID    int64    `json:"tor_instance_id,omitempty"`
	PortName         string   `json:"port_name"`
	PortNames        []string `json:"entry_port_names,omitempty"`
	PortUUID         string   `json:"port_uuid"`
	Reason           string   `json:"reason,omitempty"`
}

type UplinkDiagnostics struct {
	Servers      int                         `json:"servers"`
	SourceDigest string                      `json:"source_digest"`
	References   []UplinkDiagnosticReference `json:"references"`
}

type diagnosticToR struct {
	instanceID int64
	reason     string
	ports      map[string]string
	reasons    map[string]string
}

// CollectServerUplinkDiagnostics 只读扫描服务器一次，不加载全网端口或修改图数据。
func (c *CMDB) CollectServerUplinkDiagnostics(ctx context.Context, source model.Source) (UplinkDiagnostics, error) {
	result := UplinkDiagnostics{References: []UplinkDiagnosticReference{}}
	if strings.TrimSpace(source.ID) == "" {
		return result, ErrCMDBConfig
	}
	cfg, err := c.configFor(source)
	if err != nil {
		return result, err
	}
	idc, err := strconv.ParseInt(fmt.Sprint(cfg.DeviceCondition["idc_id"]), 10, 64)
	if err != nil || idc <= 0 {
		return result, fmt.Errorf("%w: positive device_condition.idc_id required", ErrCMDBConfig)
	}
	cfg.PageSize = min(cfg.PageSize, 200)
	cfg.DeviceCondition = map[string]any{"idc_id": idc, "parent_type_id": 52}
	cache := map[string]diagnosticToR{}
	seen := map[string]bool{}
	owners := map[string]string{}
	digest := sha256.New()
	total := -1
	var last int64
	for start := 0; ; {
		raw, err := c.collectDeviceViewMapPage(ctx, cfg, start, []string{"inst_id", "device_sn", "parent_type_id", "server_tor_ports"})
		if err != nil {
			return result, err
		}
		if total < 0 {
			total = *raw.Count
		}
		if total != *raw.Count {
			return result, fmt.Errorf("%w: server count changed", ErrCMDBResponse)
		}
		encoded, err := json.Marshal(*raw.Info)
		if err != nil {
			return result, err
		}
		_, _ = digest.Write(encoded)
		for _, item := range *raw.Info {
			inst, err := uplinkInteger(item, "inst_id")
			if err != nil {
				return result, err
			}
			category, err := uplinkInteger(item, "parent_type_id")
			if err != nil {
				return result, err
			}
			sn, err := uplinkText(item, "device_sn")
			if err != nil {
				return result, err
			}
			if inst <= last || sn == "" || category != 52 || seen[sn] {
				return result, fmt.Errorf("%w: server identity/order changed", ErrCMDBRecord)
			}
			last, seen[sn] = inst, true
			result.Servers++
			var entries []json.RawMessage
			if value := item["server_tor_ports"]; len(value) > 0 {
				if err := json.Unmarshal(value, &entries); err != nil {
					return result, fmt.Errorf("%w: invalid server_tor_ports", ErrCMDBRecord)
				}
			}
			for _, entry := range entries {
				var target map[string]json.RawMessage
				if err := json.Unmarshal(entry, &target); err != nil {
					return result, fmt.Errorf("%w: invalid uplink entry", ErrCMDBRecord)
				}
				tor, err := uplinkText(target, "sn")
				if err != nil {
					return result, err
				}
				var names []*string
				if value := target["ports"]; len(value) > 0 {
					if err := json.Unmarshal(value, &names); err != nil {
						return result, fmt.Errorf("%w: invalid uplink ports", ErrCMDBRecord)
					}
				}
				ref := UplinkDiagnosticReference{ServerSN: sn, ServerInstanceID: inst, TorSN: tor}
				// 与生产采集器一致：空 ToR 或无端口的整条 entry 计一次跳过。
				if tor == "" || len(names) == 0 {
					ref.Reason = "cmdb_blank_tor_sn"
					if tor != "" {
						ref.Reason = "cmdb_empty_ports"
					}
					for _, name := range names {
						if name == nil {
							ref.PortNames = append(ref.PortNames, "")
						} else {
							ref.PortNames = append(ref.PortNames, strings.TrimSpace(*name))
						}
					}
					result.References = append(result.References, ref)
					continue
				}
				resolved, exists := cache[tor]
				if !exists {
					resolved, err = c.collectDiagnosticToR(ctx, cfg, tor)
					if err != nil {
						return result, err
					}
					cache[tor] = resolved
				}
				for _, name := range names {
					row := ref
					if name != nil {
						row.PortName = strings.TrimSpace(*name)
					}
					row.TorInstanceID = resolved.instanceID
					row.PortUUID = resolved.ports[row.PortName]
					switch {
					case row.PortName == "":
						row.Reason = "cmdb_blank_port_name"
					case resolved.reason != "":
						row.Reason = resolved.reason
					case resolved.reasons[row.PortName] != "":
						row.Reason = resolved.reasons[row.PortName]
					case row.PortUUID == "":
						row.Reason = "cmdb_port_name_missing"
					}
					if row.Reason == "" {
						if owner, ok := owners[row.PortUUID]; ok && owner != tor {
							return result, fmt.Errorf("%w: conflicting port UUID owners", ErrCMDBRecord)
						}
						owners[row.PortUUID] = tor
					}
					result.References = append(result.References, row)
				}
			}
		}
		start += len(*raw.Info)
		if start == total {
			break
		}
	}
	result.SourceDigest = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

func (c *CMDB) collectDiagnosticToR(ctx context.Context, cfg cmdbConfig, tor string) (diagnosticToR, error) {
	result := diagnosticToR{ports: map[string]string{}, reasons: map[string]string{}}
	deviceCfg := cfg
	deviceCfg.DeviceCondition = map[string]any{"device_sn": tor, "parent_type_id": 54}
	devices, err := c.collectDeviceViewMapPage(ctx, deviceCfg, 0, []string{"inst_id", "device_sn", "parent_type_id"})
	if err != nil {
		return result, err
	}
	if *devices.Count == 0 {
		result.reason = "cmdb_network_tor_not_found"
		return result, nil
	}
	if *devices.Count != 1 {
		result.reason = "cmdb_multiple_network_tors"
		return result, nil
	}
	device := (*devices.Info)[0]
	sn, err := uplinkText(device, "device_sn")
	if err != nil {
		return result, err
	}
	category, err := uplinkInteger(device, "parent_type_id")
	if err != nil {
		return result, err
	}
	inst, err := uplinkInteger(device, "inst_id")
	if err != nil {
		return result, err
	}
	if sn != tor || category != 54 || inst <= 0 {
		result.reason = "cmdb_tor_identity_mismatch"
		return result, nil
	}
	result.instanceID = inst
	seenNames := map[string]bool{}
	seenUUID := map[string]string{}
	ambiguousUUID := map[string]bool{}
	total := -1
	var last int64
	for start := 0; ; {
		raw, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/port_view", map[string]any{"sn": tor, "port_type": "physics"}, start, []string{"inst_id", "port_uuid", "port_name", "port_type", "local_device_sn"})
		if err != nil {
			return result, err
		}
		if total < 0 {
			total = *raw.Count
		}
		if total != *raw.Count {
			return result, fmt.Errorf("%w: ToR port count changed", ErrCMDBResponse)
		}
		for _, item := range *raw.Info {
			inst, err := uplinkInteger(item, "inst_id")
			if err != nil {
				return result, err
			}
			if inst <= last {
				return result, fmt.Errorf("%w: port inst_id not increasing", ErrCMDBResponse)
			}
			last = inst
			name, err := uplinkText(item, "port_name")
			if err != nil {
				return result, err
			}
			uuid, err := uplinkText(item, "port_uuid")
			if err != nil {
				return result, err
			}
			kind, err := uplinkText(item, "port_type")
			if err != nil {
				return result, err
			}
			owner, err := uplinkText(item, "local_device_sn")
			if err != nil {
				return result, err
			}
			if kind != "physics" || owner != "" && owner != tor {
				return result, fmt.Errorf("%w: port violates physical ToR condition", ErrCMDBRecord)
			}
			if seenNames[name] {
				result.ports[name] = ""
				result.reasons[name] = "cmdb_ambiguous_port_name"
			} else {
				result.ports[name] = uuid
				if uuid == "" {
					result.reasons[name] = "cmdb_blank_port_uuid"
				}
			}
			seenNames[name] = true
			if uuid != "" {
				if previous, ok := seenUUID[uuid]; ok && previous != name {
					ambiguousUUID[uuid] = true
				}
				seenUUID[uuid] = name
			}
		}
		start += len(*raw.Info)
		if start == total {
			break
		}
	}
	for name, uuid := range result.ports {
		if name == "" {
			result.ports[name] = ""
		}
		if ambiguousUUID[uuid] {
			result.ports[name] = ""
			result.reasons[name] = "cmdb_ambiguous_port_uuid"
		}
	}
	return result, nil
}
