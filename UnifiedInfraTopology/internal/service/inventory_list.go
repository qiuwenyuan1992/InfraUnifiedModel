package service

import (
	"context"

	"UnifiedInfraTopology/internal/repository"
)

func (s *inventoryService) List(ctx context.Context, userID, resource, parentID string, query InventoryQuery) (*InventoryPage, error) {
	if err := validateInventoryQuery(resource, parentID, query); err != nil {
		return nil, err
	}
	if err := s.authorize(userID, resource); err != nil {
		return nil, err
	}
	if len(s.cursorKey) < 32 {
		return nil, ErrInventoryNotReady
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	binding := inventoryCursor{
		Version: 1, UserID: userID, Resource: resource, Filters: inventoryFilterHash(query),
	}
	if query.Cursor != "" {
		previous, err := s.decodeCursor(query.Cursor)
		if err != nil {
			return nil, err
		}
		if previous.UserID != binding.UserID || previous.Resource != binding.Resource || previous.Filters != binding.Filters {
			return nil, ErrInventoryInvalid
		}
		if resource == "sync-runs" && previous.LastCreatedAt == nil {
			return nil, ErrInventoryInvalid
		}
		binding = previous
	}
	result, err := s.repo.List(ctx, resource, repository.InventoryListQuery{
		Limit: query.Limit, LastID: binding.LastID, LastCreatedAt: binding.LastCreatedAt,
		Status: query.Status, SourceID: query.SourceID,
	})
	if err != nil {
		return nil, inventoryError(err)
	}
	page := &InventoryPage{Items: result.Items}
	if result.HasMore {
		binding.LastID = result.LastID
		binding.LastCreatedAt = result.LastCreatedAt
		next := s.encodeCursor(binding)
		page.NextCursor = &next
	}
	return page, nil
}
