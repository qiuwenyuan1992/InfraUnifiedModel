package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"UnifiedInfraTopology/internal/model"
)

type GPUCard struct {
	Identity   model.EntityIdentity
	ServerSN   string
	Properties map[string]any
}

type GPUUplink struct {
	Identity        model.RelationIdentity
	ServerSN, ToRSN string
	RawToRPort      string
	Properties      map[string]any
}

type GPUUnmatched struct {
	ServerSN, GPUSN, GPUPort, ToRSN, ToRPort, RawToRPort, UUID, Reason string
}

type GPUCollection struct {
	Cards            []GPUCard
	Uplinks          []GPUUplink
	Unmatched        []GPUUnmatched
	Servers          []string
	Objects, Skipped int
	Reasons          map[string]int
	SourceDigest     string
}

var gpuTextFields = map[string]string{
	"parts_number": "parts_number", "manufacturer": "manufacturer", "model": "model", "specification": "specification", "index": "card_index", "slot": "slot", "bmc_slot": "bmc_slot", "bus_address": "bus_address", "connector": "connector", "VRAM": "vram", "tdp": "tdp", "computer_perf": "computer_perf", "gpu_fp": "gpu_fp", "gpu_fp64": "gpu_fp64", "FP16_Tensor_Sparsity": "fp16_tensor_sparsity", "FP4": "fp4", "INT4": "int4", "INT8": "int8", "TF16": "tf16", "TF32": "tf32", "driver_ver": "driver_ver", "firmware_ver": "firmware_ver", "npu_versions": "npu_versions", "is_domestic": "is_domestic",
}
var gpuUplinkFields = []string{"gpu_port", "gpu_port_speed", "gpu_ip", "gpu_slot", "server_port_speed", "bond_name", "tor_port", "tor_port_speed", "tor_role", "source"}

func cmdbGPUCard(item map[string]json.RawMessage, sourceID, server string) (GPUCard, error) {
	partsSN, err := uplinkText(item, "parts_sn")
	if err != nil {
		return GPUCard{}, err
	}
	owner, err := uplinkText(item, "device_sn")
	if err != nil {
		return GPUCard{}, err
	}
	if partsSN == "" || server == "" || owner != server {
		return GPUCard{}, fmt.Errorf("%w: GPU requires parts_sn and exact discovered server SN", ErrCMDBRecord)
	}
	properties := map[string]any{}
	for source, target := range gpuTextFields {
		value, err := uplinkText(item, source)
		if err != nil {
			return GPUCard{}, err
		}
		properties[target] = nil
		if value != "" {
			properties[target] = value
		}
	}
	ports := []string{}
	var rawPorts []*string
	if raw := item["conn_ports"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &rawPorts); err != nil {
			return GPUCard{}, fmt.Errorf("%w: invalid GPU conn_ports", ErrCMDBRecord)
		}
	}
	seen := map[string]bool{}
	for _, port := range rawPorts {
		if port == nil {
			continue
		}
		value := NormalizePortName(*port)
		if value != "" && !seen[value] {
			ports = append(ports, value)
			seen[value] = true
		}
	}
	properties["conn_ports"] = ports
	properties["maintenance_status"] = nil
	if raw := item["maintenance_status"]; len(raw) > 0 && string(raw) != "null" {
		value, err := uplinkInteger(item, "maintenance_status")
		if err != nil {
			return GPUCard{}, err
		}
		properties["maintenance_status"] = strconv.FormatInt(value, 10)
	}
	properties["maintenance_end"] = nil
	date, err := uplinkText(item, "maintenance_end")
	if err != nil {
		return GPUCard{}, err
	}
	if date != "" {
		value, err := time.Parse("2006-01-02", date)
		if err != nil {
			return GPUCard{}, fmt.Errorf("%w: invalid GPU maintenance_end date", ErrCMDBRecord)
		}
		properties["maintenance_end"] = value
	}
	return GPUCard{Identity: model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityGPU, StableID: partsSN}, ServerSN: server, Properties: properties}, nil
}

const gpuDevicePageSize = 1000

type gpuRawCollection struct {
	records []map[string]json.RawMessage
	digest  []byte
}

