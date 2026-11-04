package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type gpuAuditTransport struct {
	pass      int
	responses [2]map[string]json.RawMessage
}

func TestLiveGPUUplinkAnomalyReportReadOnly(t *testing.T) {
	path := os.Getenv("GPU_ANOMALY_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读采集GPU上联异常，报告只写本地")
	}
	dir := os.Getenv("GPU_ANOMALY_REPORT_DIR")
	require.NotEmpty(t, dir)
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc238.page_size", 200)
	audit := &gpuAuditTransport{responses: [2]map[string]json.RawMessage{{}, {}}}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	result, err := NewCMDBWithClient(conf, &http.Client{Transport: audit}).CollectGPU(ctx, model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"})
	require.NoError(t, err)
	type portRecord struct {
		InstID int64  `json:"inst_id"`
		Name   string `json:"port_name"`
		UUID   string `json:"port_uuid"`
		Owner  string `json:"local_device_sn"`
	}
	type anomaly struct {
		UUID          string       `json:"uplink_uuid"`
		InstID        int64        `json:"uplink_inst_id"`
		Server        string       `json:"server_sn"`
		GPU           string       `json:"gpu_sn"`
		GPUPort       string       `json:"gpu_port"`
		ToR           string       `json:"tor_sn"`
		Port          string       `json:"tor_port"`
		Reason        string       `json:"reason"`
		DeviceCount   int          `json:"network_device_match_count"`
		PhysicalCount int          `json:"physical_port_count"`
		Matching      []portRecord `json:"exact_name_matches"`
		Available     []string     `json:"available_physical_port_names"`
	}
	ports := map[string][]portRecord{}
	deviceCounts := map[string]int{}
	links := map[string]map[string]json.RawMessage{}
	for key, data := range audit.responses[0] {
		split := strings.Index(key, "{")
		require.Positive(t, split)
		object := strings.TrimPrefix(key[:split], "/pub/api/v3/find/instance/object/")
		var req struct{ Condition map[string]json.RawMessage }
		require.NoError(t, json.Unmarshal([]byte(key[split:]), &req))
		var page struct {
			Count int
			Info  []map[string]json.RawMessage
		}
		require.NoError(t, json.Unmarshal(data, &page))
		text := func(row map[string]json.RawMessage, k string) string {
			v, e := uplinkText(row, k)
			require.NoError(t, e)
			return v
		}
		switch object {
		case "device_view":
			tor := text(req.Condition, "device_sn")
			if tor != "" {
				deviceCounts[tor] = page.Count
			}
		case "port_view":
			tor := text(req.Condition, "sn")
			for _, row := range page.Info {
				inst, e := uplinkInteger(row, "inst_id")
				require.NoError(t, e)
				ports[tor] = append(ports[tor], portRecord{inst, text(row, "port_name"), text(row, "port_uuid"), text(row, "local_device_sn")})
			}
		case "gpu_uplink":
			for _, row := range page.Info {
				links[text(row, "uuid")] = row
			}
		}
	}
	accepted := map[string]bool{}
	for _, link := range result.Uplinks {
		accepted[link.Identity.SourceRelationID] = true
	}
	rows := []anomaly{}
	reasons := map[string]int{}
	torTotals := map[string]int{}
	for uuid, raw := range links {
		if accepted[uuid] {
			continue
		}
		text := func(k string) string { v, e := uplinkText(raw, k); require.NoError(t, e); return v }
		inst, e := uplinkInteger(raw, "inst_id")
		require.NoError(t, e)
		row := anomaly{UUID: uuid, InstID: inst, Server: text("device_sn"), GPU: text("gpu_sn"), GPUPort: text("gpu_port"), ToR: text("tor_sn"), Port: text("tor_port"), Matching: []portRecord{}, Available: []string{}}
		row.DeviceCount = deviceCounts[row.ToR]
		row.PhysicalCount = len(ports[row.ToR])
		names := map[string]bool{}
		for _, port := range ports[row.ToR] {
			names[port.Name] = true
			if port.Name == row.Port {
				row.Matching = append(row.Matching, port)
			}
		}
		for name := range names {
			row.Available = append(row.Available, name)
		}
		sort.Strings(row.Available)
		sort.Slice(row.Matching, func(i, j int) bool { return row.Matching[i].InstID < row.Matching[j].InstID })
		switch {
		case row.ToR == "" || row.Port == "":
			row.Reason = "上联缺少ToR SN或端口名"
		case row.DeviceCount == 0:
			row.Reason = "按ToR SN和网络设备分类查询不到设备"
		case row.DeviceCount != 1:
			row.Reason = "ToR网络设备记录不唯一"
		case row.PhysicalCount == 0:
			row.Reason = "ToR设备存在但未返回物理端口"
		case len(row.Matching) == 0:
			row.Reason = "物理端口列表没有完全同名端口"
		case len(row.Matching) > 1:
			row.Reason = "同名物理端口记录重复"
		case row.Matching[0].UUID == "":
			row.Reason = "同名物理端口UUID为空"
		default:
			row.Reason = "端口UUID对应多个端口名"
			found := false
			for _, port := range ports[row.ToR] {
				if port.UUID == row.Matching[0].UUID && port.Name != row.Port {
					found = true
				}
			}
			require.True(t, found, "未识别的异常不能归入UUID冲突: %+v", row)
		}
		reasons[row.Reason]++
		torTotals[row.ToR]++
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.ToR != b.ToR {
			return a.ToR < b.ToR
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if a.GPU != b.GPU {
			return a.GPU < b.GPU
		}
		return a.UUID < b.UUID
	})
	require.Equal(t, result.Reasons["tor_port_not_unique"], len(rows), "报告须与实际跳过数完全对应")
	report := map[string]any{"collected_at": time.Now().UTC(), "source_id": "00000000000000000000000000000238", "read_only": true, "anomaly_count": len(rows), "reason_counts": reasons, "tor_counts": torTotals, "accepted_uplinks": len(result.Uplinks), "records": rows}
	data, e := json.MarshalIndent(report, "", "  ")
	require.NoError(t, e)
	base := dir + "/gpu-uplink-anomalies-2026-11-02"
	require.NoError(t, os.WriteFile(base+".json", append(data, 10), 0600))
	var csvbuf bytes.Buffer
	writer := csv.NewWriter(&csvbuf)
	require.NoError(t, writer.Write([]string{"上联inst_id", "上联UUID", "服务器SN", "GPU SN", "GPU端口", "ToR SN", "ToR端口", "异常原因", "设备匹配数", "物理端口数", "同名端口记录", "可用物理端口名"}))
	var md strings.Builder
	fmt.Fprintf(&md, "# GPU上联异常清单\n\n只读采集；异常共%d条，成功解析%d条。没有修改CMDB或图库。\n\n## 原因汇总\n\n", len(rows), len(result.Uplinks))
	keys := []string{}
	for reason := range reasons {
		keys = append(keys, reason)
	}
	sort.Strings(keys)
	for _, reason := range keys {
		fmt.Fprintf(&md, "- %s：%d条\n", reason, reasons[reason])
	}
	md.WriteString("\n## 逐条明细\n\n|上联ID|服务器SN|GPU SN|GPU端口|ToR SN|ToR端口|原因|设备数|物理端口数|\n|---|---|---|---|---|---|---|---|---|\n")
	escape := func(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s) }
	for _, row := range rows {
		matching, e := json.Marshal(row.Matching)
		require.NoError(t, e)
		require.NoError(t, writer.Write([]string{fmt.Sprint(row.InstID), row.UUID, row.Server, row.GPU, row.GPUPort, row.ToR, row.Port, row.Reason, fmt.Sprint(row.DeviceCount), fmt.Sprint(row.PhysicalCount), string(matching), strings.Join(row.Available, "; ")}))
		fmt.Fprintf(&md, "|%d|%s|%s|%s|%s|%s|%s|%d|%d|\n", row.InstID, escape(row.Server), escape(row.GPU), escape(row.GPUPort), escape(row.ToR), escape(row.Port), escape(row.Reason), row.DeviceCount, row.PhysicalCount)
	}
	writer.Flush()
	require.NoError(t, writer.Error())
	require.NoError(t, os.WriteFile(base+".csv", csvbuf.Bytes(), 0600))
	require.NoError(t, os.WriteFile(base+".md", []byte(md.String()), 0600))
	t.Logf("anomalies=%d reasons=%v tor_counts=%v reports=%s.{json,csv,md}", len(rows), reasons, torTotals, base)
}

