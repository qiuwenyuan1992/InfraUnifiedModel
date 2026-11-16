// Package handler 传输层：解析请求、调用 service、输出 JSON。
// 不直接访问数据库或调用模型；身份由服务端从请求头识别并映射权限。
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/repository"
	"UnifiedInfraOKB/internal/service"
)

// Handler HTTP 门面。
type Handler struct {
	svc *service.Service
	// authz 操作者 ID → 权限等级映射；nil 时全部视为 level 1（开发模式）。
	authz map[string]int
}

// New 装配 handler。authz 为 nil 表示开发模式：所有调用方 level 1。
func New(svc *service.Service, authz map[string]int) *Handler {
	return &Handler{svc: svc, authz: authz}
}

// caller 从请求解析服务端识别的操作主体。
// 生产中由网关注入可信头；此处从 Authorization: Bearer <operator> 取 ID 查权限表。
// 未登记的调用方按 level 0 处理（仅摘要可见），不信任客户端自报等级。
func (h *Handler) caller(r *http.Request) service.Caller {
	id := ""
	if parts := splitBearer(r.Header.Get("Authorization")); len(parts) == 2 {
		id = parts[1]
	}
	if id == "" {
		id = "anonymous"
	}
	level, ok := h.authz[id]
	if !ok {
		level = 0
	}
	if h.authz == nil {
		level = 1 // 开发模式：未配置权限表时统一 level 1
	}
	return service.Caller{ID: id, PermissionLevel: level}
}

func splitBearer(s string) []string {
	if len(s) > 7 && s[:7] == "Bearer " {
		return []string{"Bearer", s[7:]}
	}
	return nil
}

// writeJSON 统一 JSON 输出。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 统一错误输出。
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Routes 注册全部路由（路径与 api-boundaries.md 对齐）。
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/lookup", h.handleLookup)
	mux.HandleFunc("GET /api/v1/trace", h.handleTrace)
	mux.HandleFunc("GET /api/v1/evidence", h.handleEvidence)
	mux.HandleFunc("POST /api/v1/report", h.handleReport)
	mux.HandleFunc("POST /api/v1/sources", h.handleIngest)
	mux.HandleFunc("GET /api/v1/review/queue", h.handleReviewQueue)
	mux.HandleFunc("POST /api/v1/edges/{id}/confirm", h.handleConfirmEdge)
	mux.HandleFunc("POST /api/v1/edges/{id}/reject", h.handleRejectEdge)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

// handleLookup GET /api/v1/lookup?alert_signature=<sig>
func (h *Handler) handleLookup(w http.ResponseWriter, r *http.Request) {
	sig := r.URL.Query().Get("alert_signature")
	if sig == "" {
		writeErr(w, http.StatusBadRequest, "alert_signature is required")
		return
	}
	includeDraft := h.canReview(h.caller(r)) && r.URL.Query().Get("include_draft") == "true"
	res, err := h.svc.Lookup(sig, includeDraft)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleTrace GET /api/v1/trace?anomaly_code=<code>&max_hops=3&top_k=5&facts=<json>
func (h *Handler) handleTrace(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("anomaly_code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "anomaly_code is required")
		return
	}
	maxHops, _ := strconv.Atoi(r.URL.Query().Get("max_hops"))
	topK, _ := strconv.Atoi(r.URL.Query().Get("top_k"))
	facts := map[string]interface{}{}
	if raw := r.URL.Query().Get("facts"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &facts); err != nil {
			writeErr(w, http.StatusBadRequest, "facts must be a JSON object")
			return
		}
	}
	includeDraft := h.canReview(h.caller(r)) && r.URL.Query().Get("include_draft") == "true"
	res, err := h.svc.Trace(code, maxHops, topK, facts, includeDraft)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "start anomaly not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleEvidence GET /api/v1/evidence?ref=<stable_code 或 edge_id>
func (h *Handler) handleEvidence(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "ref is required")
		return
	}
	res, err := h.svc.Evidence(h.caller(r), ref)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "entity not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// fragment 级权限：存在被裁剪的原文时显式标记（api-boundaries.md 第 3 节）
	writeJSON(w, http.StatusOK, res)
}

// handleReport POST /api/v1/report
func (h *Handler) handleReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DiagnosisRef string `json:"diagnosis_ref"`
		EdgeID       string `json:"edge_id"`
		Verdict      string `json:"verdict"`
		Notes        string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	caller := h.caller(r)
	res, err := h.svc.Report(service.ReportInput{
		DiagnosisRef: req.DiagnosisRef, EdgeID: req.EdgeID,
		Verdict: model.Verdict(req.Verdict), OperatorID: caller.ID, Notes: req.Notes,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "edge not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleIngest POST /api/v1/sources —— S0 摄取入口
func (h *Handler) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	caller := h.caller(r)
	out, err := h.svc.Ingest(r.Context(), service.IngestInput{
		Kind: model.SourceKind(req.Kind), Title: req.Title, Content: req.Content, OperatorID: caller.ID,
	})
	if err != nil {
		// 脱敏校验失败：422 语义（Schema/内容不合规）
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleReviewQueue GET /api/v1/review/queue —— S2 复核队列
func (h *Handler) handleReviewQueue(w http.ResponseWriter, r *http.Request) {
	if !h.canReview(h.caller(r)) {
		writeErr(w, http.StatusForbidden, "review permission required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := h.svc.ReviewQueue(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string][]model.CausalRule{"edges": list})
}

// handleConfirmEdge POST /api/v1/edges/{id}/confirm —— S2 治理
func (h *Handler) handleConfirmEdge(w http.ResponseWriter, r *http.Request) {
	if !h.canReview(h.caller(r)) {
		writeErr(w, http.StatusForbidden, "review permission required")
		return
	}
	if err := h.svc.ConfirmEdge(r.PathValue("id"), h.caller(r).ID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "edge not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "curated"})
}

// handleRejectEdge POST /api/v1/edges/{id}/reject —— S2 治理
func (h *Handler) handleRejectEdge(w http.ResponseWriter, r *http.Request) {
	if !h.canReview(h.caller(r)) {
		writeErr(w, http.StatusForbidden, "review permission required")
		return
	}
	if err := h.svc.RejectEdge(r.PathValue("id"), h.caller(r).ID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "edge not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "draft"})
}

// canReview 审核权限：权限表等级 >= 2 视为可审核（含 include_draft 与治理端点）。
func (h *Handler) canReview(c service.Caller) bool {
	return c.PermissionLevel >= 2
}
