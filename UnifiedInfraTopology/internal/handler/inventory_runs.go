package handler

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"

	v1 "UnifiedInfraTopology/api/v1"
	"UnifiedInfraTopology/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *InventoryHandler) Enqueue(c *gin.Context) {
	if _, err := inventoryQuery(c); err != nil {
		inventoryError(c, err)
		return
	}
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" || len(c.Request.Header.Values("Idempotency-Key")) != 1 {
		inventoryError(c, service.ErrInventoryInvalid)
		return
	}
	// 限制请求体大小，并拒绝重复字段，避免歧义影响幂等摘要。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	decoder := json.NewDecoder(c.Request.Body)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		inventoryError(c, service.ErrInventoryInvalid)
		return
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			inventoryError(c, service.ErrInventoryInvalid)
			return
		}
		name, ok := token.(string)
		if !ok || (name != "source_ids" && name != "mode" && name != "base_generation_id") {
			inventoryError(c, service.ErrInventoryInvalid)
			return
		}
		if _, exists := fields[name]; exists {
			inventoryError(c, service.ErrInventoryInvalid)
			return
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			inventoryError(c, service.ErrInventoryInvalid)
			return
		}
		fields[name] = raw
	}
	if _, err = decoder.Token(); err != nil {
		inventoryError(c, service.ErrInventoryInvalid)
		return
	}
	var extra interface{}
	if decoder.Decode(&extra) != io.EOF {
		inventoryError(c, service.ErrInventoryInvalid)
		return
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		inventoryError(c, err)
		return
	}
	var request service.EnqueueInventoryRun
	if json.Unmarshal(encoded, &request) != nil {
		inventoryError(c, service.ErrInventoryInvalid)
		return
	}
	run, err := h.service.Enqueue(c.Request.Context(), GetUserIdFromCtx(c), c.Param("scope_id"), c.GetHeader("Idempotency-Key"), request)
	if err != nil {
		inventoryError(c, err)
		return
	}
	c.Header("Location", "/v1/scopes/"+c.Param("scope_id")+"/sync-runs/"+run.ID)
	inventorySuccess(c, http.StatusAccepted, v1.InventoryRun(*run))
}

func (h *InventoryHandler) GetRun(c *gin.Context) {
	if _, err := inventoryQuery(c); err != nil {
		inventoryError(c, err)
		return
	}
	run, err := h.service.GetRun(c.Request.Context(), GetUserIdFromCtx(c), c.Param("scope_id"), c.Param("run_id"))
	if err != nil {
		inventoryError(c, err)
		return
	}
	inventorySuccess(c, http.StatusOK, v1.InventoryRun(*run))
}

func (h *InventoryHandler) CancelRun(c *gin.Context) {
	if _, err := inventoryQuery(c); err != nil {
		inventoryError(c, err)
		return
	}
	run, accepted, err := h.service.CancelRun(c.Request.Context(), GetUserIdFromCtx(c), c.Param("scope_id"), c.Param("run_id"))
	if err != nil {
		inventoryError(c, err)
		return
	}
	status := http.StatusOK
	if accepted {
		status = http.StatusAccepted
	}
	inventorySuccess(c, status, v1.InventoryRun(*run))
}
