package adapter

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"UnifiedInfraTopology/internal/model"
)

const cmdbInterfacePageSize = 1000

var _ EntityPassCollector = (*CMDB)(nil)
var _ EntityPageStream = (*cmdbInterfacePass)(nil)

type cmdbInterfacePartition struct {
	deviceInstID   int64
	sn             string
	total          int
	baseOffset     int
	firstPage      []EntityRecord
	lastPageEnd    int
	lastInstanceID int64
}

type cmdbInterfacePass struct {
	collector *CMDB
	cfg       cmdbConfig
	condition map[string]any
	devices   []cmdbInterfacePartition
	total     int
}

func (c *CMDB) OpenEntityPass(ctx context.Context, source model.Source, entity model.EntityType) (EntityPageStream, error) {
	if entity == model.EntityPod {
		return c.openPodPass(ctx, source)
	}
	if entity != model.EntityInterface {
		return nil, fmt.Errorf("%w: entity passes require interface", ErrCMDBConfig)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, err := c.configFor(source)
	if err != nil {
		return nil, err
	}
	configured, err := c.conditionFor(source, entity)
	if err != nil {
		return nil, err
	}
	condition := make(map[string]any, len(configured)+2)
	for key, value := range configured {
		condition[key] = value
	}
	idc, err := strconv.ParseInt(fmt.Sprint(condition["idc_id"]), 10, 64)
	if err != nil || idc <= 0 {
		return nil, fmt.Errorf("%w: interface passes require a positive idc_id", ErrCMDBConfig)
	}

	discoveryCfg := cfg
	discoveryCfg.DeviceCondition = map[string]any{"idc_id": condition["idc_id"], "parent_type_id": 54}
	var selectedSN string
	if value, exists := condition["sn"]; exists {
		sn, ok := value.(string)
		if !ok || strings.TrimSpace(sn) == "" {
			return nil, fmt.Errorf("%w: interface sn must be a nonempty string", ErrCMDBConfig)
		}
		selectedSN = strings.TrimSpace(sn)
		condition["sn"] = selectedSN
		discoveryCfg.DeviceCondition["device_sn"] = selectedSN
	}
	pass := &cmdbInterfacePass{collector: c, cfg: cfg, condition: condition}
	seenSN := make(map[string]struct{})
	var lastInstID int64
	discoveryTotal := -1
	for start := 0; ; {
		page, err := c.collectDevicePageWithFields(ctx, discoveryCfg, start, []string{"inst_id", "device_sn", "parent_type_id"})
		if err != nil {
			return nil, err
		}
		items, total := *page.Info, *page.Count
		if discoveryTotal < 0 {
			discoveryTotal = total
		}
		if total != discoveryTotal {
			return nil, fmt.Errorf("%w: network device count changed during discovery", ErrCMDBResponse)
		}
		if start > total || len(items) != min(cfg.PageSize, total-start) {
			return nil, fmt.Errorf("%w: incomplete network device page", ErrCMDBResponse)
		}
		for _, device := range items {
			sn := strings.TrimSpace(device.DeviceSN)
			if device.ParentTypeID != 54 || device.InstID <= 0 || sn == "" {
				return nil, fmt.Errorf("%w: network device requires parent_type_id 54, positive inst_id and device_sn", ErrCMDBRecord)
			}
			if device.InstID <= lastInstID {
				return nil, fmt.Errorf("%w: network device inst_id is not strictly increasing", ErrCMDBResponse)
			}
			lastInstID = device.InstID
			if _, exists := seenSN[sn]; exists {
				return nil, fmt.Errorf("%w: duplicate network device sn", ErrCMDBResponse)
			}
			seenSN[sn] = struct{}{}
			// 上游若忽略 SN 条件，本地仍只采集明确指定的设备。
			if selectedSN != "" && selectedSN != sn {
				continue
			}
			pass.devices = append(pass.devices, cmdbInterfacePartition{deviceInstID: device.InstID, sn: sn})
		}
		start += len(items)
		if start >= total {
			break
		}
	}
	// 设备发现仍使用配置分页，端口按设备固定每页 1000 条，避免计数后重复读取。
	pass.cfg.PageSize = cmdbInterfacePageSize
	if err := pass.prefetchFirstPages(ctx); err != nil {
		return nil, err
	}
	for i := range pass.devices {
		device := &pass.devices[i]
		if device.total > int(^uint(0)>>1)-pass.total {
			return nil, fmt.Errorf("%w: interface total exceeds supported offset", ErrCMDBResponse)
		}
		device.baseOffset = pass.total
		pass.total += device.total
	}
	return pass, nil
}

func (p *cmdbInterfacePass) prefetchFirstPages(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var workers sync.WaitGroup
	var firstError sync.Once
	var fetchErr error
	for worker := 0; worker < min(p.cfg.Concurrency, len(p.devices)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				device := &p.devices[index]
				page, err := p.collector.collectEntityPage(ctx, p.cfg, p.conditionForSN(device.sn), model.EntityInterface, 0)
				if err == nil && len(page.Records) != min(p.cfg.PageSize, page.Total) {
					err = fmt.Errorf("%w: incomplete interface first page", ErrCMDBResponse)
				}
				if err != nil {
					firstError.Do(func() {
						fetchErr = err
						cancel()
					})
					return
				}
				device.total = page.Total
				device.firstPage = page.Records
				device.lastPageEnd = len(page.Records)
				if len(page.Records) > 0 {
					device.lastInstanceID = page.Records[len(page.Records)-1].SourceInstanceID
				}
			}
		}()
	}
