package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/model"
)

var _ EntityPageCollector = (*CMDB)(nil)

var dataCenterFields = []string{
	"inst_id", "uuid", "code", "cn_name", "address", "location",
	"created_at", "updated_at", "last_time",
}

var cabinetFields = []string{
	"inst_id", "uuid", "code", "dc_colo_rack", "isp_rack_code", "rack",
	"rack_column_code", "rack_row", "idc_id", "phy_building_id", "phy_room_id",
	"idc_module_id", "idc_building_structure_id", "idc_rows_code", "pod_ids",
	"row_switch_id_A", "row_switch_id_B", "created_at", "updated_at", "last_time",
}

var transformerFields = []string{
	"inst_id", "uuid", "code", "idc", "standby_transformer",
	"transformer_brand", "transformer_type", "transformer_capacity", "create_time", "last_time",
}

var interfaceFields = []string{
	"inst_id", "port_uuid", "port_name", "port_type", "port_speed", "port_operation_status",
	"group_uuid", "port_group_uuid", "port_group_name", "group_type", "port_group_type", "group_subtype",
}

type cmdbEntityRequest struct {
	Fields    []string       `json:"fields"`
	Page      deviceViewPage `json:"page"`
	Condition map[string]any `json:"condition"`
}

type cmdbEntityResponse struct {
	Result    *bool           `json:"result"`
	ErrorCode *int            `json:"error_code"`
	Data      *cmdbEntityData `json:"data"`
}

type cmdbEntityData struct {
	Count *int                          `json:"count"`
	Info  *[]map[string]json.RawMessage `json:"info"`
}

func (c *CMDB) CollectEntityPage(ctx context.Context, source model.Source, entity model.EntityType, start int) (EntityPage, error) {
	if start < 0 || entity != model.EntityDataCenter && entity != model.EntityCabinet && entity != model.EntityInterface && entity != model.EntityTransformer {
		return EntityPage{}, fmt.Errorf("%w: entity_type must be data_center, cabinet, interface, or transformer; offset must be nonnegative", ErrCMDBConfig)
	}
	cfg, err := c.configFor(source)
	if err != nil {
		return EntityPage{}, err
	}
	condition, err := c.conditionFor(source, entity)
	if err != nil {
		return EntityPage{}, err
	}
	return c.collectEntityPage(ctx, cfg, condition, entity, start)
}

func (c *CMDB) collectEntityPage(ctx context.Context, cfg cmdbConfig, condition map[string]any, entity model.EntityType, start int) (EntityPage, error) {
	endpoint, fields := "/pub/api/v3/find/instance/object/idc", dataCenterFields
	if entity == model.EntityCabinet {
		endpoint, fields = "/pub/api/v3/find/instance/object/cabinet", cabinetFields
	} else if entity == model.EntityInterface {
		endpoint, fields = "/pub/api/v3/find/instance/object/port_view", interfaceFields
	} else if entity == model.EntityTransformer {
		endpoint, fields = "/pub/api/v3/find/instance/object/idc_transformer", transformerFields
	}
	body, err := json.Marshal(cmdbEntityRequest{Fields: fields, Page: deviceViewPage{Start: start, Limit: cfg.PageSize, Sort: "inst_id"}, Condition: condition})
	if err != nil {
		return EntityPage{}, fmt.Errorf("%w: encode request", ErrCMDBConfig)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return EntityPage{}, fmt.Errorf("%w: construct request", ErrCMDBConfig)
	}
	query := req.URL.Query()
	for name, value := range cfg.Query {
		query.Set(name, value)
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Content-Type", "application/json")
	for name, value := range cfg.Headers {
		req.Header.Set(name, value)
	}
	resp, err := c.clientFor(cfg).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return EntityPage{}, ctx.Err()
		}
		return EntityPage{}, fmt.Errorf("%w: request failed", ErrCMDBHTTP)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return EntityPage{}, fmt.Errorf("%w: unexpected status %d", ErrCMDBHTTP, resp.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxResponseBytes+1))
	if err != nil {
		return EntityPage{}, fmt.Errorf("%w: response read failed", ErrCMDBHTTP)
	}
	if int64(len(encoded)) > cfg.MaxResponseBytes {
		return EntityPage{}, fmt.Errorf("%w: response exceeds configured limit", ErrCMDBResponse)
	}
	var payload cmdbEntityResponse
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return EntityPage{}, fmt.Errorf("%w: invalid json", ErrCMDBResponse)
	}
	if payload.Result == nil || !*payload.Result || payload.ErrorCode == nil || *payload.ErrorCode != 0 {
		return EntityPage{}, fmt.Errorf("%w: upstream business failure", ErrCMDBResponse)
	}
	if payload.Data == nil || payload.Data.Count == nil || payload.Data.Info == nil || *payload.Data.Count < 0 {
		return EntityPage{}, fmt.Errorf("%w: missing data, count, or info", ErrCMDBResponse)
	}
	items, total := *payload.Data.Info, *payload.Data.Count
	if len(items) > cfg.PageSize {
		return EntityPage{}, fmt.Errorf("%w: page exceeds configured limit", ErrCMDBResponse)
	}
	if len(items) == 0 && total > start {
		return EntityPage{}, fmt.Errorf("%w: empty page before count was reached", ErrCMDBResponse)
	}
	records := make([]EntityRecord, 0, len(items))
	var lastInstanceID int64
	for _, item := range items {
		record, err := cmdbEntityRecord(item, entity)
		if err != nil {
			return EntityPage{}, err
		}
		if record.SourceInstanceID <= lastInstanceID {
			return EntityPage{}, fmt.Errorf("%w: entity inst_id is not strictly increasing", ErrCMDBResponse)
		}
		lastInstanceID = record.SourceInstanceID
		records = append(records, record)
	}
	nextOffset := start + len(records)
	return EntityPage{Records: records, NextOffset: nextOffset, Total: total, Done: len(records) < cfg.PageSize || nextOffset >= total}, nil
}

