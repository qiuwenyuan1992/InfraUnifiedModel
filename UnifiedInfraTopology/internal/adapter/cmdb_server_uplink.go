package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"UnifiedInfraTopology/internal/model"
)

type UplinkPage struct {
	Links                           []model.RelationIdentity
	Targets                         map[string]string
	NextOffset, Total               int
	Done                            bool
	Skipped                         int
	FirstInstanceID, LastInstanceID int64
	SourceDigest                    string
	ServerSNs                       []string
}

func (c *CMDB) CollectServerUplinkPage(ctx context.Context, source model.Source, start int) (UplinkPage, error) {
	if err := ctx.Err(); err != nil {
		return UplinkPage{}, err
	}
	if start < 0 || strings.TrimSpace(source.ID) == "" {
		return UplinkPage{}, fmt.Errorf("%w: nonnegative offset and source ID required", ErrCMDBConfig)
	}
	cfg, err := c.configFor(source)
	if err != nil {
		return UplinkPage{}, err
	}
	idc, err := strconv.ParseInt(fmt.Sprint(cfg.DeviceCondition["idc_id"]), 10, 64)
	if err != nil || idc <= 0 {
		return UplinkPage{}, fmt.Errorf("%w: server uplinks require a positive device_condition.idc_id", ErrCMDBConfig)
	}
	// 服务器页限制 ToR 缓存生命周期，避免加载全网端口。
	cfg.PageSize = min(cfg.PageSize, 200)
	cfg.DeviceCondition = map[string]any{"idc_id": idc, "parent_type_id": 52}
	raw, err := c.collectDeviceViewMapPage(ctx, cfg, start, []string{"inst_id", "device_sn", "parent_type_id", "server_tor_ports"})
	if err != nil {
		return UplinkPage{}, err
	}
	items, total := *raw.Info, *raw.Count
	page := UplinkPage{Links: []model.RelationIdentity{}, Targets: map[string]string{}, NextOffset: start + len(items), Total: total, Done: start+len(items) == total}
	encoded, _ := json.Marshal(items)
	digest := sha256.Sum256(encoded)
	page.SourceDigest = hex.EncodeToString(digest[:])
	cache := map[string]map[string]string{}
	seenServers := map[string]bool{}
	for _, item := range items {
		inst, err := uplinkInteger(item, "inst_id")
		if err != nil {
			return UplinkPage{}, err
		}
		category, err := uplinkInteger(item, "parent_type_id")
		if err != nil {
			return UplinkPage{}, err
		}
		sn, err := uplinkText(item, "device_sn")
		if err != nil {
			return UplinkPage{}, err
		}
		if inst <= 0 || inst <= page.LastInstanceID || category != 52 || sn == "" || seenServers[sn] {
			return UplinkPage{}, fmt.Errorf("%w: server requires unique sn, increasing positive inst_id and classification 52", ErrCMDBRecord)
		}
		if page.FirstInstanceID == 0 {
			page.FirstInstanceID = inst
		}
		page.LastInstanceID = inst
		seenServers[sn] = true
		page.ServerSNs = append(page.ServerSNs, sn)
		var entries []json.RawMessage
		if raw := item["server_tor_ports"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &entries); err != nil {
				return UplinkPage{}, fmt.Errorf("%w: invalid server_tor_ports", ErrCMDBRecord)
			}
		}
		for _, entry := range entries {
			var target map[string]json.RawMessage
			if err := json.Unmarshal(entry, &target); err != nil {
				return UplinkPage{}, fmt.Errorf("%w: invalid server_tor_ports entry", ErrCMDBRecord)
			}
			tor, err := uplinkText(target, "sn")
			if err != nil {
				return UplinkPage{}, err
			}
			var names []*string
			if raw := target["ports"]; len(raw) > 0 {
				if err := json.Unmarshal(raw, &names); err != nil {
					return UplinkPage{}, fmt.Errorf("%w: invalid server_tor_ports ports", ErrCMDBRecord)
				}
			}
			if tor == "" || len(names) == 0 {
				page.Skipped++
				continue
			}
			ports, exists := cache[tor]
			if !exists {
				ports, err = c.collectUplinkToRPorts(ctx, cfg, tor)
				if err != nil {
					return UplinkPage{}, err
				}
				cache[tor] = ports
			}
			for _, name := range names {
				value := ""
				if name != nil {
					value = strings.TrimSpace(*name)
				}
				uuid := ports[value]
				if value == "" || uuid == "" {
					page.Skipped++
					continue
				}
				if owner, ok := page.Targets[uuid]; ok && owner != tor {
					return UplinkPage{}, fmt.Errorf("%w: port UUID has conflicting ToR owners", ErrCMDBRecord)
				}
				page.Targets[uuid] = tor
				page.Links = append(page.Links, model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgeNetwork, Kind: model.RelationServerUplink, From: model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: sn}, To: model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityInterface, StableID: uuid}})
			}
		}
	}
	return page, nil
}

