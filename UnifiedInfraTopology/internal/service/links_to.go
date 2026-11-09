package service

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type LinksToResult struct {
	Edges, Devices, Skipped, MissingEndpoints int
	Reasons                                   map[string]int
}
type LinksToWorker struct {
	source *adapter.CMDB
	graph  repository.TopologyGraphRepository
}

func NewLinksToWorker(source *adapter.CMDB, graph repository.TopologyGraphRepository) *LinksToWorker {
	return &LinksToWorker{source: source, graph: graph}
}

func (w *LinksToWorker) Run(ctx context.Context, source model.Source, at time.Time) (LinksToResult, error) {
	result := LinksToResult{Reasons: map[string]int{}}
	if at.IsZero() {
		return result, fmt.Errorf("links_to snapshot missing")
	}
	reader, ok := w.graph.(repository.LinksToEndpointReader)
	if !ok {
		return result, fmt.Errorf("links_to endpoint reader unavailable")
	}
	_, candidates, count, skipped, reasons, err := w.collect(ctx, source)
	result.Devices = count
	result.Skipped = skipped
	result.Reasons = reasons
	if err != nil {
		return result, err
	}
	eligible := make([]bool, len(candidates))
	err = parallelLinksCandidates(ctx, len(candidates), func(c context.Context, index int) error {
		candidate := candidates[index]
		valid, err := reader.LinksToEndpointsValid(c, model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.FromDeviceSN}, model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.ToDeviceSN}, candidate.Identity.From, candidate.Identity.To)
		if err != nil {
			return fmt.Errorf("links_to endpoint lookup failed: %w", err)
		}
		eligible[index] = valid
		return nil
	})
	if err != nil {
		return result, err
	}
	accepted := []adapter.LinksToCandidate{}
	for index, candidate := range candidates {
		if !eligible[index] {
			result.MissingEndpoints++
			result.Skipped++
			result.Reasons["graph_endpoint_mismatch"]++
			continue
		}
		accepted = append(accepted, candidate)
	}
	written := make([]bool, len(accepted))
	err = parallelLinksCandidates(ctx, len(accepted), func(c context.Context, index int) error {
		candidate := accepted[index]
		if err := w.graph.UpsertRelation(c, model.Relation{Identity: candidate.Identity, CreatedAt: at, SyncedAt: at}); err != nil {
			return fmt.Errorf("links_to write failed: %w", err)
		}
		written[index] = true
		return nil
	})
	for _, ok := range written {
		if ok {
			result.Edges++
		}
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// 端点预检完成后才发布；最多八个独立候选并行，错误取消并等待所有写入退出。
func parallelLinksCandidates(ctx context.Context, count int, action func(context.Context, int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, count)
	for index := 0; index < count; index++ {
		jobs <- index
	}
	close(jobs)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	for worker := 0; worker < min(8, count); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := action(ctx, index); err != nil {
					once.Do(func() { firstErr = err; cancel() })
					return
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

func (w *LinksToWorker) collect(ctx context.Context, source model.Source) (string, []adapter.LinksToCandidate, int, int, map[string]int, error) {
	h := sha256.New()
	total := -1
	last := int64(0)
	skipped := 0
	reasons := map[string]int{}
	seenDevices := map[string]bool{}
	seen := map[string]adapter.LinksToCandidate{}
	candidates := []adapter.LinksToCandidate{}
	pages, step, err := w.collectPages(ctx, source)
	if err != nil {
		return "", nil, total, skipped, reasons, err
	}
	for index, page := range pages {
		start := index * step
		if total < 0 {
			total = page.Total
		}
		if page.Total != total || page.NextOffset < start || page.NextOffset > total || (page.NextOffset == start && !page.Done) || page.Done != (page.NextOffset == total) || (page.NextOffset > start && (page.FirstInstanceID <= last || page.LastInstanceID < page.FirstInstanceID)) {
			return "", nil, total, skipped, reasons, fmt.Errorf("links_to pagination changed")
		}
		if page.NextOffset > start {
			last = page.LastInstanceID
		}
		for _, sn := range page.DeviceSNs {
			if sn == "" || seenDevices[sn] {
				return "", nil, total, skipped, reasons, fmt.Errorf("links_to duplicate source device SN")
			}
			seenDevices[sn] = true
		}
		skipped += page.Skipped
		for reason, count := range page.Reasons {
			reasons[reason] += count
		}
		encoded, _ := json.Marshal([]any{start, page.NextOffset, page.Total, page.SourceDigest, page.Skipped})
		h.Write(encoded)
		for _, candidate := range page.Links {
			id, err := candidate.Identity.ID()
			if err != nil {
				return "", nil, total, skipped, reasons, err
			}
			if previous, exists := seen[id]; exists {
				if previous != candidate {
					return "", nil, total, skipped, reasons, fmt.Errorf("links_to conflicting ownership")
				}
				continue
			}
			seen[id] = candidate
			candidates = append(candidates, candidate)
		}
		if page.Done {
			return hex.EncodeToString(h.Sum(nil)), candidates, total, skipped, reasons, nil
		}
	}
	return "", nil, total, skipped, reasons, fmt.Errorf("links_to page sequence incomplete")
}

// 最多四个独立设备页并行读取；结果仍按来源偏移排序后校验，避免异步顺序改变摘要。
func (w *LinksToWorker) collectPages(ctx context.Context, source model.Source) ([]adapter.LinksToPage, int, error) {
	first, err := w.source.CollectLinksToPage(ctx, source, 0)
	if err != nil {
		return nil, 0, err
	}
	if first.Total < 0 || first.NextOffset < 0 || first.NextOffset > first.Total || (first.NextOffset == 0 && !first.Done) {
		return nil, 0, fmt.Errorf("links_to invalid initial page")
	}
	if first.Done {
		return []adapter.LinksToPage{first}, first.NextOffset, nil
	}
	step := first.NextOffset
	count := (first.Total + step - 1) / step
	pages := make([]adapter.LinksToPage, count)
	pages[0] = first
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, count-1)
	for index := 1; index < count; index++ {
		jobs <- index
	}
	close(jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for worker := 0; worker < min(4, count-1); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				page, err := w.source.CollectLinksToPage(ctx, source, index*step)
				if err == nil && (page.Total != first.Total || page.NextOffset != min((index+1)*step, first.Total)) {
					err = fmt.Errorf("links_to parallel source page changed")
				}
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
				pages[index] = page
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, step, firstErr
	}
	if ctx.Err() != nil {
		return nil, step, ctx.Err()
	}
	return pages, step, nil
}
