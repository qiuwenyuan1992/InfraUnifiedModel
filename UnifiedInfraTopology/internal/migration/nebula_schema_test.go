package migration

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type nebulaField struct {
	typeName string
	notNull  bool
}

func TestNebulaSchemaContract(t *testing.T) {
	const schemaPath = "nebula/0001_topology.ngql"

	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("读取 NebulaGraph Schema %q 失败: %v", schemaPath, err)
	}

	statements := splitNebulaStatements(string(raw))
	wantTags := expectedNebulaTags()
	wantEdges := expectedNebulaEdges()
	tags := make(map[string]map[string]nebulaField)
	edges := make(map[string]map[string]nebulaField)
	indexes := make(map[string][]string)
	spaceCount := 0
	useCount := 0

	for _, statement := range statements {
		upper := strings.ToUpper(statement)
		switch {
		case strings.HasPrefix(upper, "CREATE TAG INDEX"):
			tag, fields := parseNebulaTagIndex(t, statement)
			if _, exists := indexes[tag]; exists {
				t.Fatalf("Tag %q 存在重复身份索引", tag)
			}
			indexes[tag] = fields
		case strings.HasPrefix(upper, "CREATE SPACE"):
			spaceCount++
			assertNebulaSpace(t, statement)
		case strings.HasPrefix(upper, "USE "):
			useCount++
			if !regexp.MustCompile(`(?i)^USE\s+unified_infra_topology$`).MatchString(statement) {
				t.Fatalf("USE 语句必须精确选择 unified_infra_topology: %s", statement)
			}
		case strings.HasPrefix(upper, "CREATE TAG"):
			name, fields := parseNebulaDefinition(t, statement, "TAG")
			if _, exists := tags[name]; exists {
				t.Fatalf("Tag %q 被重复定义", name)
			}
			tags[name] = fields
		case strings.HasPrefix(upper, "CREATE EDGE"):
			name, fields := parseNebulaDefinition(t, statement, "EDGE")
			if _, exists := edges[name]; exists {
				t.Fatalf("Edge Type %q 被重复定义", name)
			}
			edges[name] = fields
		default:
			t.Fatalf("Schema 包含不允许或无法识别的语句: %s", statement)
		}
	}

	if spaceCount != 1 {
		t.Fatalf("CREATE SPACE 语句数量错误: got %d, want 1", spaceCount)
	}
	if useCount != 1 {
		t.Fatalf("USE unified_infra_topology 语句数量错误: got %d, want 1", useCount)
	}
	assertNebulaDefinitions(t, "Tag", tags, wantTags)
	assertNebulaDefinitions(t, "Edge Type", edges, wantEdges)
	assertNebulaIdentityIndexes(t, indexes, wantTags)

	forbidden := regexp.MustCompile(`(?i)\b(scope_id|generation|lifecycle|resolution_status)\b`)
	if match := forbidden.FindString(stripNebulaComments(string(raw))); match != "" {
		t.Fatalf("Schema 包含禁止字段 %q", match)
	}
	if len(statements) != 1+1+len(wantTags)+len(wantEdges)+len(wantTags) {
		t.Fatalf("Schema 语句数量错误: got %d, want %d", len(statements), 2+len(wantTags)+len(wantEdges)+len(wantTags))
	}
}

func splitNebulaStatements(raw string) []string {
	var statements []string
	for _, part := range strings.Split(stripNebulaComments(raw), ";") {
		if statement := strings.TrimSpace(part); statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

func stripNebulaComments(raw string) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if comment := strings.Index(line, "--"); comment >= 0 {
			lines[i] = line[:comment]
		}
	}
	return strings.Join(lines, "\n")
}

