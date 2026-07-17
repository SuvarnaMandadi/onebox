package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"onebox/internal/config"
	"onebox/internal/db"
)

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		closeDB    bool
		wantStatus int
		wantCode   string
	}{
		{
			name:       "db reachable",
			closeDB:    false,
			wantStatus: http.StatusOK,
		},
		{
			name:       "db unreachable",
			closeDB:    true,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "db_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, err := db.Open(":memory:")
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			defer sqlDB.Close()

			if tt.closeDB {
				sqlDB.Close()
			}

			srv := New(config.Config{}, sqlDB)
			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			rec := httptest.NewRecorder()

			srv.Router().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCode != "" {
				var body errorEnvelope
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if body.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", body.Code, tt.wantCode)
				}
			}
		})
	}
}
