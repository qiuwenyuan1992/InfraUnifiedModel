package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	v1 "UnifiedInfraTopology/api/v1"
	"UnifiedInfraTopology/internal/service"
	"github.com/gin-gonic/gin"
)

type InventoryHandler struct {
	*Handler
	service service.InventoryService
}

func NewInventoryHandler(handler *Handler, inventory service.InventoryService) *InventoryHandler {
	return &InventoryHandler{Handler: handler, service: inventory}
}

func inventorySuccess(c *gin.Context, status int, data interface{}) {
	c.JSON(status, v1.Response{Code: 0, Message: "ok", Data: data})
}

func inventoryError(c *gin.Context, err error) {
	status, code, message := 500, 50001, "internal_error"
	switch {
	case errors.Is(err, service.ErrInventoryInvalid):
		status, code, message = 400, 40001, "invalid_argument"
	case errors.Is(err, service.ErrInventoryForbidden):
		status, code, message = 403, 40301, "forbidden"
	case errors.Is(err, service.ErrInventoryNotFound):
		status, code, message = 404, 40401, "not_found"
	case errors.Is(err, service.ErrInventoryNotReady):
		status, code, message = 409, 40902, "inventory_not_ready"
	case errors.Is(err, service.ErrInventoryConflict):
		status, code, message = 409, 40903, "state_conflict"
	case errors.Is(err, service.ErrInventoryIdempotency):
		status, code, message = 409, 40904, "idempotency_conflict"
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = 504, 50401, "query_deadline_exceeded"
	}
	c.JSON(status, v1.Response{Code: code, Message: message, Data: gin.H{}})
}

func inventoryQuery(context *gin.Context, allowed ...string) (url.Values, error) {
	query, err := url.ParseQuery(context.Request.URL.RawQuery)
	if err != nil {
		return nil, service.ErrInventoryInvalid
	}
	for key, values := range query {
		valid := false
		for _, name := range allowed {
			if key == name {
				valid = true
				break
			}
		}
		if !valid || len(values) != 1 || values[0] == "" {
			return nil, service.ErrInventoryInvalid
		}
	}
	return query, nil
}

func (h *InventoryHandler) List(resource string) gin.HandlerFunc {
	return func(context *gin.Context) {
		allowed := []string{"limit", "cursor"}
		if resource == "sync-runs" {
			allowed = append(allowed, "status", "source_id")
		}
		query, err := inventoryQuery(context, allowed...)
		if err != nil {
			inventoryError(context, err)
			return
		}
		limit := 0
		if query.Has("limit") {
			limit, err = strconv.Atoi(query.Get("limit"))
			if err != nil || limit < 1 || limit > 200 {
				inventoryError(context, service.ErrInventoryInvalid)
				return
			}
		}
		page, err := h.service.List(context.Request.Context(), GetUserIdFromCtx(context), resource, "", service.InventoryQuery{
			Limit: limit, Cursor: query.Get("cursor"), Status: query.Get("status"), SourceID: query.Get("source_id"),
		})
		if err != nil {
			inventoryError(context, err)
			return
		}
		items, err := v1.InventoryItems(page.Items)
		if err != nil {
			inventoryError(context, err)
			return
		}
		page.Items = items
		inventorySuccess(context, http.StatusOK, page)
	}
}
