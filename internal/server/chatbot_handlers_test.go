package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestChatShareEnableDisableAndPublicGating(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	t.Run("non-admin cannot manage chat share", func(t *testing.T) {
		_, userToken := signupUser(t, srv, "notadmin@example.com")
		rec := doAuth(t, srv, http.MethodGet, "/api/chat-share", userToken, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	statusRec := doAuth(t, srv, http.MethodGet, "/api/chat-share", adminToken, nil)
	var status chatShareStatusResponse
	json.Unmarshal(statusRec.Body.Bytes(), &status)
	if status.Enabled {
		t.Fatalf("expected chat share disabled by default")
	}

	enableRec := doAuth(t, srv, http.MethodPost, "/api/chat-share/enable", adminToken, nil)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable: status = %d, body = %s", enableRec.Code, enableRec.Body.String())
	}
	var enabled chatShareStatusResponse
	json.Unmarshal(enableRec.Body.Bytes(), &enabled)
	if !enabled.Enabled || enabled.URL == "" {
		t.Fatalf("expected enabled with a URL, got %+v", enabled)
	}
	token := enabled.URL[len(enabled.URL)-32:]

	t.Run("wrong token rejected", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/chat/wrong-token", chatbotRequest{Message: "hi"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("correct token passes the gate (the call then fails for an unrelated reason: no model configured in tests)", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/chat/"+token, chatbotRequest{Message: "hi"})
		if rec.Code == http.StatusNotFound {
			t.Fatalf("a valid token must not be rejected as not_found, body = %s", rec.Body.String())
		}
	})

	regenRec := doAuth(t, srv, http.MethodPost, "/api/chat-share/regenerate", adminToken, nil)
	var regen chatShareStatusResponse
	json.Unmarshal(regenRec.Body.Bytes(), &regen)
	if regen.URL == enabled.URL {
		t.Fatalf("expected regenerate to produce a different URL/token")
	}

	t.Run("old token no longer works after regenerate", func(t *testing.T) {
		rec := doJSON(t, srv, http.MethodPost, "/api/chat/"+token, chatbotRequest{Message: "hi"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
		}
	})

	disableRec := doAuth(t, srv, http.MethodPost, "/api/chat-share/disable", adminToken, nil)
	var disabled chatShareStatusResponse
	json.Unmarshal(disableRec.Body.Bytes(), &disabled)
	if disabled.Enabled {
		t.Fatalf("expected disabled after /disable")
	}
}

func TestPublicChatPageServesHTML(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doAuth(t, srv, http.MethodGet, "/chat/anything", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("expected a Content-Type header")
	}
}
