package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"UnifiedInfraTopology/internal/model"
)

type SpatialCollection struct {
	Relations        []model.Relation
	Objects, Skipped int
	Reasons          map[string]int
	SourceDigest     string
}

type spatialRecord struct {
	ID                    int64
	Identity, CabinetUUID string
	IDC                   *int64
	RoomID                *int64
	PodIDs                []int64
	PodState              string
}

type spatialDirectory struct {
	byID          map[int64]spatialRecord
	byUUID        map[string]spatialRecord
	ambiguousID   map[int64]bool
	ambiguousUUID map[string]bool
}

func newSpatialDirectory(records []spatialRecord) spatialDirectory {
	d := spatialDirectory{map[int64]spatialRecord{}, map[string]spatialRecord{}, map[int64]bool{}, map[string]bool{}}
	for _, r := range records {
		if _, ok := d.byID[r.ID]; ok {
			d.ambiguousID[r.ID] = true
		}
		if _, ok := d.byUUID[r.Identity]; ok {
			d.ambiguousUUID[r.Identity] = true
		}
		d.byID[r.ID], d.byUUID[r.Identity] = r, r
	}
	// 任一身份映射冲突都会使同一记录的另一种引用方式失效。
	for _, r := range records {
		if d.ambiguousID[r.ID] {
			d.ambiguousUUID[r.Identity] = true
		}
	}
	for _, r := range records {
		if d.ambiguousUUID[r.Identity] {
			d.ambiguousID[r.ID] = true
		}
	}
	return d
}

