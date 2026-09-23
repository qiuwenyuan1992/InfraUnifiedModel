package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"UnifiedInfraTopology/internal/model"
)

const deviceViewEndpoint = "/pub/api/v3/find/instance/object/device_view"

var (
	ErrCMDBHTTP     = errors.New("cmdb http error")
	ErrCMDBResponse = errors.New("cmdb response error")
	ErrCMDBRecord   = errors.New("cmdb device record error")
)

type deviceViewRequest struct {
	Fields    []string       `json:"fields"`
	Page      deviceViewPage `json:"page"`
	Condition map[string]any `json:"condition,omitempty"`
}

var deviceViewFields = []string{
	"inst_id", "device_id", "device_sn", "device_name", "host_name",
	"parent_type_id", "device_type_id", "role", "cabinet_uuid", "pod_uuid",
	"compute_plane", "device_ip_info", "eth_ip", "ilo_ip", "data_ip",
	"console_ip", "management_ip",
}

type deviceViewPage struct {
	Start int    `json:"start"`
	Limit int    `json:"limit"`
	Sort  string `json:"sort"`
}

type deviceViewResponse struct {
	Result    *bool           `json:"result"`
	ErrorCode *int            `json:"error_code"`
	Data      *deviceViewData `json:"data"`
}

type deviceViewData struct {
	Count *int             `json:"count"`
	Info  *[]deviceViewDTO `json:"info"`
}

type deviceViewDTO struct {
	InstID       int64                   `json:"inst_id"`
	DeviceID     int64                   `json:"device_id"`
	DeviceSN     string                  `json:"device_sn"`
	DeviceName   string                  `json:"device_name"`
	HostName     string                  `json:"host_name"`
	ParentTypeID int64                   `json:"parent_type_id"`
	DeviceTypeID int64                   `json:"device_type_id"`
	Role         string                  `json:"role"`
	CabinetUUID  string                  `json:"cabinet_uuid"`
	PodUUID      string                  `json:"pod_uuid"`
	ComputePlane []deviceComputePlaneDTO `json:"compute_plane"`
	DeviceIPInfo deviceIPInfoDTO         `json:"device_ip_info"`
	EthIP        []string                `json:"eth_ip"`
	ILOIP        []string                `json:"ilo_ip"`
	DataIP       []string                `json:"data_ip"`
	ConsoleIP    []string                `json:"console_ip"`
	ManagementIP []string                `json:"management_ip"`
}

type deviceComputePlaneDTO struct {
	PodID int64 `json:"pod_id"`
}

type deviceIPInfoDTO struct {
	Eth        []deviceIPDTO `json:"eth"`
	ILO        []deviceIPDTO `json:"ilo"`
	Data       []deviceIPDTO `json:"data"`
	Console    []deviceIPDTO `json:"console"`
	Management []deviceIPDTO `json:"management"`
}

type deviceIPDTO struct {
	IP string `json:"ip"`
}

func (c *CMDB) CollectDevices(ctx context.Context, source model.Source, visit func(DeviceRecord) error) error {
	if visit == nil {
		return fmt.Errorf("%w: device visitor is required", ErrCMDBConfig)
	}
	cfg, err := c.configFor(source)
	if err != nil {
		return err
	}

	var lastInstanceID int64
	for start := 0; ; {
		page, err := c.collectDeviceRecordsPage(ctx, cfg, start)
		if err != nil {
			return err
		}
		for _, record := range page.Records {
			if record.SourceInstanceID <= lastInstanceID {
				return fmt.Errorf("%w: device inst_id is not strictly increasing", ErrCMDBResponse)
			}
			lastInstanceID = record.SourceInstanceID
			if err := visit(record); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		if len(page.Records) == 0 {
			return nil
		}
		start = page.NextOffset
		if len(page.Records) < cfg.PageSize {
			return nil
		}
	}
}

func (c *CMDB) CollectDevicePage(ctx context.Context, source model.Source, start int) (DevicePage, error) {
	cfg, err := c.configFor(source)
	if err != nil {
		return DevicePage{}, err
	}
	return c.collectDeviceRecordsPage(ctx, cfg, start)
}

func (c *CMDB) collectDeviceRecordsPage(ctx context.Context, cfg cmdbConfig, start int) (DevicePage, error) {
	page, err := c.collectDevicePage(ctx, cfg, start)
	if err != nil {
		return DevicePage{}, err
	}
	items := *page.Info
	if len(items) > cfg.PageSize {
		return DevicePage{}, fmt.Errorf("%w: page exceeds configured limit", ErrCMDBResponse)
	}

	records := make([]DeviceRecord, 0, len(items))
	var lastInstanceID int64
	for _, item := range items {
		record, err := item.toRecord()
		if err != nil {
			return DevicePage{}, err
		}
		if record.SourceInstanceID <= lastInstanceID {
			return DevicePage{}, fmt.Errorf("%w: device inst_id is not strictly increasing", ErrCMDBResponse)
		}
		lastInstanceID = record.SourceInstanceID
		records = append(records, record)
	}

	total := *page.Count
	if len(records) == 0 && total > start {
		return DevicePage{}, fmt.Errorf("%w: empty page before count was reached", ErrCMDBResponse)
	}
	nextOffset := start + len(records)
	return DevicePage{
		Records:    records,
		NextOffset: nextOffset,
		Total:      total,
		Done:       len(records) < cfg.PageSize || nextOffset >= total,
	}, nil
}

func (c *CMDB) collectDevicePage(ctx context.Context, cfg cmdbConfig, start int) (*deviceViewData, error) {
	body, err := json.Marshal(deviceViewRequest{
		Fields:    deviceViewFields,
		Page:      deviceViewPage{Start: start, Limit: cfg.PageSize, Sort: "inst_id"},
		Condition: cfg.DeviceCondition,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode request", ErrCMDBResponse)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+deviceViewEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: construct request", ErrCMDBConfig)
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
		return nil, fmt.Errorf("%w: request failed", ErrCMDBHTTP)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: unexpected status %d", ErrCMDBHTTP, resp.StatusCode)
	}

	encoded, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: response read failed", ErrCMDBHTTP)
	}
	if int64(len(encoded)) > cfg.MaxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeds configured limit", ErrCMDBResponse)
	}
	var payload deviceViewResponse
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, fmt.Errorf("%w: invalid json", ErrCMDBResponse)
	}
	if payload.Result == nil || !*payload.Result || payload.ErrorCode == nil || *payload.ErrorCode != 0 {
		return nil, fmt.Errorf("%w: upstream business failure", ErrCMDBResponse)
	}
	if payload.Data == nil || payload.Data.Count == nil || payload.Data.Info == nil || *payload.Data.Count < 0 {
		return nil, fmt.Errorf("%w: missing data, count, or info", ErrCMDBResponse)
	}
	return payload.Data, nil
}