func cmdbEntityRecord(item map[string]json.RawMessage, entity model.EntityType) (EntityRecord, error) {
	if entity == model.EntityInterface {
		return cmdbInterfaceRecord(item)
	}
	if entity == model.EntityTransformer {
		return cmdbTransformerRecord(item)
	}
	properties := make(map[string]any)
	textFields := []string{"uuid", "code", "cn_name", "address", "location"}
	integerFields := []string{"inst_id"}
	if entity == model.EntityCabinet {
		textFields = []string{"uuid", "code", "dc_colo_rack", "isp_rack_code", "rack", "rack_column_code", "rack_row"}
		integerFields = append(integerFields, "idc_id", "phy_building_id", "phy_room_id", "idc_module_id", "idc_building_structure_id", "idc_rows_code", "row_switch_id_A", "row_switch_id_B")
	}
	for _, field := range textFields {
		var value *string
		if raw := item[field]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				return EntityRecord{}, fmt.Errorf("%w: invalid text field %s", ErrCMDBRecord, field)
			}
		}
		properties[field] = nil
		if value != nil && strings.TrimSpace(*value) != "" {
			properties[field] = strings.TrimSpace(*value)
		}
	}
	for _, field := range integerFields {
		var value *int64
		if raw := item[field]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				return EntityRecord{}, fmt.Errorf("%w: invalid integer field %s", ErrCMDBRecord, field)
			}
		}
		target := field
		if field == "row_switch_id_A" || field == "row_switch_id_B" {
			target = strings.ToLower(field)
		}
		properties[target] = nil
		if value != nil {
			properties[target] = *value
		}
	}
	instID, _ := properties["inst_id"].(int64)
	uuid, _ := properties["uuid"].(string)
	if instID <= 0 || uuid == "" {
		return EntityRecord{}, fmt.Errorf("%w: positive inst_id and uuid are required", ErrCMDBRecord)
	}
	if entity == model.EntityCabinet {
		var ids []*int64
		if raw := item["pod_ids"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &ids); err != nil {
				return EntityRecord{}, fmt.Errorf("%w: invalid pod_ids", ErrCMDBRecord)
			}
		}
		seen := make(map[int64]bool)
		podIDs := make([]int64, 0, len(ids))
		for _, id := range ids {
			if id != nil && *id > 0 && !seen[*id] {
				seen[*id] = true
				podIDs = append(podIDs, *id)
			}
		}
		sort.Slice(podIDs, func(i, j int) bool { return podIDs[i] < podIDs[j] })
		properties["pod_ids"] = podIDs
	}
	properties["source_created_at"] = cmdbSourceTime(item["created_at"])
	properties["source_updated_at"] = cmdbSourceTime(item["updated_at"])
	if properties["source_updated_at"] == nil {
		properties["source_updated_at"] = cmdbSourceTime(item["last_time"])
	}
	delete(properties, "uuid")
	return EntityRecord{SourceInstanceID: instID, Identity: uuid, Properties: properties}, nil
}