func (c *CMDB) collectGPUObject(ctx context.Context, cfg cmdbConfig, object string, condition map[string]any) (gpuRawCollection, error) {
	out := gpuRawCollection{records: []map[string]json.RawMessage{}}
	total := -1
	var last int64
	for start := 0; ; {
		// 全字段仅用于摘要；每台设备按 inst_id 顺序完整分页读取。
		page, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/"+object, condition, start, []string{})
		if err != nil {
			return gpuRawCollection{}, err
		}
		if total < 0 {
			total = *page.Count
		}
		if total != *page.Count {
			return gpuRawCollection{}, fmt.Errorf("%w: GPU %s count changed", ErrCMDBResponse, object)
		}
		encoded, _ := json.Marshal([]any{object, condition, start, total, *page.Info})
		out.digest = append(out.digest, encoded...)
		for _, record := range *page.Info {
			inst, err := uplinkInteger(record, "inst_id")
			if err != nil {
				return gpuRawCollection{}, err
			}
			if inst <= last {
				return gpuRawCollection{}, fmt.Errorf("%w: GPU %s inst_id must increase strictly", ErrCMDBResponse, object)
			}
			last = inst
			out.records = append(out.records, record)
		}
		start += len(*page.Info)
		if start == total {
			return out, nil
		}
	}
}

func (c *CMDB) collectGPUByServer(ctx context.Context, cfg cmdbConfig, object string, servers []string) ([]gpuRawCollection, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// 每台设备的页大小独立于发现接口和命令行分页覆盖。
	cfg.PageSize = gpuDevicePageSize
	results := make([]gpuRawCollection, len(servers))
	jobs := make(chan int)
	var workers sync.WaitGroup
	var firstError sync.Once
	var collectErr error
	for worker := 0; worker < min(cfg.Concurrency, len(servers)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				result, err := c.collectGPUObject(ctx, cfg, object, map[string]any{"device_sn": servers[index]})
				if err != nil {
					firstError.Do(func() { collectErr = err; cancel() })
					return
				}
				// 每个索引只有一个写入者；摘要和身份表在全部完成后串行合并。
				results[index] = result
			}
		}()
	}