func assertNebulaSpace(t *testing.T, statement string) {
	t.Helper()
	match := regexp.MustCompile(`(?is)^CREATE\s+SPACE\s+IF\s+NOT\s+EXISTS\s+unified_infra_topology\s*\((.*)\)$`).FindStringSubmatch(statement)
	if match == nil {
		t.Fatalf("CREATE SPACE 必须使用 IF NOT EXISTS 和名称 unified_infra_topology: %s", statement)
	}

	options := make(map[string]string)
	for _, rawOption := range strings.Split(match[1], ",") {
		parts := strings.SplitN(strings.TrimSpace(rawOption), "=", 2)
		if len(parts) != 2 {
			t.Fatalf("无法解析 CREATE SPACE 选项 %q", rawOption)
		}
		options[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.ToUpper(strings.Join(strings.Fields(parts[1]), ""))
	}
	want := map[string]string{
		"partition_num":  "10",
		"replica_factor": "1",
		"vid_type":       "FIXED_STRING(64)",
	}
	if diff := diffStringMap(options, want); diff != "" {
		t.Fatalf("CREATE SPACE 选项不符合开发基线:\n%s", diff)
	}
}

func parseNebulaDefinition(t *testing.T, statement, kind string) (string, map[string]nebulaField) {
	t.Helper()
	pattern := `(?is)^CREATE\s+` + kind + `\s+IF\s+NOT\s+EXISTS\s+([a-z_][a-z0-9_]*)\s*\((.*)\)$`
	match := regexp.MustCompile(pattern).FindStringSubmatch(statement)
	if match == nil {
		t.Fatalf("无法解析 CREATE %s 语句: %s", kind, statement)
	}

	fields := make(map[string]nebulaField)
	fieldPattern := regexp.MustCompile(`(?i)^([a-z_][a-z0-9_]*)\s+(STRING|INT|DOUBLE|BOOL|DATETIME)\s+(NULL|NOT\s+NULL)$`)
	for _, rawField := range strings.Split(match[2], ",") {
		field := strings.TrimSpace(rawField)
		parts := fieldPattern.FindStringSubmatch(field)
		if parts == nil {
			t.Fatalf("%s %q 的字段定义无法解析或未显式声明 NULL/NOT NULL: %s", kind, match[1], field)
		}
		name := strings.ToLower(parts[1])
		if _, exists := fields[name]; exists {
			t.Fatalf("%s %q 存在重复字段 %q", kind, match[1], name)
		}
		fields[name] = nebulaField{
			typeName: strings.ToUpper(parts[2]),
			notNull:  strings.EqualFold(strings.Join(strings.Fields(parts[3]), " "), "NOT NULL"),
		}
	}
	return strings.ToLower(match[1]), fields
}

func parseNebulaTagIndex(t *testing.T, statement string) (string, []string) {
	t.Helper()
	match := regexp.MustCompile(`(?is)^CREATE\s+TAG\s+INDEX\s+IF\s+NOT\s+EXISTS\s+[a-z_][a-z0-9_]*\s+ON\s+([a-z_][a-z0-9_]*)\s*\((.*)\)$`).FindStringSubmatch(statement)
	if match == nil {
		t.Fatalf("无法解析 CREATE TAG INDEX 语句: %s", statement)
	}
	var fields []string
	for _, rawField := range strings.Split(match[2], ",") {
		fields = append(fields, strings.ToLower(strings.Join(strings.Fields(rawField), "")))
	}
	return strings.ToLower(match[1]), fields
}

func assertNebulaDefinitions(t *testing.T, kind string, got, want map[string]map[string]nebulaField) {
	t.Helper()
	if diff := diffNameSet(got, want); diff != "" {
		t.Fatalf("%s 名称不符合精确白名单:\n%s", kind, diff)
	}
	for name, wantFields := range want {
		if diff := diffFieldMap(got[name], wantFields); diff != "" {
			t.Fatalf("%s %q 字段契约不匹配:\n%s", kind, name, diff)
		}
	}
}

func assertNebulaIdentityIndexes(t *testing.T, indexes map[string][]string, tags map[string]map[string]nebulaField) {
	t.Helper()
	if diff := diffNameSet(indexes, tags); diff != "" {
		t.Fatalf("Tag 身份索引不符合精确白名单:\n%s", diff)
	}
	stableFields := map[string]string{
		"device": "device_sn", "interface": "port_uuid", "gpu": "uuid", "pod": "uuid", "cabinet": "uuid",
		"data_center": "uuid", "rpp": "uuid", "ups_group": "uuid", "ups": "uuid", "transformer": "uuid",
	}
	for tag, stableField := range stableFields {
		want := []string{"source_id(64)", stableField + "(192)"}
		got := indexes[tag]
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("Tag %q 身份索引字段错误: got %v, want %v", tag, got, want)
		}
	}
}

