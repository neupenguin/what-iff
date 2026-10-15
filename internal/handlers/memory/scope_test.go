package memory

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// The scope handler's own validation runs before any datastore access, so h.ds stays nil here;
// the move rules themselves are covered by datastore.TestMoveMemoryScope_*.

func TestMoveMemoryScopeUnauthorized(t *testing.T) {
	t.Parallel()

	h := &Handler{logger: zap.NewNop()}
	router := mux.NewRouter()
	h.RegisterRoutes(router)

	req := httptest.NewRequest(http.MethodPost, "/memory/"+uuid.New().String()+"/scope", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestMoveMemoryScopeHandler_BadRequests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		body string
	}{
		{name: "invalid memory id", id: "not-a-uuid", body: `{"level":"global"}`},
		{name: "invalid JSON body", id: uuid.New().String(), body: "not json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := &Handler{logger: zap.NewNop()}
			req := requestWithUserBody(t, http.MethodPost, "/memory/"+tc.id+"/scope", []byte(tc.body))
			req = mux.SetURLVars(req, map[string]string{"id": tc.id})
			rr := httptest.NewRecorder()
			h.MoveMemoryScope(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}
