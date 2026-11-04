package handler

import (
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/service"
	"context"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundServiceStub struct {
	service.InventoryRoundService
	called bool
}

func (s *roundServiceStub) Enqueue(_ context.Context, _, _ string, _ service.EnqueueInventoryRound) (*model.SyncRound, error) {
	s.called = true
	return &model.SyncRound{ID: strings.Repeat("b", 32), Status: "queued", RequestHash: "secret"}, nil
}
func (s *roundServiceStub) Get(_ context.Context, _, id string) (*model.SyncRound, []model.SyncRoundPhase, error) {
	return &model.SyncRound{ID: id, Status: "queued"}, nil, nil
}
func TestInventoryRoundStrictBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`null`, `{}`, `{"source_id":"a","mode":"full","extra":1}`, `{"mode":"full","mode":"full"}`, `{"source_id":null,"mode":"full"}`, `{"source_id":"` + strings.Repeat("a", 32) + `","mode":"full"} {}`, `{"source_id":"` + strings.Repeat("a", 65536) + `","mode":"full"}`} {
		stub := &roundServiceStub{}
		h := NewInventoryRoundHandler(nil, stub)
		r := gin.New()
		r.POST("/", h.Enqueue)
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 || stub.called {
			t.Fatalf("body rejected incorrectly status=%d called=%v", w.Code, stub.called)
		}
	}
}
func TestInventoryRoundAcceptedLocation(t *testing.T) {
	stub := &roundServiceStub{}
	h := NewInventoryRoundHandler(nil, stub)
	r := gin.New()
	r.POST("/", h.Enqueue)
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"source_id":"`+strings.Repeat("a", 32)+`","mode":"full"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 202 || w.Header().Get("Location") != "/v1/inventory/sync-rounds/"+strings.Repeat("b", 32) || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
}
