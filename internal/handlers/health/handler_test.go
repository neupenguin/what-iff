package health

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"
)

func TestCheck_HealthyDatabase(t *testing.T) {
	// Create mock database
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	// Expect ping to succeed
	mock.ExpectPing()

	logger := zap.NewNop()
	handler := NewHandler(db, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	handler.Check(rec, req)

	// Verify status code
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	// Verify response body
	var response Response
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "healthy" {
		t.Errorf("Expected status 'healthy', got '%s'", response.Status)
	}

	if response.Checks["database"] != "ok" {
		t.Errorf("Expected database check 'ok', got '%s'", response.Checks["database"])
	}

	// Verify all expectations met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestCheck_UnhealthyDatabase(t *testing.T) {
	// Create mock database
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	// Expect ping to fail
	mock.ExpectPing().WillReturnError(sql.ErrConnDone)

	logger := zap.NewNop()
	handler := NewHandler(db, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	handler.Check(rec, req)

	// Verify status code
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}

	// Verify response body
	var response Response
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "unhealthy" {
		t.Errorf("Expected status 'unhealthy', got '%s'", response.Status)
	}

	if response.Checks["database"] != "error" {
		t.Errorf("Expected database check 'error', got '%s'", response.Checks["database"])
	}

	// Verify all expectations met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestCheck_NilDatabase(t *testing.T) {
	logger := zap.NewNop()
	handler := NewHandler(nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	handler.Check(rec, req)

	// Verify status code
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}

	// Verify response body
	var response Response
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "unhealthy" {
		t.Errorf("Expected status 'unhealthy', got '%s'", response.Status)
	}

	if response.Checks["database"] != "error" {
		t.Errorf("Expected database check 'error', got '%s'", response.Checks["database"])
	}
}

func TestCheck_ContentTypeJSON(t *testing.T) {
	// Create mock database
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	mock.ExpectPing()

	logger := zap.NewNop()
	handler := NewHandler(db, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	handler.Check(rec, req)

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
	}
}

func TestLive_NilDatabase(t *testing.T) {
	handler := NewHandler(nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/health/live", nil)
	rec := httptest.NewRecorder()

	handler.Live(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var response Response
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if response.Status != "healthy" {
		t.Errorf("Expected status 'healthy', got '%s'", response.Status)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
	}
}

func TestLive_FailingDatabaseNotPinged(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	// A failing ping is armed; Live must neither consume it nor report it.
	mock.ExpectPing().WillReturnError(sql.ErrConnDone)

	handler := NewHandler(db, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/health/live", nil)
	rec := httptest.NewRecorder()

	handler.Live(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err == nil {
		t.Error("Expected the database not to be pinged, but the ping expectation was consumed")
	}
}

func TestReady(t *testing.T) {
	tests := []struct {
		name       string
		pingErr    error
		wantCode   int
		wantStatus string
		wantDB     string
	}{
		{name: "ping succeeds", wantCode: http.StatusOK, wantStatus: "healthy", wantDB: "ok"},
		{name: "ping fails", pingErr: sql.ErrConnDone, wantCode: http.StatusServiceUnavailable, wantStatus: "unhealthy", wantDB: "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			if err != nil {
				t.Fatalf("Failed to create mock: %v", err)
			}
			defer db.Close()

			mock.ExpectPing().WillReturnError(tt.pingErr)

			handler := NewHandler(db, zap.NewNop())

			req := httptest.NewRequest(http.MethodGet, "/api/health/ready", nil)
			rec := httptest.NewRecorder()

			handler.Ready(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("Expected status %d, got %d", tt.wantCode, rec.Code)
			}

			var response Response
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}
			if response.Status != tt.wantStatus {
				t.Errorf("Expected status '%s', got '%s'", tt.wantStatus, response.Status)
			}
			if response.Checks["database"] != tt.wantDB {
				t.Errorf("Expected database check '%s', got '%s'", tt.wantDB, response.Checks["database"])
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("Unfulfilled expectations: %v", err)
			}
		})
	}
}