func (d deviceViewDTO) toRecord() (DeviceRecord, error) {
	if d.InstID <= 0 || d.DeviceID <= 0 {
		return DeviceRecord{}, fmt.Errorf("%w: inst_id and device_id are required", ErrCMDBRecord)
	}
	if d.InstID != d.DeviceID {
		return DeviceRecord{}, fmt.Errorf("%w: inst_id and device_id conflict", ErrCMDBRecord)
	}
	deviceSN := strings.TrimSpace(d.DeviceSN)
	if deviceSN == "" {
		return DeviceRecord{}, fmt.Errorf("%w: device_sn is required", ErrCMDBRecord)
	}
	if !validParentTypeID(d.ParentTypeID) || d.DeviceTypeID < 0 {
		return DeviceRecord{}, fmt.Errorf("%w: invalid device type", ErrCMDBRecord)
	}

	allIPs, err := d.allIPs()
	if err != nil {
		return DeviceRecord{}, err
	}
	instanceID := d.InstID
	if instanceID == 0 {
		instanceID = d.DeviceID
	}
	name := strings.TrimSpace(d.DeviceName)
	if name == "" {
		name = strings.TrimSpace(d.HostName)
	}
	if name == "" {
		name = deviceSN
	}
	computePlane := make([]ComputePlaneReference, len(d.ComputePlane))
	for i, ref := range d.ComputePlane {
		computePlane[i] = ComputePlaneReference{PodID: ref.PodID}
	}
	return DeviceRecord{
		SourceInstanceID: instanceID,
		DeviceSN:         deviceSN,
		Name:             name,
		ParentTypeID:     d.ParentTypeID,
		DeviceTypeID:     d.DeviceTypeID,
		Role:             strings.TrimSpace(d.Role),
		CabinetUUID:      strings.TrimSpace(d.CabinetUUID),
		PodUUID:          strings.TrimSpace(d.PodUUID),
		ComputePlane:     computePlane,
		AllIPs:           allIPs,
	}, nil
}

func validParentTypeID(id int64) bool {
	return id == 0 || id == 52 || id == 53 || id == 54 || id == 55
}

func (d deviceViewDTO) allIPs() ([]string, error) {
	values := make([]string, 0)
	for _, group := range [][]deviceIPDTO{
		d.DeviceIPInfo.Eth,
		d.DeviceIPInfo.ILO,
		d.DeviceIPInfo.Data,
		d.DeviceIPInfo.Console,
		d.DeviceIPInfo.Management,
	} {
		for _, item := range group {
			values = append(values, item.IP)
		}
	}
	values = append(values, d.EthIP...)
	values = append(values, d.ILOIP...)
	values = append(values, d.DataIP...)
	values = append(values, d.ConsoleIP...)
	values = append(values, d.ManagementIP...)

	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		normalized, err := normalizeDeviceIP(value)
		if err != nil {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func normalizeDeviceIP(value string) (string, error) {
	if address, err := netip.ParseAddr(value); err == nil {
		return address.String(), nil
	}
	if endpoint, err := netip.ParseAddrPort(value); err == nil {
		return endpoint.Addr().String(), nil
	}

	separator := strings.LastIndex(value, ":")
	if separator <= 0 || separator == len(value)-1 {
		return "", errors.New("invalid address")
	}
	address, err := netip.ParseAddr(strings.Trim(value[:separator], "[]"))
	if err != nil {
		return "", err
	}
	ports := strings.FieldsFunc(value[separator+1:], func(r rune) bool { return r == '/' || r == '\\' })
	if len(ports) < 2 {
		return "", errors.New("invalid port list")
	}
	for _, port := range ports {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return "", errors.New("invalid port")
		}
	}
	return address.String(), nil
}