// 空间采集先读完全部目录与引用，只生成关系，不写图或清理旧边。
func (c *CMDB) CollectSpatial(ctx context.Context, source model.Source) (SpatialCollection, error) {
	result := SpatialCollection{Relations: []model.Relation{}, Reasons: map[string]int{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if c == nil || source.ID == "" {
		return result, ErrCMDBConfig
	}
	cfg, err := c.configFor(source)
	if err != nil {
		return result, err
	}
	conditions, idc, err := c.spatialConditions(source)
	if err != nil {
		return result, err
	}
	records := map[model.EntityType][]spatialRecord{}
	for _, entity := range []model.EntityType{model.EntityDevice, model.EntityCabinet, model.EntityDataCenter, model.EntityPod, model.EntityRoom, model.EntityRPP} {
		rows, err := c.collectSpatialRecords(ctx, cfg, entity, conditions[entity], idc)
		if err != nil {
			return result, err
		}
		records[entity] = rows
	}
	result.Objects = len(records[model.EntityDevice]) + len(records[model.EntityCabinet])
	cabinets := newSpatialDirectory(records[model.EntityCabinet])
	centers := newSpatialDirectory(records[model.EntityDataCenter])
	pods := newSpatialDirectory(records[model.EntityPod])
	rooms := newSpatialDirectory(records[model.EntityRoom])
	rpps := newSpatialDirectory(records[model.EntityRPP])
	result.Objects += len(records[model.EntityRoom]) + len(records[model.EntityRPP])
	// 设备 SN 相同且引用不同不能任意选择；相同事实在关系层确定性去重。
	devices := map[string]string{}
	ambiguousDevice := map[string]bool{}
	for _, r := range records[model.EntityDevice] {
		if prev, ok := devices[r.Identity]; ok && prev != r.CabinetUUID {
			ambiguousDevice[r.Identity] = true
		}
		devices[r.Identity] = r.CabinetUUID
	}
	skip := func(reason string) { result.Skipped++; result.Reasons[reason]++ }
	relations := map[string]model.Relation{}
	appendRelation := func(fromType model.EntityType, from string, kind model.RelationKind, toType model.EntityType, to string) error {
		t := time.Unix(1, 0).UTC()
		r, err := model.NormalizeRelation(model.Relation{Identity: model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgeSpatial, Kind: kind, From: model.EntityIdentity{SourceID: source.ID, EntityType: fromType, StableID: from}, To: model.EntityIdentity{SourceID: source.ID, EntityType: toType, StableID: to}}, CreatedAt: t, SyncedAt: t})
		if err != nil {
			return err
		}
		id, err := r.Identity.ID()
		if err != nil {
			return err
		}
		relations[id] = r
		return nil
	}
	for _, r := range records[model.EntityDevice] {
		switch {
		case r.Identity == "":
			skip("missing_device_sn")
		case ambiguousDevice[r.Identity]:
			skip("ambiguous_device")
		case r.CabinetUUID == "":
			skip("empty_cabinet_reference")
		case cabinets.ambiguousUUID[r.CabinetUUID]:
			skip("ambiguous_cabinet")
		case cabinets.byUUID[r.CabinetUUID].Identity == "":
			skip("missing_cabinet")
		default:
			if err := appendRelation(model.EntityDevice, r.Identity, model.RelationLocatedIn, model.EntityCabinet, r.CabinetUUID); err != nil {
				return result, err
			}
		}
	}
	for _, r := range records[model.EntityCabinet] {
		if cabinets.ambiguousID[r.ID] || cabinets.ambiguousUUID[r.Identity] {
			skip("ambiguous_cabinet")
			continue
		}
		switch {
		case r.IDC == nil || *r.IDC <= 0:
			skip("empty_data_center_reference")
		case centers.ambiguousID[*r.IDC]:
			skip("ambiguous_data_center")
		case centers.byID[*r.IDC].Identity == "":
			skip("missing_data_center")
		default:
			if err := appendRelation(model.EntityCabinet, r.Identity, model.RelationLocatedIn, model.EntityDataCenter, centers.byID[*r.IDC].Identity); err != nil {
				return result, err
			}
		}
		if len(r.PodIDs) == 0 {
			skip("empty_pod_references")
		}
		for _, id := range r.PodIDs {
			if pods.ambiguousID[id] {
				skip("ambiguous_pod")
				continue
			}
			target := pods.byID[id]
			if target.Identity == "" {
				skip("missing_pod")
				continue
			}
			if err := appendRelation(model.EntityCabinet, r.Identity, model.RelationTaggedWith, model.EntityPod, target.Identity); err != nil {
				return result, err
			}
		}
	}
	for _, r := range records[model.EntityRoom] {
		switch {
		case rooms.ambiguousID[r.ID] || rooms.ambiguousUUID[r.Identity]:
			skip("ambiguous_room")
		case r.IDC == nil || *r.IDC <= 0:
			skip("empty_data_center_reference")
		case centers.ambiguousID[*r.IDC]:
			skip("ambiguous_data_center")
		case centers.byID[*r.IDC].Identity == "":
			skip("missing_data_center")
		default:
			if err := appendRelation(model.EntityRoom, r.Identity, model.RelationLocatedIn, model.EntityDataCenter, centers.byID[*r.IDC].Identity); err != nil {
				return result, err
			}
		}
	}
	for _, entry := range []struct {
		entity    model.EntityType
		directory spatialDirectory
	}{{model.EntityCabinet, cabinets}, {model.EntityRPP, rpps}} {
		for _, r := range records[entry.entity] {
			switch {
			case entry.directory.ambiguousID[r.ID] || entry.directory.ambiguousUUID[r.Identity]:
				skip("ambiguous_" + string(entry.entity))
			case r.RoomID == nil || *r.RoomID <= 0:
				skip("empty_room_reference")
			case rooms.ambiguousID[*r.RoomID]:
				skip("ambiguous_room")
			case rooms.byID[*r.RoomID].Identity == "":
				skip("missing_room")
			default:
				if err := appendRelation(entry.entity, r.Identity, model.RelationLocatedIn, model.EntityRoom, rooms.byID[*r.RoomID].Identity); err != nil {
					return result, err
				}
			}
		}
	}
	keys := make([]string, 0, len(relations))
	for id := range relations {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		result.Relations = append(result.Relations, relations[id])
	}
	encoded, err := json.Marshal([]any{source.ID, idc, conditions, records, result.Reasons})
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(encoded)
	result.SourceDigest = hex.EncodeToString(sum[:])
	return result, ctx.Err()
}

func (c *CMDB) spatialConditions(source model.Source) (map[model.EntityType]map[string]any, int64, error) {
	conditions := map[model.EntityType]map[string]any{}
	for _, entity := range []model.EntityType{model.EntityDevice, model.EntityCabinet, model.EntityDataCenter, model.EntityPod, model.EntityRoom, model.EntityRPP} {
		condition, err := c.conditionFor(source, entity)
		if err != nil {
			return nil, 0, err
		}
		conditions[entity] = condition
	}
	device := conditions[model.EntityDevice]
	idc, err := strconv.ParseInt(fmt.Sprint(device["idc_id"]), 10, 64)
	if err != nil || idc <= 0 {
		return nil, 0, ErrCMDBConfig
	}
	for key, value := range device {
		if key == "idc_id" {
			continue
		}
		// 保留既有服务器大类筛选，不忽略未知的窄化条件。
		if key != "parent_type_id" || fmt.Sprint(value) != "52" {
			return nil, 0, fmt.Errorf("%w: incompatible spatial device condition", ErrCMDBConfig)
		}
	}
	for _, entry := range []struct {
		entity model.EntityType
		key    string
	}{{model.EntityCabinet, "idc_id"}, {model.EntityDataCenter, "inst_id"}} {
		condition := conditions[entry.entity]
		if len(condition) > 1 {
			return nil, 0, ErrCMDBConfig
		}
		for key, value := range condition {
			if key != entry.key || fmt.Sprint(value) != strconv.FormatInt(idc, 10) {
				return nil, 0, fmt.Errorf("%w: incompatible spatial directory condition", ErrCMDBConfig)
			}
		}
		conditions[entry.entity] = map[string]any{entry.key: idc}
	}
	if len(conditions[model.EntityPod]) != 0 {
		return nil, 0, fmt.Errorf("%w: spatial POD directory must be complete", ErrCMDBConfig)
	}
	for _, entity := range []model.EntityType{model.EntityRoom, model.EntityRPP} {
		key := "idc_id"
		if entity == model.EntityRPP {
			key = "idc"
		}
		for field, value := range conditions[entity] {
			if entity == model.EntityRoom && field == "status" && fmt.Sprint(value) == "1" {
				continue
			}
			if field != key || fmt.Sprint(value) != strconv.FormatInt(idc, 10) {
				return nil, 0, fmt.Errorf("%w: incompatible spatial %s directory condition", ErrCMDBConfig, entity)
			}
		}
		conditions[entity] = map[string]any{key: idc}
		if entity == model.EntityRoom {
			conditions[entity]["status"] = 1
		}
	}
	return conditions, idc, nil
}

func (c *CMDB) collectSpatialRecords(ctx context.Context, cfg cmdbConfig, entity model.EntityType, condition map[string]any, idc int64) ([]spatialRecord, error) {
	endpoint, fields := deviceViewEndpoint, append(append([]string{}, deviceViewFields...), "idc_id")
	switch entity {
	case model.EntityCabinet:
		endpoint, fields = "/pub/api/v3/find/instance/object/cabinet", cabinetFields
	case model.EntityDataCenter:
		endpoint, fields = "/pub/api/v3/find/instance/object/idc", dataCenterFields
	case model.EntityPod:
		endpoint, fields = "/pub/api/v3/find/instance/object/pod", podFields
	case model.EntityRoom:
		endpoint, fields = roomEndpoint, roomFields
	case model.EntityRPP:
		endpoint, fields = "/pub/api/v3/find/instance/object/idc_RPP", rppFields
	}
	rows := []spatialRecord{}
	total, last := -1, int64(0)
	for start := 0; ; {
		page, err := c.collectUplinkMapPage(ctx, cfg, endpoint, condition, start, fields)
		if err != nil {
			return nil, err
		}
		if page.Count == nil || page.Info == nil || *page.Count < 0 {
			return nil, ErrCMDBResponse
		}
		if total < 0 {
			total = *page.Count
		}
		if total != *page.Count || start > total || len(*page.Info) != min(cfg.PageSize, total-start) {
			return nil, fmt.Errorf("%w: spatial pagination changed", ErrCMDBResponse)
		}
		for _, raw := range *page.Info {
			r := spatialRecord{}
			r.ID, err = uplinkInteger(raw, "inst_id")
			if err != nil || r.ID <= 0 {
				return nil, fmt.Errorf("%w: invalid spatial inst_id", ErrCMDBRecord)
			}
			if r.ID < last {
				return nil, fmt.Errorf("%w: spatial ordering changed", ErrCMDBResponse)
			}
			last = r.ID
			if entity == model.EntityDevice {
				deviceIDC, e := uplinkInteger(raw, "idc_id")
				if e != nil || deviceIDC != idc {
					return nil, fmt.Errorf("%w: spatial device outside IDC selection", ErrCMDBResponse)
				}
				r.IDC = &deviceIDC
				if expected, ok := condition["parent_type_id"]; ok {
					category, e := uplinkInteger(raw, "parent_type_id")
					if e != nil || strconv.FormatInt(category, 10) != fmt.Sprint(expected) {
						return nil, fmt.Errorf("%w: spatial device outside classification selection", ErrCMDBResponse)
					}
				}
				r.Identity, err = uplinkText(raw, "device_sn")
				if err != nil {
					return nil, err
				}
				r.CabinetUUID, err = uplinkText(raw, "cabinet_uuid")
				if err != nil {
					return nil, err
				}
			} else {
				record, err := cmdbEntityRecord(raw, entity)
				if err != nil {
					return nil, err
				}
				r.Identity = record.Identity
				if entity == model.EntityRoom || entity == model.EntityRPP {
					if value, ok := record.Properties["idc_id"].(int64); ok {
						r.IDC = &value
						if value > 0 && value != idc {
							return nil, fmt.Errorf("%w: spatial %s outside IDC selection", ErrCMDBResponse, entity)
						}
					}
					if entity == model.EntityRoom {
						status, ok := record.Properties["status"].(int64)
						if !ok || status != 1 {
							return nil, fmt.Errorf("%w: spatial room outside status selection", ErrCMDBResponse)
						}
					} else if value, ok := record.Properties["idc_room"].(int64); ok {
						r.RoomID = &value
					}
				}
				if entity == model.EntityDataCenter && r.ID != idc {
					return nil, fmt.Errorf("%w: spatial IDC outside selection", ErrCMDBResponse)
				}
				if entity == model.EntityCabinet {
					if value, ok := record.Properties["phy_room_id"].(int64); ok {
						r.RoomID = &value
					}
					if value, ok := record.Properties["idc_id"].(int64); ok {
						r.IDC = &value
						if value > 0 && value != idc {
							return nil, fmt.Errorf("%w: spatial cabinet outside selection", ErrCMDBResponse)
						}
					}
					r.PodIDs = record.Properties["pod_ids"].([]int64)
					r.PodState = "missing"
					if value, ok := raw["pod_ids"]; ok {
						r.PodState = "array"
						if string(value) == "null" {
							r.PodState = "null"
						}
					}
				}
			}
			rows = append(rows, r)
		}
		start += len(*page.Info)
		if start == total {
			break
		}
	}
	// 相同数字身份的目录记录按规范内容排序，使摘要不依赖冲突记录顺序。
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ID != rows[j].ID {
			return rows[i].ID < rows[j].ID
		}
		a, _ := json.Marshal(rows[i])
		b, _ := json.Marshal(rows[j])
		return string(a) < string(b)
	})
	return rows, nil
}