func (c *CMDB) collectUplinkToRPorts(ctx context.Context, cfg cmdbConfig, tor string) (map[string]string, error) {
	deviceCfg := cfg
	// SN 已限定设备身份；不额外限制机房，允许解析跨机房 ToR 引用。
	deviceCfg.DeviceCondition = map[string]any{"device_sn": tor, "parent_type_id": 54}
	devices, err := c.collectDeviceViewMapPage(ctx, deviceCfg, 0, []string{"inst_id", "device_sn", "parent_type_id"})
	if err != nil {
		return nil, err
	}
	ports := map[string]string{}
	if *devices.Count == 0 {
		return ports, nil
	}
	// 不通过 ToR SN 猜分类：只接受唯一且确实属于网络设备的记录。
	if *devices.Count != 1 {
		return ports, nil
	}
	device := (*devices.Info)[0]
	sn, err := uplinkText(device, "device_sn")
	if err != nil {
		return nil, err
	}
	category, err := uplinkInteger(device, "parent_type_id")
	if err != nil {
		return nil, err
	}
	inst, err := uplinkInteger(device, "inst_id")
	if err != nil {
		return nil, err
	}
	if sn != tor || category != 54 || inst <= 0 {
		return ports, nil
	}
	condition := map[string]any{"sn": tor, "port_type": "physics"}
	total := -1
	var last int64
	seenNames := map[string]bool{}
	seenUUID := map[string]string{}
	ambiguousUUID := map[string]bool{}
	for start := 0; ; {
		raw, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/port_view", condition, start, []string{"inst_id", "port_uuid", "port_name", "port_type", "local_device_sn"})
		if err != nil {
			return nil, err
		}
		if total < 0 {
			total = *raw.Count
		}
		if total != *raw.Count {
			return nil, fmt.Errorf("%w: ToR port count changed", ErrCMDBResponse)
		}
		for _, item := range *raw.Info {
			inst, err := uplinkInteger(item, "inst_id")
			if err != nil {
				return nil, err
			}
			if inst <= last {
				return nil, fmt.Errorf("%w: port inst_id is not strictly increasing", ErrCMDBResponse)
			}
			last = inst
			name, err := uplinkText(item, "port_name")
			if err != nil {
				return nil, err
			}
			uuid, err := uplinkText(item, "port_uuid")
			if err != nil {
				return nil, err
			}
			kind, err := uplinkText(item, "port_type")
			if err != nil {
				return nil, err
			}
			owner, err := uplinkText(item, "local_device_sn")
			if err != nil {
				return nil, err
			}
			if kind != "physics" || owner != "" && owner != tor {
				return nil, fmt.Errorf("%w: port violates physical ToR condition", ErrCMDBRecord)
			}
			if seenNames[name] {
				ports[name] = ""
			} else {
				ports[name] = uuid
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
	for name, uuid := range ports {
		if name == "" || ambiguousUUID[uuid] {
			ports[name] = ""
		}
	}
	return ports, nil
}

func uplinkText(item map[string]json.RawMessage, field string) (string, error) {
	var value *string
	if raw := item[field]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("%w: invalid text field %s", ErrCMDBRecord, field)
		}
	}
	if value == nil {
		return "", nil
	}
	return strings.TrimSpace(*value), nil
}

func uplinkInteger(item map[string]json.RawMessage, field string) (int64, error) {
	var value int64
	if err := json.Unmarshal(item[field], &value); err != nil {
		return 0, fmt.Errorf("%w: invalid integer field %s", ErrCMDBRecord, field)
	}
	return value, nil
}

func (c *CMDB) collectDeviceViewMapPage(ctx context.Context, cfg cmdbConfig, start int, fields []string) (*cmdbEntityData, error) {
	return c.collectUplinkMapPage(ctx, cfg, deviceViewEndpoint, cfg.DeviceCondition, start, fields)
}

func (c *CMDB) collectUplinkMapPage(ctx context.Context, cfg cmdbConfig, endpoint string, condition map[string]any, start int, fields []string) (*cmdbEntityData, error) {
	body, err := json.Marshal(cmdbEntityRequest{Fields: fields, Page: deviceViewPage{Start: start, Limit: cfg.PageSize, Sort: "inst_id"}, Condition: condition})
	if err != nil {
		return nil, fmt.Errorf("%w: encode request", ErrCMDBConfig)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: construct request", ErrCMDBConfig)
	}
	query := req.URL.Query()
	for k, v := range cfg.Query {
		query.Set(k, v)
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.clientFor(cfg).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: request failed", ErrCMDBHTTP)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: unexpected status %d", ErrCMDBHTTP, resp.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: response read failed", ErrCMDBHTTP)
	}
	if int64(len(encoded)) > cfg.MaxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeds configured limit", ErrCMDBResponse)
	}
	var payload cmdbEntityResponse
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, fmt.Errorf("%w: invalid json", ErrCMDBResponse)
	}
	if payload.Result == nil || !*payload.Result || payload.ErrorCode == nil || *payload.ErrorCode != 0 {
		return nil, fmt.Errorf("%w: upstream business failure", ErrCMDBResponse)
	}
	if payload.Data == nil || payload.Data.Count == nil || payload.Data.Info == nil || *payload.Data.Count < 0 {
		return nil, fmt.Errorf("%w: missing count or info", ErrCMDBResponse)
	}
	total := *payload.Data.Count
	if start > total || len(*payload.Data.Info) != min(cfg.PageSize, total-start) {
		return nil, fmt.Errorf("%w: incomplete or excessive uplink page", ErrCMDBResponse)
	}
	return payload.Data, nil
}
