package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"UnifiedInfraTopology/internal/model"
)

type LinksToCandidate struct {
	Identity     model.RelationIdentity
	FromDeviceSN string
	ToDeviceSN   string
}
type LinksToPage struct {
	Links                           []LinksToCandidate
	NextOffset, Total, Skipped      int
	Done                            bool
	SourceDigest                    string
	FirstInstanceID, LastInstanceID int64
	DeviceSNs                       []string
	Reasons                         map[string]int
}

func (c *CMDB) CollectLinksToPage(ctx context.Context, source model.Source, start int) (LinksToPage, error) {
	cfg, err := c.configFor(source)
	if err != nil {
		return LinksToPage{}, err
	}
	condition, err := c.conditionFor(source, model.EntityInterface)
	if err != nil {
		return LinksToPage{}, err
	}
	idc, err := strconv.ParseInt(fmt.Sprint(condition["idc_id"]), 10, 64)
	if err != nil || idc <= 0 {
		return LinksToPage{}, ErrCMDBConfig
	}
	cfg.DeviceCondition = map[string]any{"idc_id": idc, "parent_type_id": 54}
	// 设备页限制对端缓存生命周期，端口页单独保持配置大小。
	discoveryCfg := cfg
	discoveryCfg.PageSize = min(cfg.PageSize, 200)
	raw, err := c.collectDeviceViewMapPage(ctx, discoveryCfg, start, []string{"inst_id", "device_sn", "parent_type_id"})
	if err != nil {
		return LinksToPage{}, err
	}
	if raw.Count == nil || raw.Info == nil || *raw.Count < 0 || start < 0 || start > *raw.Count || len(*raw.Info) > *raw.Count-start {
		return LinksToPage{}, ErrCMDBResponse
	}
	page := LinksToPage{Total: *raw.Count, NextOffset: start + len(*raw.Info), Reasons: map[string]int{}}
	page.Done = page.NextOffset == page.Total
	if len(*raw.Info) == 0 && !page.Done {
		return LinksToPage{}, ErrCMDBResponse
	}
	seen := map[string]bool{}
	targets := map[string]map[string]bool{}
	digest := sha256.New()
	for _, item := range *raw.Info {
		inst, err := uplinkInteger(item, "inst_id")
		if err != nil || inst <= page.LastInstanceID {
			return LinksToPage{}, ErrCMDBResponse
		}
		sn, err := uplinkText(item, "device_sn")
		if err != nil || sn == "" || seen[sn] {
			return LinksToPage{}, ErrCMDBResponse
		}
		category, err := uplinkInteger(item, "parent_type_id")
		if err != nil || category != 54 {
			return LinksToPage{}, ErrCMDBResponse
		}
		seen[sn] = true
		page.DeviceSNs = append(page.DeviceSNs, sn)
		if page.FirstInstanceID == 0 {
			page.FirstInstanceID = inst
		}
		page.LastInstanceID = inst
		encoded, _ := json.Marshal(item)
		digest.Write(encoded)
		ports, err := c.linksPhysicalRecords(ctx, cfg, sn)
		if err != nil {
			return LinksToPage{}, err
		}
		seenSourceUUID := map[string]bool{}
		for _, port := range ports {
			uuid, err := uplinkText(port, "port_uuid")
			if err != nil || uuid == "" || seenSourceUUID[uuid] {
				return LinksToPage{}, fmt.Errorf("%w: ambiguous source port UUID", ErrCMDBResponse)
			}
			seenSourceUUID[uuid] = true
		}
		for _, port := range ports {
			encoded, _ := json.Marshal(port)
			digest.Write(encoded)
			local, err := uplinkText(port, "port_uuid")
			if err != nil || local == "" {
				return LinksToPage{}, ErrCMDBResponse
			}
			owner, err := uplinkText(port, "local_device_sn")
			if err != nil || (owner != "" && owner != sn) {
				return LinksToPage{}, ErrCMDBResponse
			}
			remoteSN, err := uplinkText(port, "remote_device_sn")
			if err != nil {
				return LinksToPage{}, err
			}
			remotePort, err := uplinkText(port, "remote_interface_uuid")
			if err != nil {
				return LinksToPage{}, err
			}
			remoteType, err := uplinkText(port, "remote_interface_type")
			if err != nil {
				return LinksToPage{}, err
			}
			skip := func(reason string) { page.Skipped++; page.Reasons[reason]++ }
			if remoteSN == "" || remotePort == "" {
				skip("no_remote")
				continue
			}
			if remoteType != "" && remoteType != "physics" {
				skip("nonphysical_remote")
				continue
			}
			if remotePort == local {
				skip("self_loop")
				continue
			}
			target, exists := targets[remoteSN]
			if !exists {
				targetCfg := cfg
				targetCfg.DeviceCondition = map[string]any{"device_sn": remoteSN, "parent_type_id": 54}
				device, err := c.collectDeviceViewMapPage(ctx, targetCfg, 0, []string{"inst_id", "device_sn", "parent_type_id"})
				if err != nil {
					return LinksToPage{}, err
				}
				if device.Count == nil || device.Info == nil {
					return LinksToPage{}, ErrCMDBResponse
				}
				if *device.Count == 1 && len(*device.Info) == 1 {
					targetSN, e1 := uplinkText((*device.Info)[0], "device_sn")
					cat, e2 := uplinkInteger((*device.Info)[0], "parent_type_id")
					if e1 == nil && e2 == nil && targetSN == remoteSN && cat == 54 {
						records, err := c.linksPhysicalRecords(ctx, cfg, remoteSN)
						if err != nil {
							return LinksToPage{}, err
						}
						target = map[string]bool{}
						counts := map[string]int{}
						for _, record := range records {
							uuid, err := uplinkText(record, "port_uuid")
							if err != nil || uuid == "" {
								return LinksToPage{}, ErrCMDBResponse
							}
							counts[uuid]++
						}
						for uuid, count := range counts {
							target[uuid] = count == 1
						}
					}
				}
				targets[remoteSN] = target
			}
			if target == nil {
				skip("nonnetwork_remote")
				continue
			}
			if !target[remotePort] {
				skip("unresolved_port")
				continue
			}
			identity := model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgeNetwork, Kind: model.RelationLinksTo, From: model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityInterface, StableID: local}, To: model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityInterface, StableID: remotePort}}
			normalized, err := model.NormalizeRelation(model.Relation{Identity: identity, CreatedAt: time.Unix(1, 0).UTC(), SyncedAt: time.Unix(1, 0).UTC()})
			if err != nil {
				return LinksToPage{}, err
			}
			candidate := LinksToCandidate{Identity: normalized.Identity, FromDeviceSN: sn, ToDeviceSN: remoteSN}
			if candidate.Identity.From.StableID != local {
				candidate.FromDeviceSN, candidate.ToDeviceSN = remoteSN, sn
			}
			page.Links = append(page.Links, candidate)
		}
	}
	data, _ := json.Marshal(page.Links)
	digest.Write(data)
	page.SourceDigest = hex.EncodeToString(digest.Sum(nil))
	return page, nil
}

