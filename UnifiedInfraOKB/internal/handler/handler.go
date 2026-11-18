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
}

// New 装配 handler。
func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// operatorID 从 Authorization: Bearer <operator> 取操作者署名，仅用于审计记录；
// 本期不做按动作授权（2026-11-16 用户决定后置 S3）。
func operatorID(r *http.Request) string {
	if parts := splitBearer(r.Header.Get("Authorization")); len(parts) == 2 && parts[1] != "" {
		return parts[1]
	}
	return "anonymous"
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
	mux.HandleFunc("GET /api/v1/graph", h.handleGraph)
	mux.HandleFunc("GET /api/v1/knowledge", h.handleKnowledge)
	mux.HandleFunc("GET /api/v1/scenarios", h.handleScenarioCards)
	mux.HandleFunc("POST /api/v1/scenarios", h.handleScenarioUpsert)
	mux.HandleFunc("GET /api/v1/scenarios/{code}", h.handleScenarioDetail)
	mux.HandleFunc("POST /api/v1/scenarios/{code}/nodes", h.handleScenarioAssign)
	mux.HandleFunc("GET /api/v1/lookup", h.handleLookup)
	mux.HandleFunc("GET /api/v1/trace", h.handleTrace)
	mux.HandleFunc("GET /api/v1/mitigations", h.handleMitigations)
	mux.HandleFunc("GET /api/v1/indications", h.handleIndications)
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

// handleKnowledge GET /api/v1/knowledge?kind=anomaly|rule|mapping&status=&q=&limit=&offset=
func (h *Handler) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	kind := query.Get("kind")
	if kind == "" {
		kind = "anomaly"
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))
	res, err := h.svc.Knowledge(kind, model.KnowledgeStatus(query.Get("status")), query.Get("q"), limit, offset)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleScenarioCards GET /api/v1/scenarios —— 场景卡片墙
func (h *Handler) handleScenarioCards(w http.ResponseWriter, r *http.Request) {
	cards, err := h.svc.ScenarioCards()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scenarios": cards})
}

// handleScenarioUpsert POST /api/v1/scenarios —— 创建/更新场景
func (h *Handler) handleScenarioUpsert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Domain      string `json:"domain"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.svc.UpsertScenario(&model.Scenario{
		Code: req.Code, Name: req.Name, Domain: req.Domain, Description: req.Description,
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"code": req.Code, "status": "ok"})
}

// handleScenarioDetail GET /api/v1/scenarios/{code} —— 场景详情（成员节点 + 场景内边）
func (h *Handler) handleScenarioDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := h.svc.ScenarioDetailByCode(r.PathValue("code"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "scenario not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleScenarioAssign POST /api/v1/scenarios/{code}/nodes —— 指派成员节点
func (h *Handler) handleScenarioAssign(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StableCodes []string `json:"stable_codes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	n, err := h.svc.AssignScenarioNodes(r.PathValue("code"), req.StableCodes, operatorID(r))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"assigned": n})
}

// handleGraph GET /api/v1/graph —— 整图视图（可视化消费）
func (h *Handler) handleGraph(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Graph()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleLookup GET /api/v1/lookup?alert_signature=<sig>
func (h *Handler) handleLookup(w http.ResponseWriter, r *http.Request) {
	sig := r.URL.Query().Get("alert_signature")
	if sig == "" {
		writeErr(w, http.StatusBadRequest, "alert_signature is required")
		return
	}
	includeDraft := r.URL.Query().Get("include_draft") == "true"
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
	includeDraft := r.URL.Query().Get("include_draft") == "true"
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
	res, err := h.svc.Evidence(ref)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "entity not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleMitigations GET /api/v1/mitigations?anomaly_code=<code>&root_cause_code=<code>&include_draft=true
// root_cause_code 可选：诊断结论的根因，用于按根因挂靠过滤处置建议（防张冠李戴）。
func (h *Handler) handleMitigations(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("anomaly_code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "anomaly_code is required")
		return
	}
	includeDraft := r.URL.Query().Get("include_draft") == "true"
	rows, err := h.svc.Mitigations(code, r.URL.Query().Get("root_cause_code"), includeDraft)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "anomaly not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anomaly_code": code, "mitigations": rows})
}

// handleIndications GET /api/v1/indications?anomaly_code=<code>&direction=outgoing|incoming
// outgoing：该异常会表现为什么症状；incoming：该症状可能指示哪些根因（诊断反查）。
func (h *Handler) handleIndications(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("anomaly_code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "anomaly_code is required")
		return
	}
	direction := r.URL.Query().Get("direction")
	if direction == "" {
		direction = "incoming" // 诊断反查是主消费场景，作缺省
	}
	includeDraft := r.URL.Query().Get("include_draft") == "true"
	rows, err := h.svc.Indications(code, direction, includeDraft)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "anomaly not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anomaly_code": code, "direction": direction, "indications": rows})
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
	res, err := h.svc.Report(service.ReportInput{
		DiagnosisRef: req.DiagnosisRef, EdgeID: req.EdgeID,
		Verdict: model.Verdict(req.Verdict), OperatorID: operatorID(r), Notes: req.Notes,
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
	out, err := h.svc.Ingest(r.Context(), service.IngestInput{
		Kind: model.SourceKind(req.Kind), Title: req.Title, Content: req.Content, OperatorID: operatorID(r),
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
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := h.svc.ReviewQueue(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string][]service.KnowledgeRow{"edges": rows})
}

// handleConfirmEdge POST /api/v1/edges/{id}/confirm —— S2 治理
func (h *Handler) handleConfirmEdge(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.ConfirmEdge(r.PathValue("id"), operatorID(r)); err != nil {
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
	if err := h.svc.RejectEdge(r.PathValue("id"), operatorID(r)); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "edge not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "draft"})
}