func cmdbTransformerRecord(item map[string]json.RawMessage) (EntityRecord, error) {
	properties := make(map[string]any)
	var instID int64
	if raw := item["inst_id"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &instID); err != nil {
			return EntityRecord{}, fmt.Errorf("%w: invalid integer field inst_id", ErrCMDBRecord)
		}
	}
	for _, field := range []struct{ source, target string }{
		{"uuid", "uuid"}, {"code", "code"}, {"transformer_brand", "brand"}, {"transformer_type", "model"},
	} {
		var value *string
		if raw := item[field.source]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				return EntityRecord{}, fmt.Errorf("%w: invalid text field %s", ErrCMDBRecord, field.source)
			}
		}
		properties[field.target] = nil
		if value != nil && strings.TrimSpace(*value) != "" {
			properties[field.target] = strings.TrimSpace(*value)
		}
	}
	identity, _ := properties["uuid"].(string)
	if instID <= 0 || identity == "" {
		return EntityRecord{}, fmt.Errorf("%w: positive inst_id and uuid are required", ErrCMDBRecord)
	}
	delete(properties, "uuid")
	properties["inst_id"] = instID
	for _, field := range []struct{ source, target string }{
		{"idc", "idc_id"}, {"standby_transformer", "standby_transformer_id"},
	} {
		properties[field.target] = nil
		var value int64
		if raw := item[field.source]; len(raw) != 0 && json.Unmarshal(raw, &value) == nil && value > 0 {
			properties[field.target] = value
		}
	}
	properties["rated_capacity"] = nil
	var capacity *int64
	if raw := item["transformer_capacity"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &capacity); err != nil {
			return EntityRecord{}, fmt.Errorf("%w: invalid numeric field transformer_capacity", ErrCMDBRecord)
		}
	}
	if capacity != nil {
		properties["rated_capacity"] = *capacity
	}
	properties["source_created_at"] = cmdbSourceTime(item["create_time"])
	properties["source_updated_at"] = cmdbSourceTime(item["last_time"])
	return EntityRecord{SourceInstanceID: instID, Identity: identity, Properties: properties}, nil
}

func cmdbInterfaceRecord(item map[string]json.RawMessage) (EntityRecord, error) {
	properties := make(map[string]any)
	var instID int64
	if raw := item["inst_id"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &instID); err != nil {
			return EntityRecord{}, fmt.Errorf("%w: invalid integer field inst_id", ErrCMDBRecord)
		}
	}
	for _, field := range []string{"port_uuid", "port_name", "port_type", "group_uuid", "port_group_uuid", "port_group_name"} {
		var value *string
		if raw := item[field]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				return EntityRecord{}, fmt.Errorf("%w: invalid text field %s", ErrCMDBRecord, field)
			}
		}
		target := field
		if field == "port_group_name" {
			target = "group_name"
		}
		properties[target] = nil
		if value != nil && strings.TrimSpace(*value) != "" {
			properties[target] = strings.TrimSpace(*value)
		}
	}
	identity, _ := properties["port_uuid"].(string)
	if instID <= 0 || identity == "" {
		return EntityRecord{}, fmt.Errorf("%w: positive inst_id and port_uuid are required", ErrCMDBRecord)
	}
	delete(properties, "port_uuid")
	for _, field := range []string{"port_speed", "port_operation_status"} {
		properties[field] = nil
		raw := item[field]
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		// 图 Schema 使用 STRING；保留 CMDB 数值原文，不换算单位或解释枚举。
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			properties[field] = value
			continue
		}
		var number json.Number
		if err := json.Unmarshal(raw, &number); err != nil {
			return EntityRecord{}, fmt.Errorf("%w: invalid text or numeric field %s", ErrCMDBRecord, field)
		}
		properties[field] = number.String()
	}
	for _, field := range []string{"group_type", "port_group_type", "group_subtype"} {
		var value *int64
		if raw := item[field]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				return EntityRecord{}, fmt.Errorf("%w: invalid integer field %s", ErrCMDBRecord, field)
			}
		}
		properties[field] = nil
		if value != nil && *value != 0 {
			properties[field] = *value
		}
	}
	return EntityRecord{SourceInstanceID: instID, Identity: identity, Properties: properties}, nil
}

func cmdbSourceTime(raw json.RawMessage) any {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "0000-00-00") {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		// CMDB 无时区的时间与来源样例中的 +08:00 时间对应。
		parsed, err := time.ParseInLocation(layout, value, time.FixedZone("CMDB", 8*60*60))
		if err != nil {
			continue
		}
		if parsed.Unix() <= 0 || parsed.Year() == 2000 && parsed.Month() == 1 && parsed.Day() == 1 && parsed.Hour() == 0 && parsed.Minute() == 0 && parsed.Second() == 0 && parsed.Nanosecond() == 0 {
			return nil
		}
		return parsed.UTC().Truncate(time.Microsecond)
	}
	return nil
}