func (c *CMDB) linksPhysicalRecords(ctx context.Context, cfg cmdbConfig, sn string) ([]map[string]json.RawMessage, error) {
	fields := []string{"inst_id", "port_uuid", "port_name", "port_type", "local_device_sn", "remote_device_sn", "remote_interface_uuid", "remote_interface_type"}
	var records []map[string]json.RawMessage
	total, last := -1, int64(0)
	for start := 0; ; {
		raw, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/port_view", map[string]any{"sn": sn, "port_type": "physics"}, start, fields)
		if err != nil {
			return nil, err
		}
		if raw.Count == nil || raw.Info == nil || *raw.Count < 0 {
			return nil, ErrCMDBResponse
		}
		if total < 0 {
			total = *raw.Count
		}
		if total != *raw.Count || start > total || len(*raw.Info) > total-start || (len(*raw.Info) == 0 && start < total) {
			return nil, ErrCMDBResponse
		}
		for _, record := range *raw.Info {
			inst, err := uplinkInteger(record, "inst_id")
			if err != nil || inst <= last {
				return nil, fmt.Errorf("%w: port order", ErrCMDBResponse)
			}
			last = inst
			kind, err := uplinkText(record, "port_type")
			if err != nil || kind != "physics" {
				return nil, ErrCMDBResponse
			}
			owner, err := uplinkText(record, "local_device_sn")
			if err != nil || (owner != "" && owner != sn) {
				return nil, ErrCMDBResponse
			}
			records = append(records, record)
		}
		start += len(*raw.Info)
		if start == total {
			return records, nil
		}
	}
}
