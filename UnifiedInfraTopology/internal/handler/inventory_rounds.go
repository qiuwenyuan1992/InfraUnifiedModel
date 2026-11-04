package handler

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"

	v1 "UnifiedInfraTopology/api/v1"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/service"
	"github.com/gin-gonic/gin"
)

type InventoryRoundHandler interface {
	Enqueue(*gin.Context)
	List(*gin.Context)
	Get(*gin.Context)
	Cancel(*gin.Context)
	Diagnostics(bool) gin.HandlerFunc
}
type inventoryRoundHandler struct {
	*Handler
	service service.InventoryRoundService
}

func NewInventoryRoundHandler(h *Handler, s service.InventoryRoundService) InventoryRoundHandler {
	return &inventoryRoundHandler{Handler: h, service: s}
}

// Enqueue godoc
// @Summary Enqueue a durable full synchronization round
// @Tags inventory
// @Security Bearer
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Printable ASCII key, 1–128 characters"
// @Param request body v1.EnqueueInventoryRoundRequest true "Current configured source; mode must be full"
// @Success 202 {object} v1.Response{data=v1.InventoryRoundDTO}
// @Header 202 {string} Location "Round resource URL"
// @Failure 400,401,403,409,500 {object} v1.Response
// @Router /inventory/sync-rounds [post]
func (h *inventoryRoundHandler) Enqueue(c *gin.Context) {
	if _, err := inventoryQuery(c); err != nil {
		inventoryError(c, err)
		return
	}
	request, err := roundBody(c)
	if err != nil {
		inventoryError(c, err)
		return
	}
	round, err := h.service.Enqueue(c.Request.Context(), GetUserIdFromCtx(c), c.GetHeader("Idempotency-Key"), request)
	if err != nil {
		inventoryError(c, err)
		return
	}
	current, phases, err := h.service.Get(c.Request.Context(), GetUserIdFromCtx(c), round.ID)
	if err != nil {
		inventoryError(c, err)
		return
	}
	c.Header("Location", "/v1/inventory/sync-rounds/"+round.ID)
	inventorySuccess(c, http.StatusAccepted, v1.InventoryRound(*current, phases))
}
func roundBody(c *gin.Context) (service.EnqueueInventoryRound, error) {
	var request service.EnqueueInventoryRound
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" || len(c.Request.Header.Values("Idempotency-Key")) != 1 {
		return request, service.ErrInventoryInvalid
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	decoder := json.NewDecoder(c.Request.Body)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return request, service.ErrInventoryInvalid
	}
	fields := map[string]string{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return request, service.ErrInventoryInvalid
		}
		name, ok := token.(string)
		if !ok || (name != "source_id" && name != "mode") {
			return request, service.ErrInventoryInvalid
		}
		if _, exists := fields[name]; exists {
			return request, service.ErrInventoryInvalid
		}
		var value interface{}
		if decoder.Decode(&value) != nil {
			return request, service.ErrInventoryInvalid
		}
		text, ok := value.(string)
		if !ok {
			return request, service.ErrInventoryInvalid
		}
		fields[name] = text
	}
	if _, err = decoder.Token(); err != nil {
		return request, service.ErrInventoryInvalid
	}
	var extra interface{}
	if decoder.Decode(&extra) != io.EOF {
		return request, service.ErrInventoryInvalid
	}
	request.SourceID = fields["source_id"]
	request.Mode = fields["mode"]
	if request.Mode != "full" || len(request.SourceID) != 32 {
		return request, service.ErrInventoryInvalid
	}
	for _, ch := range request.SourceID {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return request, service.ErrInventoryInvalid
		}
	}
	return request, nil
}
func roundQuery(c *gin.Context, diagnostics bool) (service.InventoryQuery, error) {
	allowed := []string{"limit", "cursor", "source_id"}
	if !diagnostics {
		allowed = append(allowed, "status")
	}
	values, err := inventoryQuery(c, allowed...)
	if err != nil {
		return service.InventoryQuery{}, err
	}
	q := service.InventoryQuery{Cursor: values.Get("cursor"), Status: values.Get("status"), SourceID: values.Get("source_id")}
	if values.Has("limit") {
		q.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || q.Limit < 1 || q.Limit > 200 {
			return q, service.ErrInventoryInvalid
		}
	}
	return q, nil
}