queue:
	for index := range servers {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break queue
		}
	}
	close(jobs)
	workers.Wait()
	if collectErr != nil {
		return nil, collectErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// CollectGPU 完整采集候选后才返回；原始元数据和解析身份共同参与复读摘要。
func (c *CMDB) CollectGPU(ctx context.Context, source model.Source) (GPUCollection, error) {
	out := GPUCollection{Cards: []GPUCard{}, Uplinks: []GPUUplink{}, Servers: []string{}, Reasons: map[string]int{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if strings.TrimSpace(source.ID) == "" {
		return out, fmt.Errorf("%w: GPU source ID required", ErrCMDBConfig)
	}
	cfg, err := c.configFor(source)
	if err != nil {
		return out, err
	}
	idc, err := strconv.ParseInt(fmt.Sprint(cfg.DeviceCondition["idc_id"]), 10, 64)
	if err != nil || idc <= 0 {
		return out, fmt.Errorf("%w: GPU requires positive device_condition.idc_id", ErrCMDBConfig)
	}
	digest := sha256.New()
	servers, err := c.collectGPUObject(ctx, cfg, "device_view", map[string]any{"idc_id": idc, "parent_type_id": 52, "device_type_id": 58})
	if err != nil {
		return out, err
	}
	digest.Write(servers.digest)
	serverSeen := map[string]bool{}
	cardSeen := map[string]GPUCard{}
	cardCounts := map[string]int{}
	conflictingSNs := map[string]bool{}
	linkSeen := map[string]string{}
	portOwners := map[string]string{}
	skip := func(reason string) { out.Skipped++; out.Reasons[reason]++ }
	for _, server := range servers.records {
		sn, err := uplinkText(server, "device_sn")
		if err != nil {
			return out, err
		}
		parent, err := uplinkInteger(server, "parent_type_id")
		if err != nil {
			return out, err
		}
		category, err := uplinkInteger(server, "device_type_id")
		if err != nil {
			return out, err
		}
		if sn == "" || serverSeen[sn] || parent != 52 || category != 58 {
			return out, fmt.Errorf("%w: invalid or duplicate GPU server classification/SN", ErrCMDBRecord)
		}
		serverSeen[sn] = true
		out.Servers = append(out.Servers, sn)
	}
	rawCardsByServer, err := c.collectGPUByServer(ctx, cfg, "server_gpu", out.Servers)
	if err != nil {
		return out, err
	}
	for index, sn := range out.Servers {
		rawCards := rawCardsByServer[index]
		digest.Write(rawCards.digest)
		out.Objects += len(rawCards.records)
		for _, raw := range rawCards.records {
			partsSN, err := uplinkText(raw, "parts_sn")
			if err != nil {
				return out, err
			}
			if partsSN == "" {
				skip("gpu_missing_parts_sn")
				continue
			}
			card, err := cmdbGPUCard(raw, source.ID, sn)
			if err != nil {
				return out, err
			}
			cardCounts[partsSN]++
			if previous, ok := cardSeen[card.Identity.StableID]; ok {
				a, _ := json.Marshal(previous)
				b, _ := json.Marshal(card)
				if string(a) != string(b) {
					conflictingSNs[partsSN] = true
				}
				continue
			}
			cardSeen[card.Identity.StableID] = card
			out.Cards = append(out.Cards, card)
		}
	}
	// 先完成全部卡归属校验，冲突 SN 的所有记录与上联均排除，不择一发布。
	cards := out.Cards[:0]
	for _, card := range out.Cards {
		if conflictingSNs[card.Identity.StableID] {
			count := cardCounts[card.Identity.StableID]
			out.Skipped += count
			out.Reasons["gpu_conflicting_parts_sn"] += count
			out.Reasons["gpu_conflicting_parts_sn_groups"]++
			continue
		}
		cards = append(cards, card)
	}
	out.Cards = cards
	validCardsByServer := map[string]map[string]GPUCard{}
	for _, card := range out.Cards {
		if validCardsByServer[card.ServerSN] == nil {
			validCardsByServer[card.ServerSN] = map[string]GPUCard{}
		}
		validCardsByServer[card.ServerSN][card.Identity.StableID] = card
	}
	validServers := []string{}
	for _, sn := range out.Servers {
		if len(validCardsByServer[sn]) > 0 {
			validServers = append(validServers, sn)
		}
	}
	rawLinksByServer, err := c.collectGPUByServer(ctx, cfg, "gpu_uplink", validServers)
	if err != nil {
		return out, err
	}
	torPorts := map[string]map[string]string{}
	for index, sn := range validServers {
		validCards := validCardsByServer[sn]
		rawLinks := rawLinksByServer[index]
		digest.Write(rawLinks.digest)
		out.Objects += len(rawLinks.records)
		for _, raw := range rawLinks.records {
			uuid, err := uplinkText(raw, "uuid")
			if err != nil {
				return out, err
			}
			owner, err := uplinkText(raw, "device_sn")
			if err != nil {
				return out, err
			}
			if uuid == "" || owner != sn {
				return out, fmt.Errorf("%w: GPU uplink requires UUID and exact server SN", ErrCMDBRecord)
			}
			comparable := make(map[string]json.RawMessage, len(raw))
			for field, value := range raw {
				if field != "inst_id" {
					comparable[field] = value
				}
			}
			encoded, _ := json.Marshal(comparable)
			signature := string(encoded)
			if previous, ok := linkSeen[uuid]; ok {
				if previous != signature {
					return out, fmt.Errorf("%w: conflicting GPU uplink UUID %s", ErrCMDBRecord, uuid)
				}
				continue
			}
			linkSeen[uuid] = signature
			values := map[string]string{}
			for _, field := range append(append([]string{}, gpuUplinkFields...), "gpu_sn", "gpu_model", "gpu_vram", "tor_sn") {
				value, err := uplinkText(raw, field)
				if err != nil {
					return out, err
				}
				values[field] = value
			}
			rawToRPort := ""
			if value := raw["tor_port"]; len(value) > 0 {
				// 字符串类型已校验；诊断保留规范化前的完整端口名称。
				_ = json.Unmarshal(value, &rawToRPort)
			}
			values["gpu_port"] = NormalizePortName(values["gpu_port"])
			values["tor_port"] = NormalizePortName(values["tor_port"])
			unmatched := func(reason string) {
				out.Unmatched = append(out.Unmatched, GPUUnmatched{ServerSN: sn, GPUSN: values["gpu_sn"], GPUPort: values["gpu_port"], ToRSN: values["tor_sn"], ToRPort: values["tor_port"], RawToRPort: rawToRPort, UUID: uuid, Reason: reason})
			}
			gpuSN := values["gpu_sn"]
			if gpuSN == "" {
				skip("gpu_uplink_missing_gpu_sn")
				unmatched("gpu_uplink_missing_gpu_sn")
				continue
			}
			if conflictingSNs[gpuSN] {
				skip("gpu_uplink_conflicting_gpu_sn")
				unmatched("gpu_uplink_conflicting_gpu_sn")
				continue
			}
			card, ok := validCards[gpuSN]
			if !ok {
				skip("gpu_uplink_unknown_gpu_sn")
				unmatched("gpu_uplink_unknown_gpu_sn")
				continue
			}
			cardText := func(field string) string { value, _ := card.Properties[field].(string); return value }
			conflicts := []string{}
			if ports := card.Properties["conn_ports"].([]string); values["gpu_port"] != "" {
				found := false
				for _, port := range ports {
					if port == values["gpu_port"] {
						found = true
					}
				}
				if !found {
					out.Reasons["gpu_port_conflict"]++
					conflicts = append(conflicts, "gpu_port_conflict")
				}
			}
			if values["gpu_slot"] != "" && cardText("slot") != "" && values["gpu_slot"] != cardText("slot") {
				out.Reasons["gpu_slot_conflict"]++
				conflicts = append(conflicts, "gpu_slot_conflict")
			}
			for _, pair := range [][3]string{{"gpu_model", "model", "gpu_model_difference"}, {"gpu_vram", "vram", "gpu_vram_difference"}} {
				if values[pair[0]] != "" && cardText(pair[1]) != "" && values[pair[0]] != cardText(pair[1]) {
					out.Reasons[pair[2]]++
				}
			}
			if len(conflicts) > 0 {
				out.Skipped++
				unmatched(strings.Join(conflicts, ","))
				continue
			}
			tor, name := values["tor_sn"], values["tor_port"]
			if tor == "" || name == "" {
				skip("tor_reference_missing")
				unmatched("tor_reference_missing")
				continue
			}
			ports, ok := torPorts[tor]
			if !ok {
				ports, err = c.collectUplinkToRPorts(ctx, cfg, tor)
				if err != nil {
					return out, err
				}
				torPorts[tor] = ports
				resolved, _ := json.Marshal([]any{sn, tor, ports})
				digest.Write(resolved)
			}
			port := ports[name]
			if port == "" {
				skip("tor_port_not_unique")
				unmatched("tor_port_not_unique")
				continue
			}
			if previous, ok := portOwners[port]; ok && previous != tor {
				return out, fmt.Errorf("%w: GPU target interface has conflicting ToR owners", ErrCMDBRecord)
			}
			portOwners[port] = tor
			properties := map[string]any{}
			for _, field := range gpuUplinkFields {
				properties[field] = nil
				if values[field] != "" {
					properties[field] = values[field]
				}
			}
			link := GPUUplink{ServerSN: sn, ToRSN: tor, RawToRPort: rawToRPort, Properties: properties, Identity: model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgeNetwork, Kind: model.RelationGPUUplink, SourceRelationID: uuid, From: card.Identity, To: model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityInterface, StableID: port}}}
			resolved, _ := json.Marshal(link)
			digest.Write(resolved)
			out.Uplinks = append(out.Uplinks, link)
		}
	}
	if len(out.Servers) == 0 {
		out.Reasons["empty_gpu_scope"]++
	}
	out.SourceDigest = hex.EncodeToString(digest.Sum(nil))
	return out, nil
}