func expectedNebulaTags() map[string]map[string]nebulaField {
	required := func(typeName string) nebulaField { return nebulaField{typeName: typeName, notNull: true} }
	optional := func(typeName string) nebulaField { return nebulaField{typeName: typeName} }
	common := func(stableField string) map[string]nebulaField {
		return map[string]nebulaField{
			"source_id":  required("STRING"),
			stableField:  required("STRING"),
			"created_at": required("DATETIME"),
			"synced_at":  required("DATETIME"),
		}
	}
	with := func(fields map[string]nebulaField, extra map[string]string) map[string]nebulaField {
		for name, typeName := range extra {
			fields[name] = optional(typeName)
		}
		return fields
	}

	return map[string]map[string]nebulaField{
		"device": with(common("device_sn"), map[string]string{
			"name": "STRING", "parent_type_id": "INT", "device_type_id": "INT", "role": "STRING", "all_ips": "STRING",
		}),
		"interface": with(common("port_uuid"), map[string]string{
			"port_name": "STRING", "port_type": "STRING", "port_speed": "STRING", "port_operation_status": "STRING",
			"group_uuid": "STRING", "port_group_uuid": "STRING", "group_name": "STRING", "group_type": "INT",
			"port_group_type": "INT", "group_subtype": "INT",
		}),
		"gpu": with(common("uuid"), map[string]string{
			"parts_sn": "STRING", "parts_number": "STRING", "manufacturer": "STRING", "model": "STRING",
			"specification": "STRING", "card_index": "STRING", "slot": "STRING", "bmc_slot": "STRING",
			"bus_address": "STRING", "connector": "STRING", "conn_ports": "STRING", "vram": "STRING", "tdp": "STRING",
			"computer_perf": "STRING", "gpu_fp": "STRING", "gpu_fp64": "STRING", "fp16_tensor_sparsity": "STRING",
			"fp4": "STRING", "int4": "STRING", "int8": "STRING", "tf16": "STRING", "tf32": "STRING",
			"driver_ver": "STRING", "firmware_ver": "STRING", "npu_versions": "STRING", "is_domestic": "STRING",
			"maintenance_status": "STRING", "maintenance_end": "DATETIME",
		}),
		"pod": with(common("uuid"), map[string]string{
			"inst_id": "INT", "basic_code": "STRING", "name": "STRING", "full_name": "STRING", "plane": "INT",
			"mode": "STRING", "rdma": "INT", "phy_building_id": "INT", "idc_logic_id": "INT", "is_delete": "INT",
			"is_sync": "INT", "source_created_at": "DATETIME", "source_updated_at": "DATETIME",
		}),
		"cabinet": with(common("uuid"), map[string]string{
			"inst_id": "INT", "code": "STRING", "dc_colo_rack": "STRING", "isp_rack_code": "STRING", "rack": "STRING",
			"rack_column_code": "STRING", "rack_row": "STRING", "idc_id": "INT", "phy_building_id": "INT",
			"phy_room_id": "INT", "idc_module_id": "INT", "idc_building_structure_id": "INT", "idc_rows_code": "INT",
			"pod_ids": "STRING", "row_switch_id_a": "INT", "row_switch_id_b": "INT", "source_created_at": "DATETIME",
			"source_updated_at": "DATETIME",
		}),
		"data_center": with(common("uuid"), map[string]string{
			"inst_id": "INT", "code": "STRING", "cn_name": "STRING", "address": "STRING", "location": "STRING",
			"source_created_at": "DATETIME", "source_updated_at": "DATETIME",
		}),
		"rpp": with(common("uuid"), map[string]string{
			"inst_id": "INT", "code": "STRING", "idc_id": "INT", "ups_group_id": "INT", "transformer_group_a_id": "INT",
			"source_created_at": "DATETIME", "source_updated_at": "DATETIME",
		}),
		"ups_group": with(common("uuid"), map[string]string{
			"inst_id": "INT", "code": "STRING", "idc_id": "INT", "transformer_id_up": "INT",
			"source_created_at": "DATETIME", "source_updated_at": "DATETIME",
		}),
		"ups": with(common("uuid"), map[string]string{
			"inst_id": "INT", "code": "STRING", "idc_id": "INT", "ups_group_id": "INT", "transformer_id_up": "INT",
			"brand": "STRING", "model": "STRING", "rated_capacity": "INT", "source_created_at": "DATETIME",
			"source_updated_at": "DATETIME",
		}),
		"transformer": with(common("uuid"), map[string]string{
			"inst_id": "INT", "code": "STRING", "idc_id": "INT", "standby_transformer_id": "INT", "brand": "STRING",
			"model": "STRING", "rated_capacity": "INT", "source_created_at": "DATETIME", "source_updated_at": "DATETIME",
		}),
	}
}

