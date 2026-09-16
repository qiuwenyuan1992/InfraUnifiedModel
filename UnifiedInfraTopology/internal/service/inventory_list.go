package service

import (
	"context"
	"sort"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

func (s *inventoryService) List(ctx context.Context, userID, scopeID, resource, parentID string, q InventoryQuery) (*InventoryPage, error) {
	if err := validateInventoryQuery(resource, parentID, q); err != nil {
		return nil, err
	}
	scopeIDs := []string{}
	if resource == "scopes" {
		if scopeID != "" {
			return nil, ErrInventoryInvalid
		}
		seen := map[string]bool{}
		for _, g := range s.grants {
			if g.UserID == userID && inventoryID(g.ScopeID) && len(g.Permissions) > 0 && !seen[g.ScopeID] {
				seen[g.ScopeID] = true
				scopeIDs = append(scopeIDs, g.ScopeID)
			}
		}
		if len(scopeIDs) == 0 {
			return nil, ErrInventoryForbidden
		}
		sort.Strings(scopeIDs)
	} else if err := s.authorize(userID, scopeID, resource); err != nil {
		return nil, err
	}
	if len(s.cursorKey) < 32 {
		return nil, ErrInventoryNotReady
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	binding := inventoryCursor{Version: 2, UserID: userID, Resource: resource, ScopeID: scopeID, ParentID: parentID, GenerationID: q.GenerationID, Filters: inventoryFilterHash(q)}
	if q.Cursor != "" {
		previous, err := s.decodeCursor(q.Cursor)
		if err != nil {
			return nil, err
		}
		if previous.UserID != binding.UserID || previous.Resource != resource || previous.ScopeID != scopeID || previous.ParentID != parentID || previous.Filters != binding.Filters {
			return nil, ErrInventoryInvalid
		}
		if q.GenerationID != "" && q.GenerationID != previous.GenerationID {
			return nil, ErrInventoryConflict
		}
		if resource == "sync-runs" && previous.LastCreatedAt == nil {
			return nil, ErrInventoryInvalid
		}
		binding = previous
	}
	page := &InventoryPage{}
	var projection *model.TopologyScope
	switch resource {
	case "devices", "interfaces", "addresses":
		scope, generation, err := s.resolveGeneration(ctx, scopeID, binding.GenerationID)
		if err != nil {
			return nil, err
		}
		if q.Cursor != "" && binding.ProjectionEpoch != scope.ProjectionEpoch {
			return nil, ErrInventoryConflict
		}
		projection = scope
		binding.ProjectionEpoch = scope.ProjectionEpoch
		binding.GenerationID = generation.ID
		page.GenerationID = &generation.ID
		page.PublishedAt = generation.PublishedAt
		page.InventoryReady = &generation.InventoryReady
		page.GraphReady = &generation.GraphReady
		page.RoutingReady = &generation.RoutingReady
		if resource != "devices" {
			if err := s.checkProjection(ctx, projection); err != nil {
				return nil, err
			}
			_, err = s.graph.Device(ctx, scopeID, parentID)
			if checkErr := s.checkProjection(ctx, projection); checkErr != nil {
				return nil, checkErr
			}
			if err != nil {
				return nil, inventoryError(err)
			}
		}
	case "scopes":
	default:
		if _, err := s.repo.Scope(ctx, scopeID); err != nil {
			return nil, inventoryError(err)
		}
	}
	query := repository.InventoryListQuery{
		ScopeID: scopeID, ScopeIDs: scopeIDs, ParentID: parentID, Limit: q.Limit,
		LastID: binding.LastID, LastCreatedAt: binding.LastCreatedAt, DeviceKind: q.DeviceKind, Name: q.Name, Lifecycle: q.Lifecycle,
		InterfaceKind: q.InterfaceKind, AddressFamily: q.AddressFamily, Status: q.Status, SourceID: q.SourceID,
	}
	var result *repository.InventoryListResult
	var err error
	if projection != nil {
		if err := s.checkProjection(ctx, projection); err != nil {
			return nil, err
		}
		result, err = s.graph.List(ctx, resource, query)
		if checkErr := s.checkProjection(ctx, projection); checkErr != nil {
			return nil, checkErr
		}
	} else {
		result, err = s.repo.List(ctx, resource, query)
	}
	if err != nil {
		return nil, inventoryError(err)
	}
	page.Items = result.Items
	// 批次只描述本次响应，不参与当前图资产的查询或存储。
	switch rows := result.Items.(type) {
	case []model.Device:
		current := append([]model.Device{}, rows...)
		for i := range current {
			current[i].GenerationID = binding.GenerationID
		}
		page.Items = current
	case []model.Interface:
		current := append([]model.Interface{}, rows...)
		for i := range current {
			current[i].GenerationID = binding.GenerationID
		}
		page.Items = current
	case []model.Address:
		current := append([]model.Address{}, rows...)
		for i := range current {
			current[i].GenerationID = binding.GenerationID
		}
		page.Items = current
	}
	if result.HasMore {
		binding.LastID = result.LastID
		binding.LastCreatedAt = result.LastCreatedAt
		next := s.encodeCursor(binding)
		page.NextCursor = &next
	}
	return page, nil
}
