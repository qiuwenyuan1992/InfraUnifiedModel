package router

import (
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/service"
	"context"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundInventoryStub struct{ service.InventoryRoundService }

func (s *roundInventoryStub) Enqueue(_ context.Context, _, _ string, _ service.EnqueueInventoryRound) (*model.SyncRound, error) {
	return &model.SyncRound{ID: strings.Repeat("b", 32), Status: "queued"}, nil
}
func (s *roundInventoryStub) List(_ context.Context, _ string, _ service.InventoryQuery) (*service.InventoryPage, error) {
	return &service.InventoryPage{Items: []model.SyncRound{}}, nil
}
func (s *roundInventoryStub) Get(_ context.Context, _, id string) (*model.SyncRound, []model.SyncRoundPhase, error) {
	return &model.SyncRound{ID: id, Status: "running"}, []model.SyncRoundPhase{{Name: "device", Status: "succeeded"}}, nil
}
func (s *roundInventoryStub) Cancel(_ context.Context, _, id string) (*model.SyncRound, bool, error) {
	return &model.SyncRound{ID: id, Status: "running"}, true, nil
}
func (s *roundInventoryStub) Diagnostics(_ context.Context, _, id string, _ bool, _ service.InventoryQuery) (*service.InventoryPage, error) {
	return &service.InventoryPage{Items: []model.SyncDiagnostic{{ID: id, Code: "safe_code", Detail: "internal-secret"}}}, nil
}
func TestInventoryRoundRoutesRequireJWT(t *testing.T) {
	r, token := inventoryRouter(t, &inventoryStub{})
	parent := strings.Repeat("a", 32)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"POST", "/sync-rounds", `{"source_id":"` + parent + `","mode":"full"}`, 202}, {"GET", "/sync-rounds", "", 200}, {"GET", "/sync-rounds/" + parent, "", 200}, {"POST", "/sync-rounds/" + parent + "/cancel", "", 202}, {"GET", "/sync-rounds/" + parent + "/diagnostics", "", 200}, {"GET", "/sync-runs/" + parent + "/diagnostics", "", 200}} {
		for _, auth := range []bool{false, true} {
			req := httptest.NewRequest(tc.method, "/v1/inventory"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "key")
			if auth {
				req.Header.Set("Authorization", token)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			status := 401
			if auth {
				status = tc.status
			}
			require.Equal(t, status, w.Code, tc.path)
			require.NotContains(t, w.Body.String(), "internal-secret")
		}
	}
}
