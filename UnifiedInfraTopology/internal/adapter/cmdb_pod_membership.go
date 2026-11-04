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

type PODMembershipPage struct {
	Relations         []model.Relation
	DeviceSNs         []string
	NextOffset, Total int
	Done              bool
	SourceDigest      string
	Skipped           int
	Reasons           map[string]int
}

type PODMembershipPass interface {
	Page(context.Context, int) (PODMembershipPage, error)
	ScopeDigest() string
}

type podMembershipDevice struct {
	ID             int64
	SN, UUID, Name string
	PodID          *int64
	Compute        []struct {
		PodID *int64 `json:"pod_id"`
	}
}

type podMembershipTarget struct {
	ID    int64
	UUID  string
	Plane int64
}

type cmdbPODMembershipPass struct {
	source   model.Source
	pageSize int
	devices  []podMembershipDevice
	byID     map[int64]podMembershipTarget
	byUUID   map[string]podMembershipTarget
	digest   string
}

func (p *cmdbPODMembershipPass) ScopeDigest() string { return p.digest }

// 只缓存轻量设备引用和 POD 身份，不采集或写入图节点。
func (c *CMDB) OpenPODMembershipPass(ctx context.Context, source model.Source) (PODMembershipPass, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrCMDBConfig
	}
	cfg, err := c.configFor(source)
	if err != nil {
		return nil, err
	}
	condition, err := c.conditionFor(source, model.EntityDevice)
	if err != nil {
		return nil, err
	}
	idc, err := strconv.ParseInt(fmt.Sprint(condition["idc_id"]), 10, 64)
	if err != nil || idc <= 0 {
		return nil, ErrCMDBConfig
	}
	p := &cmdbPODMembershipPass{source: source, pageSize: cfg.PageSize, devices: []podMembershipDevice{}, byID: map[int64]podMembershipTarget{}, byUUID: map[string]podMembershipTarget{}}
	seen := map[string]bool{}
	total, last := -1, int64(0)
	fields := []string{"inst_id", "device_sn", "pod_uuid", "pod_id", "pod_name", "compute_plane"}
	for start := 0; ; {
		page, err := c.collectUplinkMapPage(ctx, cfg, deviceViewEndpoint, map[string]any{"idc_id": idc}, start, fields)
		if err != nil {
			return nil, err
		}
		if page.Count == nil || page.Info == nil || *page.Count < 0 {
			return nil, ErrCMDBResponse
		}
		if total < 0 {
			total = *page.Count
		}
		if *page.Count != total || start > total || len(*page.Info) != min(cfg.PageSize, total-start) {
			return nil, fmt.Errorf("%w: membership device pagination changed", ErrCMDBResponse)
		}
		for _, raw := range *page.Info {
			device := podMembershipDevice{}
			device.ID, err = uplinkInteger(raw, "inst_id")
			if err != nil || device.ID <= last {
				return nil, fmt.Errorf("%w: membership device ordering", ErrCMDBResponse)
			}
			last = device.ID
			device.SN, err = uplinkText(raw, "device_sn")
			if err != nil {
				return nil, err
			}
			if device.SN != "" && seen[device.SN] {
				return nil, fmt.Errorf("%w: duplicate device SN across pages", ErrCMDBResponse)
			}
			if device.SN != "" {
				seen[device.SN] = true
			}
			device.UUID, err = uplinkText(raw, "pod_uuid")
			if err != nil {
				return nil, err
			}
			device.Name, err = uplinkText(raw, "pod_name")
			if err != nil {
				return nil, err
			}
			if value := raw["pod_id"]; len(value) > 0 {
				if err := json.Unmarshal(value, &device.PodID); err != nil {
					return nil, fmt.Errorf("%w: invalid pod_id", ErrCMDBRecord)
				}
			}
			if value := raw["compute_plane"]; len(value) > 0 {
				if err := json.Unmarshal(value, &device.Compute); err != nil {
					return nil, fmt.Errorf("%w: invalid compute_plane", ErrCMDBRecord)
				}
			}
			p.devices = append(p.devices, device)
		}
		start += len(*page.Info)
		if start == total {
			break
		}
	}
	// 独立目录也包含仅由管理 pod_id 引用的目标，不依赖实体 pass 的 UUID 筛选。
	targets := []podMembershipTarget{}
	total, last = -1, 0
	for start := 0; ; {
		page, err := c.collectEntityPage(ctx, cfg, map[string]any{}, model.EntityPod, start)
		if err != nil {
			return nil, err
		}
		if total < 0 {
			total = page.Total
		}
		if total < 0 || page.Total != total || start > total || len(page.Records) != min(cfg.PageSize, total-start) || page.NextOffset != start+len(page.Records) || page.NextOffset > total || page.Done != (page.NextOffset == total) {
			return nil, fmt.Errorf("%w: membership POD pagination changed", ErrCMDBResponse)
		}
		for _, record := range page.Records {
			if record.SourceInstanceID <= last || record.Identity == "" || p.byUUID[record.Identity].ID != 0 {
				return nil, fmt.Errorf("%w: ambiguous POD identity", ErrCMDBResponse)
			}
			last = record.SourceInstanceID
			plane, _ := record.Properties["plane"].(int64)
			target := podMembershipTarget{ID: record.SourceInstanceID, UUID: record.Identity, Plane: plane}
			p.byID[target.ID], p.byUUID[target.UUID] = target, target
			targets = append(targets, target)
		}
		if page.Done {
			break
		}
		start = page.NextOffset
	}
	encoded, err := json.Marshal([]any{source.ID, idc, p.devices, targets})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	p.digest = hex.EncodeToString(digest[:])
	return p, nil
}

