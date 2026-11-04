package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

type podTarget struct {
	ID   int64
	UUID string
}
type cmdbPodPass struct {
	collector *CMDB
	cfg       cmdbConfig
	targets   []podTarget
	digest    string
	summary   map[string]int
}

func (p *cmdbPodPass) ScopeDigest() string { return p.digest }
func (p *cmdbPodPass) ScopeSummary() map[string]int {
	m := map[string]int{}
	for k, v := range p.summary {
		m[k] = v
	}
	return m
}
func (p *cmdbPodPass) Page(ctx context.Context, start int) (EntityPage, error) {
	if start < 0 || start > len(p.targets) {
		return EntityPage{}, ErrCMDBConfig
	}
	end := min(start+p.cfg.PageSize, len(p.targets))
	page := EntityPage{Total: len(p.targets), NextOffset: end, Done: end == len(p.targets), Records: []EntityRecord{}}
	for _, target := range p.targets[start:end] {
		selected, err := p.collector.collectEntityPage(ctx, p.cfg, map[string]any{"inst_id": target.ID}, model.EntityPod, 0)
		if err != nil {
			return EntityPage{}, err
		}
		if selected.Total != 1 || len(selected.Records) != 1 || !selected.Done || selected.Records[0].SourceInstanceID != target.ID || selected.Records[0].Identity != target.UUID {
			return EntityPage{}, fmt.Errorf("%w: POD target changed", ErrCMDBResponse)
		}
		page.Records = append(page.Records, selected.Records[0])
	}
	return page, nil
}
func (c *CMDB) openPodPass(ctx context.Context, source model.Source) (EntityPageStream, error) {
	cfg, err := c.configFor(source)
	if err != nil {
		return nil, err
	}
	deviceCondition, err := c.conditionFor(source, model.EntityDevice)
	if err != nil {
		return nil, err
	}
	cabinetCondition, err := c.conditionFor(source, model.EntityCabinet)
	if err != nil {
		return nil, err
	}
	idc, err := strconv.ParseInt(fmt.Sprint(deviceCondition["idc_id"]), 10, 64)
	if err != nil || idc <= 0 {
		return nil, ErrCMDBConfig
	}
	cabinetIDC, err := strconv.ParseInt(fmt.Sprint(cabinetCondition["idc_id"]), 10, 64)
	if err != nil || idc != cabinetIDC {
		return nil, ErrCMDBConfig
	}
	ids := map[int64]bool{}
	uuids := map[string]bool{}
	summary := map[string]int{}
	for _, object := range []string{"device_view", "cabinet"} {
		fields := []string{"inst_id", "pod_uuid", "compute_plane"}
		if object == "cabinet" {
			fields = []string{"inst_id", "pod_ids"}
		}
		total, last := -1, int64(0)
		for start := 0; ; {
			raw, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/"+object, map[string]any{"idc_id": idc}, start, fields)
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
			for _, item := range *raw.Info {
				inst, err := uplinkInteger(item, "inst_id")
				if err != nil || inst <= last {
					return nil, ErrCMDBResponse
				}
				last = inst
				if object == "device_view" {
					uuid, err := uplinkText(item, "pod_uuid")
					if err != nil {
						return nil, err
					}
					if uuid != "" {
						uuids[uuid] = true
					} else {
						summary["empty_uuid_references"]++
					}
					var planes []struct {
						PodID *int64 `json:"pod_id"`
					}
					if value := item["compute_plane"]; len(value) > 0 {
						if err := json.Unmarshal(value, &planes); err != nil {
							return nil, fmt.Errorf("%w: invalid compute_plane", ErrCMDBRecord)
						}
					}
					for _, plane := range planes {
						if plane.PodID != nil && *plane.PodID > 0 {
							ids[*plane.PodID] = true
						} else {
							summary["empty_id_references"]++
						}
					}
				} else {
					var values []*int64
					if value := item["pod_ids"]; len(value) > 0 {
						if err := json.Unmarshal(value, &values); err != nil {
							return nil, fmt.Errorf("%w: invalid cabinet pod_ids", ErrCMDBRecord)
						}
					}
					for _, value := range values {
						if value != nil && *value > 0 {
							ids[*value] = true
						} else {
							summary["empty_id_references"]++
						}
					}
				}
			}
			start += len(*raw.Info)
			if start == total {
				summary[object+"_records"] = total
				break
			}
		}
	}
	podIDs := map[int64]string{}
	podUUIDs := map[string]int64{}
	targets := []podTarget{}
	total, last := -1, int64(0)
	if len(ids)+len(uuids) > 0 {
		for start := 0; ; {
			page, err := c.collectEntityPage(ctx, cfg, map[string]any{}, model.EntityPod, start)
			if err != nil {
				return nil, err
			}
			if total < 0 {
				total = page.Total
			}
			if total < 0 || start > total || len(page.Records) != min(cfg.PageSize, total-start) || page.Total != total || page.NextOffset != start+len(page.Records) || page.NextOffset > total || page.Done != (page.NextOffset == total) {
				return nil, ErrCMDBResponse
			}
			for _, record := range page.Records {
				if record.SourceInstanceID <= last || podIDs[record.SourceInstanceID] != "" || podUUIDs[record.Identity] != 0 {
					return nil, fmt.Errorf("%w: ambiguous POD identity", ErrCMDBResponse)
				}
				last = record.SourceInstanceID
				podIDs[record.SourceInstanceID] = record.Identity
				podUUIDs[record.Identity] = record.SourceInstanceID
				if ids[record.SourceInstanceID] || uuids[record.Identity] {
					targets = append(targets, podTarget{ID: record.SourceInstanceID, UUID: record.Identity})
				}
			}
			start = page.NextOffset
			if page.Done {
				break
			}
		}
	}
	for id := range ids {
		if podIDs[id] == "" {
			summary["missing_id_references"]++
		}
	}
	for uuid := range uuids {
		if podUUIDs[uuid] == 0 {
			summary["missing_uuid_references"]++
		}
	}
	summary["id_references"] = len(ids)
	summary["uuid_references"] = len(uuids)
	summary["resolved_targets"] = len(targets)
	summary["catalog_records"] = max(total, 0)
	idList := []int64{}
	uuidList := []string{}
	for id := range ids {
		idList = append(idList, id)
	}
	for uuid := range uuids {
		uuidList = append(uuidList, uuid)
	}
	sort.Slice(idList, func(i, j int) bool { return idList[i] < idList[j] })
	sort.Strings(uuidList)
	data, _ := json.Marshal([]any{idc, idList, uuidList, targets, summary})
	digest := sha256.Sum256(data)
	return &cmdbPodPass{collector: c, cfg: cfg, targets: targets, digest: hex.EncodeToString(digest[:]), summary: summary}, nil
}