// List godoc
// @Summary List durable synchronization rounds
// @Tags inventory
// @Security Bearer
// @Produce json
// @Param limit query int false "Page size, 1–200"
// @Param cursor query string false "Authenticated opaque cursor"
// @Param source_id query string false "Current configured source ID"
// @Param status query string false "Round status"
// @Success 200 {object} v1.Response{data=v1.InventoryRoundPageDTO}
// @Failure 400,401,403,409,500 {object} v1.Response
// @Router /inventory/sync-rounds [get]
func (h *inventoryRoundHandler) List(c *gin.Context) {
	q, err := roundQuery(c, false)
	if err != nil {
		inventoryError(c, err)
		return
	}
	page, err := h.service.List(c.Request.Context(), GetUserIdFromCtx(c), q)
	if err != nil {
		inventoryError(c, err)
		return
	}
	rounds, ok := page.Items.([]model.SyncRound)
	if !ok {
		inventoryError(c, service.ErrInventoryNotReady)
		return
	}
	items := make([]v1.InventoryRoundDTO, len(rounds))
	for i, r := range rounds {
		current, phases, getErr := h.service.Get(c.Request.Context(), GetUserIdFromCtx(c), r.ID)
		if getErr != nil {
			inventoryError(c, getErr)
			return
		}
		items[i] = v1.InventoryRound(*current, phases)
	}
	inventorySuccess(c, 200, v1.InventoryRoundPageDTO{Items: items, NextCursor: page.NextCursor})
}

// Get godoc
// @Summary Get round state and fixed fifteen-phase progress
// @Tags inventory
// @Security Bearer
// @Produce json
// @Param id path string true "32 lowercase hexadecimal round ID"
// @Success 200 {object} v1.Response{data=v1.InventoryRoundDTO}
// @Failure 400,401,403,404,500 {object} v1.Response
// @Router /inventory/sync-rounds/{id} [get]
func (h *inventoryRoundHandler) Get(c *gin.Context) {
	if _, err := inventoryQuery(c); err != nil {
		inventoryError(c, err)
		return
	}
	round, phases, err := h.service.Get(c.Request.Context(), GetUserIdFromCtx(c), c.Param("id"))
	if err != nil {
		inventoryError(c, err)
		return
	}
	inventorySuccess(c, 200, v1.InventoryRound(*round, phases))
}

// Cancel godoc
// @Summary Cancel queued or cooperatively cancel running round without graph rollback
// @Tags inventory
// @Security Bearer
// @Produce json
// @Param id path string true "Round ID"
// @Success 200,202 {object} v1.Response{data=v1.InventoryRoundDTO}
// @Failure 400,401,403,404,409,500 {object} v1.Response
// @Router /inventory/sync-rounds/{id}/cancel [post]
func (h *inventoryRoundHandler) Cancel(c *gin.Context) {
	if _, err := inventoryQuery(c); err != nil {
		inventoryError(c, err)
		return
	}
	round, accepted, err := h.service.Cancel(c.Request.Context(), GetUserIdFromCtx(c), c.Param("id"))
	if err != nil {
		inventoryError(c, err)
		return
	}
	current, phases, err := h.service.Get(c.Request.Context(), GetUserIdFromCtx(c), round.ID)
	if err != nil {
		inventoryError(c, err)
		return
	}
	status := 200
	if accepted {
		status = 202
	}
	inventorySuccess(c, status, v1.InventoryRound(*current, phases))
}

// RoundDiagnostics godoc
// @Summary List safe diagnostics across a round's child runs
// @Tags inventory
// @Security Bearer
// @Produce json
// @Param id path string true "Round ID"
// @Param limit query int false "Page size, 1–200"
// @Param cursor query string false "User, parent and filter-bound cursor"
// @Param source_id query string false "Source filter"
// @Success 200 {object} v1.Response{data=v1.InventoryDiagnosticPageDTO}
// @Failure 400,401,403,404,500 {object} v1.Response
// @Router /inventory/sync-rounds/{id}/diagnostics [get]
func (h *inventoryRoundHandler) RoundDiagnostics(c *gin.Context) { h.Diagnostics(true)(c) }

// RunDiagnostics godoc
// @Summary List safe single-run diagnostics
// @Tags inventory
// @Security Bearer
// @Produce json
// @Param run_id path string true "Run ID"
// @Param limit query int false "Page size, 1–200"
// @Param cursor query string false "User, parent and filter-bound cursor"
// @Param source_id query string false "Source filter"
// @Success 200 {object} v1.Response{data=v1.InventoryDiagnosticPageDTO}
// @Failure 400,401,403,404,500 {object} v1.Response
// @Router /inventory/sync-runs/{run_id}/diagnostics [get]
func (h *inventoryRoundHandler) RunDiagnostics(c *gin.Context) { h.Diagnostics(false)(c) }
func (h *inventoryRoundHandler) Diagnostics(isRound bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		q, err := roundQuery(c, true)
		if err != nil {
			inventoryError(c, err)
			return
		}
		id := c.Param("run_id")
		if isRound {
			id = c.Param("id")
		}
		page, err := h.service.Diagnostics(c.Request.Context(), GetUserIdFromCtx(c), id, isRound, q)
		if err != nil {
			inventoryError(c, err)
			return
		}
		diagnostics, ok := page.Items.([]model.SyncDiagnostic)
		if !ok {
			inventoryError(c, service.ErrInventoryNotReady)
			return
		}
		items := make([]v1.InventoryDiagnosticDTO, len(diagnostics))
		for i, d := range diagnostics {
			items[i] = v1.InventoryDiagnostic(d)
		}
		inventorySuccess(c, 200, v1.InventoryDiagnosticPageDTO{Items: items, NextCursor: page.NextCursor})
	}
}