func (a *gpuAuditTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	response, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	var envelope struct{ Data json.RawMessage }
	if err = json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	a.responses[a.pass][r.URL.Path+string(body)] = envelope.Data
	return response, nil
}

func TestLiveGPUIndependentSourceReadOnly(t *testing.T) {
	path := os.Getenv("GPU_DOUBLE_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读两遍核对GPU来源，不写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc238.page_size", 200)
	audit := &gpuAuditTransport{responses: [2]map[string]json.RawMessage{{}, {}}}
	c := NewCMDBWithClient(conf, &http.Client{Transport: audit})
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	first, err := c.CollectGPU(ctx, model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"})
	require.NoError(t, err)
	audit.pass = 1
	second, err := c.CollectGPU(ctx, model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"})
	require.NoError(t, err)
	t.Logf("first cards=%d uplinks=%d second cards=%d uplinks=%d digest_equal=%v", len(first.Cards), len(first.Uplinks), len(second.Cards), len(second.Uplinks), first.SourceDigest == second.SourceDigest)
	changed := 0
	for key, raw := range audit.responses[0] {
		var a, b struct {
			Count int
			Info  []map[string]json.RawMessage
		}
		require.NoError(t, json.Unmarshal(raw, &a))
		require.NoError(t, json.Unmarshal(audit.responses[1][key], &b))
		if reflect.DeepEqual(a, b) {
			continue
		}
		changed++
		if a.Count != b.Count || len(a.Info) != len(b.Info) {
			t.Logf("changed count first=%d second=%d", a.Count, b.Count)
			continue
		}
		for i, row := range a.Info {
			for field, value := range row {
				if !bytes.Equal(value, b.Info[i][field]) {
					t.Logf("changed inst_id=%s field=%s", row["inst_id"], field)
				}
			}
		}
	}
	t.Logf("changed source pages=%d", changed)
	require.Equal(t, first.SourceDigest, second.SourceDigest, "来源变化不可宣称完整快照")
}

func TestLiveIDC238GPUCollectionReadOnly(t *testing.T) {
	path := os.Getenv("GPU_COLLECTION_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读诊断GPU来源与解析")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc238.page_size", 200)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	result, err := NewCMDB(conf).CollectGPU(ctx, model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"})
	t.Logf("servers=%d cards=%d uplinks=%d reasons=%v error=%v", len(result.Servers), len(result.Cards), len(result.Uplinks), result.Reasons, err)
	if err != nil && len(result.Servers) > 0 {
		c := NewCMDB(conf)
		cfg, e := c.configFor(model.Source{ConfigRef: "idc238"})
		require.NoError(t, e)
		raw, e := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/server_gpu", map[string]any{"device_sn": result.Servers[0]}, 0, []string{})
		require.NoError(t, e)
		for i, item := range *raw.Info {
			if i == 3 {
				break
			}
			t.Logf("queried_server=%s gpu_uuid=%s device_sn=%s parts_sn=%s inst_id=%s", result.Servers[0], item["uuid"], item["device_sn"], item["parts_sn"], item["inst_id"])
		}
	}
	require.NoError(t, err)
}
