package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// waitForSubscriber polls until the hub has at least n connected clients,
// so tests don't race a record creation ahead of the SSE handshake.
func waitForSubscribers(t *testing.T, srv *Server, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.hub.clientCount() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d realtime subscriber(s)", n)
}

func TestRealtimeBroadcastsRecordChanges(t *testing.T) {
	srv, _ := newTestServer(t)
	rules := DefaultRules()
	rules.List, rules.View = RulePublic, RulePublic
	setupPostsCollection(t, srv, rules)

	httpSrv := httptest.NewServer(srv.Router())
	defer httpSrv.Close()

	req, err := http.NewRequest(http.MethodGet, httpSrv.URL+"/api/realtime", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect to /api/realtime: %v", err)
	}
	defer resp.Body.Close()

	events := make(chan realtimeEvent, 4)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var evt realtimeEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err == nil {
				events <- evt
			}
		}
	}()

	waitForSubscribers(t, srv, 1)

	_, token := signupUser(t, srv, "writer@example.com")
	createRec := doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", token, map[string]any{"title": "breaking news"})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create failed: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	select {
	case evt := <-events:
		if evt.Action != "create" {
			t.Fatalf("action = %q, want %q", evt.Action, "create")
		}
		if evt.Collection != "posts" {
			t.Fatalf("collection = %q, want %q", evt.Collection, "posts")
		}
		if evt.Record["title"] != "breaking news" {
			t.Fatalf("record title = %v, want %q", evt.Record["title"], "breaking news")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for realtime create event")
	}
}

func TestRealtimeRespectsOwnerRule(t *testing.T) {
	srv, _ := newTestServer(t)
	rules := DefaultRules()
	rules.View = RuleOwner
	setupPostsCollection(t, srv, rules)

	httpSrv := httptest.NewServer(srv.Router())
	defer httpSrv.Close()

	_, ownerToken := signupUser(t, srv, "owner@example.com")

	// A second, unrelated user subscribes to realtime; since the view rule
	// is owner-only, they must not receive the first user's create event.
	_, otherToken := signupUser(t, srv, "other@example.com")
	req, err := http.NewRequest(http.MethodGet, httpSrv.URL+"/api/realtime?token="+otherToken, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect to /api/realtime: %v", err)
	}
	defer resp.Body.Close()

	events := make(chan realtimeEvent, 4)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var evt realtimeEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err == nil {
				events <- evt
			}
		}
	}()

	waitForSubscribers(t, srv, 1)

	createRec := doAuth(t, srv, http.MethodPost, "/api/collections/posts/records", ownerToken, map[string]any{"title": "private note"})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create failed: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	select {
	case evt := <-events:
		t.Fatalf("expected no event visible to a non-owner subscriber, got %+v", evt)
	case <-time.After(300 * time.Millisecond):
		// expected: nothing arrived
	}
}