func (c *CMDB) CollectPODMembershipPage(ctx context.Context, source model.Source, start int) (PODMembershipPage, error) {
	pass, err := c.OpenPODMembershipPass(ctx, source)
	if err != nil {
		return PODMembershipPage{}, err
	}
	return pass.Page(ctx, start)
}

func (p *cmdbPODMembershipPass) Page(ctx context.Context, start int) (PODMembershipPage, error) {
	if err := ctx.Err(); err != nil {
		return PODMembershipPage{}, err
	}
	if start < 0 || start > len(p.devices) {
		return PODMembershipPage{}, ErrCMDBConfig
	}
	end := min(start+p.pageSize, len(p.devices))
	page := PODMembershipPage{Relations: []model.Relation{}, DeviceSNs: []string{}, NextOffset: end, Total: len(p.devices), Done: end == len(p.devices), SourceDigest: p.digest, Reasons: map[string]int{}}
	skip := func(reason string) { page.Skipped++; page.Reasons[reason]++ }
	identities := map[string]bool{}
	appendTarget := func(device podMembershipDevice, target podMembershipTarget, compute bool) error {
		if target.ID == 0 {
			skip("missing_pod")
			return nil
		}
		if target.Plane <= 0 {
			skip("missing_plane")
			return nil
		}
		if compute && target.Plane == 1 {
			skip("compute_plane_conflict")
			return nil
		}
		if target.Plane != 1 && target.Plane != 2 {
			page.Reasons["unknown_plane"]++
		}
		snapshot := time.Unix(1, 0).UTC()
		relation, err := model.NormalizeRelation(model.Relation{Identity: model.RelationIdentity{SourceID: p.source.ID, EdgeType: model.EdgeSpatial, Kind: model.RelationMemberOf, From: model.EntityIdentity{SourceID: p.source.ID, EntityType: model.EntityDevice, StableID: device.SN}, To: model.EntityIdentity{SourceID: p.source.ID, EntityType: model.EntityPod, StableID: target.UUID}}, Properties: map[string]any{"plane": target.Plane}, CreatedAt: snapshot, SyncedAt: snapshot})
		if err != nil {
			return err
		}
		id, err := relation.Identity.ID()
		if err != nil {
			return err
		}
		if !identities[id] {
			identities[id] = true
			page.Relations = append(page.Relations, relation)
		}
		return nil
	}
	for _, device := range p.devices[start:end] {
		if err := ctx.Err(); err != nil {
			return PODMembershipPage{}, err
		}
		if device.SN == "" {
			skip("missing_device_sn")
			continue
		}
		page.DeviceSNs = append(page.DeviceSNs, device.SN)
		hasMain := device.UUID != "" || device.PodID != nil && *device.PodID > 0
		if hasMain {
			var target podMembershipTarget
			conflict := false
			if device.UUID != "" {
				target = p.byUUID[device.UUID]
				if device.PodID != nil && *device.PodID > 0 {
					other := p.byID[*device.PodID]
					conflict = target.ID != 0 && other.ID != 0 && target.ID != other.ID
					if target.ID == 0 || other.ID == 0 {
						target = podMembershipTarget{}
					}
				}
			} else {
				target = p.byID[*device.PodID]
			}
			if conflict {
				skip("uuid_id_conflict")
			} else if err := appendTarget(device, target, false); err != nil {
				return PODMembershipPage{}, err
			}
		}
		for _, compute := range device.Compute {
			if compute.PodID == nil || *compute.PodID <= 0 {
				skip("invalid_compute_pod_id")
				continue
			}
			if err := appendTarget(device, p.byID[*compute.PodID], true); err != nil {
				return PODMembershipPage{}, err
			}
		}
		if !hasMain && len(device.Compute) == 0 {
			page.Reasons["no_pod_references"]++
		}
	}
	return page, nil
}