func expectedNebulaEdges() map[string]map[string]nebulaField {
	required := func(typeName string) nebulaField { return nebulaField{typeName: typeName, notNull: true} }
	optional := func(typeName string) nebulaField { return nebulaField{typeName: typeName} }
	common := func() map[string]nebulaField {
		return map[string]nebulaField{
			"relation_id":   required("STRING"),
			"relation_kind": required("STRING"),
			"source_id":     required("STRING"),
			"created_at":    required("DATETIME"),
			"synced_at":     required("DATETIME"),
		}
	}
	spatial := common()
	spatial["plane"] = optional("INT")
	composition := common()
	network := common()
	for _, name := range []string{"gpu_port", "gpu_port_speed", "gpu_ip", "gpu_slot", "server_port_speed", "bond_name", "tor_port", "tor_port_speed", "tor_role", "source"} {
		network[name] = optional("STRING")
	}
	power := common()
	power["power_path"] = optional("STRING")
	return map[string]map[string]nebulaField{
		"spatial_relation":     spatial,
		"composition_relation": composition,
		"network_relation":     network,
		"power_relation":       power,
	}
}

func diffNameSet[A, B any](got map[string]A, want map[string]B) string {
	var missing, extra []string
	for name := range want {
		if _, exists := got[name]; !exists {
			missing = append(missing, name)
		}
	}
	for name := range got {
		if _, exists := want[name]; !exists {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	var lines []string
	if len(missing) > 0 {
		lines = append(lines, "缺少: "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		lines = append(lines, "多余: "+strings.Join(extra, ", "))
	}
	return strings.Join(lines, "\n")
}

func diffFieldMap(got, want map[string]nebulaField) string {
	if diff := diffNameSet(got, want); diff != "" {
		return diff
	}
	var mismatches []string
	for name, wantField := range want {
		if gotField := got[name]; gotField != wantField {
			mismatches = append(mismatches, name+": got "+formatNebulaField(gotField)+", want "+formatNebulaField(wantField))
		}
	}
	sort.Strings(mismatches)
	return strings.Join(mismatches, "\n")
}

func diffStringMap(got, want map[string]string) string {
	if diff := diffNameSet(got, want); diff != "" {
		return diff
	}
	var mismatches []string
	for name, wantValue := range want {
		if gotValue := got[name]; gotValue != wantValue {
			mismatches = append(mismatches, name+": got "+gotValue+", want "+wantValue)
		}
	}
	sort.Strings(mismatches)
	return strings.Join(mismatches, "\n")
}

func formatNebulaField(field nebulaField) string {
	if field.notNull {
		return field.typeName + " NOT NULL"
	}
	return field.typeName + " NULL"
}
