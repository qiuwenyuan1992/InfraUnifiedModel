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

func NewInventoryHandler(h *Handler, s service.InventoryService) *InventoryHandler {
	return &InventoryHandler{Handler: h, service: s}
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
		status, code, message = 409, 40902, "projection_not_ready"
	case errors.Is(err, service.ErrInventoryConflict):
		status, code, message = 409, 40903, "state_conflict"
	case errors.Is(err, service.ErrInventoryIdempotency):
		status, code, message = 409, 40904, "idempotency_conflict"
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = 504, 50401, "query_deadline_exceeded"
	}
	c.JSON(status, v1.Response{Code: code, Message: message, Data: gin.H{}})
}

func inventoryQuery(c *gin.Context, allowed ...string) (url.Values, error) {
	q, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return nil, service.ErrInventoryInvalid
	}
	for key, values := range q {
		ok := false
		for _, name := range allowed {
			if key == name {
				ok = true
				break
			}
		}
		if !ok || len(values) != 1 || values[0] == "" {
			return nil, service.ErrInventoryInvalid
		}
	}
	return q, nil
}

func (h *InventoryHandler) List(resource string) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed := []string{"limit", "cursor"}
		switch resource {
		case "devices":
			allowed = append(allowed, "generation_id", "device_kind", "name", "lifecycle")
		case "interfaces":
			allowed = append(allowed, "generation_id", "interface_kind")
		case "addresses":
			allowed = append(allowed, "generation_id", "address_family")
		case "sync-runs":
			allowed = append(allowed, "status", "source_id")
		}
		q, err := inventoryQuery(c, allowed...)
		if err != nil {
			inventoryError(c, err)
			return
		}
		limit := 0
		if q.Has("limit") {
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil || limit < 1 || limit > 200 {
				inventoryError(c, service.ErrInventoryInvalid)
				return
			}
		}
		page, err := h.service.List(c.Request.Context(), GetUserIdFromCtx(c), c.Param("scope_id"), resource, c.Param("device_id"), service.InventoryQuery{
			Limit: limit, Cursor: q.Get("cursor"), GenerationID: q.Get("generation_id"), DeviceKind: q.Get("device_kind"), Name: q.Get("name"), Lifecycle: q.Get("lifecycle"), InterfaceKind: q.Get("interface_kind"), AddressFamily: q.Get("address_family"), Status: q.Get("status"), SourceID: q.Get("source_id"),
		})
		if err != nil {
			inventoryError(c, err)
			return
		}
		items, err := v1.InventoryItems(page.Items)
		if err != nil {
			inventoryError(c, err)
			return
		}
		page.Items = items
		inventorySuccess(c, http.StatusOK, page)
	}
}

func (h *InventoryHandler) GetDevice(c *gin.Context) {
	q, err := inventoryQuery(c, "generation_id")
	if err != nil {
		inventoryError(c, err)
		return
	}
	d, err := h.service.GetDevice(c.Request.Context(), GetUserIdFromCtx(c), c.Param("scope_id"), c.Param("device_id"), q.Get("generation_id"))
	if err != nil {
		inventoryError(c, err)
		return
	}
	inventorySuccess(c, 200, gin.H{"device": v1.InventoryDevice(d.Device), "generation": v1.InventoryGeneration(d.Generation)})
}