dispatch:
	for index := range p.devices {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if fetchErr != nil {
		return fetchErr
	}
	return ctx.Err()
}

func (p *cmdbInterfacePass) conditionForSN(sn string) map[string]any {
	condition := make(map[string]any, len(p.condition)+1)
	for key, value := range p.condition {
		condition[key] = value
	}
	condition["sn"] = sn
	return condition
}

func (p *cmdbInterfacePass) Page(ctx context.Context, start int) (EntityPage, error) {
	if err := ctx.Err(); err != nil {
		return EntityPage{}, err
	}
	if start < 0 || start > p.total {
		return EntityPage{}, fmt.Errorf("%w: interface offset is outside pass", ErrCMDBConfig)
	}
	if start == p.total {
		return EntityPage{Records: []EntityRecord{}, NextOffset: start, Total: p.total, Done: true}, nil
	}
	index := sort.Search(len(p.devices), func(i int) bool {
		return p.devices[i].baseOffset+p.devices[i].total > start
	})
	device := &p.devices[index]
	localStart := start - device.baseOffset
	var page EntityPage
	if localStart == 0 {
		page = EntityPage{Records: append([]EntityRecord(nil), device.firstPage...), Total: device.total}
	} else {
		var err error
		page, err = p.collector.collectEntityPage(ctx, p.cfg, p.conditionForSN(device.sn), model.EntityInterface, localStart)
		if err != nil {
			return EntityPage{}, err
		}
	}
	if page.Total != device.total {
		return EntityPage{}, fmt.Errorf("%w: interface count changed during pass", ErrCMDBResponse)
	}
	if len(page.Records) != min(p.cfg.PageSize, device.total-localStart) {
		return EntityPage{}, fmt.Errorf("%w: incomplete interface partition page", ErrCMDBResponse)
	}
	if localStart == device.lastPageEnd && page.Records[0].SourceInstanceID <= device.lastInstanceID {
		return EntityPage{}, fmt.Errorf("%w: interface inst_id is not strictly increasing across pages", ErrCMDBResponse)
	}
	device.lastPageEnd = localStart + len(page.Records)
	device.lastInstanceID = page.Records[len(page.Records)-1].SourceInstanceID
	for i := range page.Records {
		page.Records[i].Partition = device.deviceInstID
		page.Records[i].OwnerDeviceSN = device.sn
	}
	page.NextOffset = start + len(page.Records)
	page.Total = p.total
	page.Done = page.NextOffset == p.total
	return page, nil
}
