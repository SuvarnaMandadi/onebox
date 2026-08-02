package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onebox/internal/llm"
)

// toolCall is a small test helper for building an llm.ToolCall with a
// map-literal argument set, saving every test below a json.Marshal call.
func toolCall(name string, args map[string]any) llm.ToolCall {
	b, _ := json.Marshal(args)
	return llm.ToolCall{ID: "call_test", Name: name, Arguments: b}
}

// TestNativeToolCallParserPerType is the table-driven pin for
// nativeToolCallParser: one structured ToolCall per action type must
// produce exactly one proposedAction of the expected Type/Destructive
// shape — built entirely from Arguments, never from any text.
func TestNativeToolCallParserPerType(t *testing.T) {
	cases := []struct {
		name      string
		call      llm.ToolCall
		wantType  string
		wantDestr bool
	}{
		{"create_collection", toolCall(actionCreateCollection, map[string]any{"name": "orders", "fields": []map[string]any{{"name": "total", "type": "number"}}}), actionCreateCollection, false},
		{"delete_collection", toolCall(actionDeleteCollection, map[string]any{"name": "orders"}), actionDeleteCollection, true},
		{"rename_collection", toolCall(actionRenameCollection, map[string]any{"from": "orders", "to": "purchases"}), actionRenameCollection, true},
		{"add_field", toolCall(actionAddField, map[string]any{"collection": "users", "field": "email", "type": "text"}), actionAddField, false},
		{"delete_field", toolCall(actionDeleteField, map[string]any{"collection": "users", "field": "email"}), actionDeleteField, true},
		{"import_data", toolCall(actionImportData, map[string]any{"collection": "orders"}), actionImportData, false},
		{"update_schema", toolCall(actionUpdateSchema, map[string]any{"collection": "orders", "summary": "widen total to number"}), actionUpdateSchema, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actions := nativeToolCallParser{}.ParseActions(llm.ChatResult{ToolCalls: []llm.ToolCall{tc.call}})
			if len(actions) != 1 {
				t.Fatalf("expected exactly 1 action, got %d: %+v", len(actions), actions)
			}
			a := actions[0]
			if a.Type != tc.wantType {
				t.Fatalf("Type = %q, want %q", a.Type, tc.wantType)
			}
			if a.Destructive != tc.wantDestr {
				t.Fatalf("Destructive = %v, want %v", a.Destructive, tc.wantDestr)
			}
			if a.ID == "" {
				t.Fatal("ID must never be empty")
			}
			if a.Title == "" {
				t.Fatal("Title must never be empty")
			}
			if a.Description == "" {
				t.Fatal("Description must never be empty")
			}
		})
	}
}

// TestNativeToolCallParserNoToolCallsProducesNoActions pins the "the
// model didn't call anything" case — the common one for a plain
// question — as a real, valid outcome (nil), never a guess.
func TestNativeToolCallParserNoToolCallsProducesNoActions(t *testing.T) {
	actions := nativeToolCallParser{}.ParseActions(llm.ChatResult{Content: "OneBox is a self-hosted backend-as-a-service."})
	if len(actions) != 0 {
		t.Fatalf("expected no actions, got %+v", actions)
	}
}

// TestNativeToolCallParserIgnoresUnknownToolName guards against a model
// hallucinating a tool it was never offered — dropped rather than
// producing a malformed action with no known title/destructive metadata.
func TestNativeToolCallParserIgnoresUnknownToolName(t *testing.T) {
	actions := nativeToolCallParser{}.ParseActions(llm.ChatResult{
		ToolCalls: []llm.ToolCall{toolCall("delete_everything", map[string]any{})},
	})
	if len(actions) != 0 {
		t.Fatalf("expected unknown tool name to be dropped, got %+v", actions)
	}
}

// TestNativeToolCallParserDropsCallsMissingRequiredArguments guards
// against a model calling a real tool but omitting a field its own
// schema declared required — dropped rather than proposing an action
// with an empty/zero-value field the admin never actually specified.
func TestNativeToolCallParserDropsCallsMissingRequiredArguments(t *testing.T) {
	cases := []llm.ToolCall{
		toolCall(actionCreateCollection, map[string]any{"fields": []map[string]any{}}), // missing name
		toolCall(actionRenameCollection, map[string]any{"from": "orders"}),              // missing to
		toolCall(actionAddField, map[string]any{"collection": "users"}),                 // missing field/type
		{ID: "call_bad", Name: actionDeleteCollection, Arguments: json.RawMessage(`not json`)},
	}
	for _, call := range cases {
		actions := nativeToolCallParser{}.ParseActions(llm.ChatResult{ToolCalls: []llm.ToolCall{call}})
		if len(actions) != 0 {
			t.Fatalf("expected call %+v to be dropped, got %+v", call, actions)
		}
	}
}

// TestNativeToolCallParserCreateCollectionPayloadShape pins the exact
// payload shape the frontend's renderActionCard depends on — payload.name
// and payload.fields[].name (see chatbot_actions.go's payload structs,
// which are chosen to match what the frontend already expects, so this
// milestone's frontend code didn't need to change).
func TestNativeToolCallParserCreateCollectionPayloadShape(t *testing.T) {
	call := toolCall(actionCreateCollection, map[string]any{
		"name": "messages",
		"fields": []map[string]any{
			{"name": "message_text", "type": "text"},
			{"name": "is_read", "type": "bool"},
		},
	})
	actions := nativeToolCallParser{}.ParseActions(llm.ChatResult{ToolCalls: []llm.ToolCall{call}})
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	payload, ok := actions[0].Payload.(createCollectionPayload)
	if !ok {
		t.Fatalf("Payload is not createCollectionPayload: %T %+v", actions[0].Payload, actions[0].Payload)
	}
	if payload.Name != "messages" {
		t.Fatalf("payload.Name = %q, want messages", payload.Name)
	}
	if len(payload.Fields) != 2 || payload.Fields[0].Name != "message_text" || payload.Fields[1].Name != "is_read" {
		t.Fatalf("unexpected fields: %+v", payload.Fields)
	}

	// Also pin the JSON wire shape directly, since that's what the
	// frontend actually consumes.
	b, err := json.Marshal(actions[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	json.Unmarshal(b, &decoded)
	payloadMap, ok := decoded["payload"].(map[string]any)
	if !ok {
		t.Fatalf("serialized payload is not an object: %s", b)
	}
	if payloadMap["name"] != "messages" {
		t.Fatalf("serialized payload.name = %v, want messages: %s", payloadMap["name"], b)
	}
	fields, ok := payloadMap["fields"].([]any)
	if !ok || len(fields) != 2 {
		t.Fatalf("serialized payload.fields unexpected: %s", b)
	}
}

// TestProposedActionSerializesExpectedShape pins the wire shape the
// frontend's renderActionCard depends on — id/type/title/description/
// payload/destructive, with payload omitted only when genuinely empty.
func TestProposedActionSerializesExpectedShape(t *testing.T) {
	a := proposedAction{
		ID: "act_abc123", Type: actionCreateCollection, Title: "Create Collection",
		Description: `Create collection "messages" with 1 field(s): body`,
		Payload:     createCollectionPayload{Name: "messages", Fields: []proposedField{{Name: "body", Type: "text"}}},
		Destructive: false,
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"id", "type", "title", "description", "payload", "destructive"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("serialized action missing key %q: %s", key, b)
		}
	}
}

// TestActionToolDefsCoverAllActionTypes guards against actionToolDefs
// and actionMeta drifting apart — every tool offered to the model must
// have metadata to build a proposedAction from, and vice versa.
func TestActionToolDefsCoverAllActionTypes(t *testing.T) {
	offered := map[string]bool{}
	for _, tool := range actionToolDefs {
		offered[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description — the model has no other signal for when to call it", tool.Name)
		}
		if len(tool.Schema) == 0 {
			t.Errorf("tool %q has no schema", tool.Name)
		}
		if _, ok := actionMeta[tool.Name]; !ok {
			t.Errorf("tool %q has no actionMeta entry", tool.Name)
		}
	}
	for typ := range actionMeta {
		if !offered[typ] {
			t.Errorf("actionMeta has %q but it's never offered as a tool", typ)
		}
	}
}

// TestCompatNormalizingParserUnwrapsStringifiedNestedArguments reproduces
// the exact live quirk found against Ollama 0.32.1 + llama3.2:3b — see
// chatbot_actions_compat.go — and pins that the wrapped parser still
// produces a correct, fully-populated create_collection action from it,
// while a plain nativeToolCallParser (no compat wrapper) would drop the
// same call as schema-invalid.
func TestCompatNormalizingParserUnwrapsStringifiedNestedArguments(t *testing.T) {
	quirky := llm.ToolCall{
		Name:      actionCreateCollection,
		Arguments: json.RawMessage(`{"name":"notes","fields":"[{\"name\": \"body\", \"type\": \"text\"}]"}`),
	}

	if actions := (nativeToolCallParser{}).ParseActions(llm.ChatResult{ToolCalls: []llm.ToolCall{quirky}}); len(actions) != 0 {
		t.Fatalf("expected the plain parser to drop the stringified-array call as schema-invalid, got %+v", actions)
	}

	actions := actionParser.ParseActions(llm.ChatResult{ToolCalls: []llm.ToolCall{quirky}})
	if len(actions) != 1 {
		t.Fatalf("expected 1 action from the compat-wrapped parser, got %d: %+v", len(actions), actions)
	}
	payload, ok := actions[0].Payload.(createCollectionPayload)
	if !ok {
		t.Fatalf("Payload is not createCollectionPayload: %T", actions[0].Payload)
	}
	if payload.Name != "notes" {
		t.Fatalf("payload.Name = %q, want notes", payload.Name)
	}
	if len(payload.Fields) != 1 || payload.Fields[0].Name != "body" || payload.Fields[0].Type != "text" {
		t.Fatalf("unexpected fields: %+v", payload.Fields)
	}
}

// TestNormalizeToolArgumentsLeavesCleanArgumentsUntouched guards against
// the compat shim corrupting the common case — every well-behaved
// provider's arguments (a native array, not a string) must pass through
// byte-for-byte unchanged.
func TestNormalizeToolArgumentsLeavesCleanArgumentsUntouched(t *testing.T) {
	clean := json.RawMessage(`{"name":"notes","fields":[{"name":"body","type":"text"}]}`)
	got := normalizeToolArguments(clean)
	if string(got) != string(clean) {
		t.Fatalf("clean arguments were altered: got %s, want %s", got, clean)
	}
}

// TestChatbotResponseIncludesActionsNonStreaming is the end-to-end pin for
// the non-streaming path: when the fake provider returns a structured
// tool call, the HTTP response's Actions field must be populated from it
// — and the Reply text is deliberately unrelated to the action, proving
// the two are independent (the action isn't reconstructed from Reply).
func TestChatbotResponseIncludesActionsNonStreaming(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{
		reply:     "Sounds good, let me know if you'd like anything else!",
		toolCalls: []llm.ToolCall{toolCall(actionCreateCollection, map[string]any{"name": "notes", "fields": []map[string]any{{"name": "body", "type": "text"}}})},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message: `Create a collection called "notes"`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp chatbotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Actions) != 1 {
		t.Fatalf("expected 1 action in response, got %d: %s", len(resp.Actions), rec.Body.String())
	}
	if resp.Actions[0].Type != actionCreateCollection {
		t.Fatalf("action type = %q, want %q", resp.Actions[0].Type, actionCreateCollection)
	}
	if !strings.Contains(resp.Actions[0].Description, "notes") {
		t.Fatalf("action description = %q, want it to mention the collection name from the tool call's arguments", resp.Actions[0].Description)
	}
	if resp.Reply != fake.reply {
		t.Fatalf("Reply must be exactly what the model said, independent of the action, got %q", resp.Reply)
	}
	if strings.Contains(resp.Reply, "notes") {
		t.Fatalf("test setup problem: Reply should NOT mention notes, so a passing test proves the action wasn't derived from Reply text")
	}
	if len(fake.lastTools) == 0 {
		t.Fatal("actionToolDefs must have been offered to the provider on the full chat path")
	}
}

// TestFastPathOffersNoTools pins that the lightweight-greeting fast path
// never offers actionToolDefs — a greeting has nothing to propose, and
// this keeps that path's request as small as its "skip everything but
// the tiny prompt" design already intends.
func TestFastPathOffersNoTools(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "hi"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if len(fake.lastTools) != 0 {
		t.Fatalf("expected no tools offered on the greeting fast path, got %+v", fake.lastTools)
	}
}

// TestChatbotStreamingDoneEventIncludesActions is the end-to-end pin for
// the streaming path — the one that actually matters in production, since
// the dashboard's chat widget always requests Accept: text/event-stream
// (see streamChatRequest in app.js). Actions must ride along on the final
// "done" SSE event, built from the fake provider's structured ToolCalls.
func TestChatbotStreamingDoneEventIncludesActions(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{
		reply:     "Sure, one moment.",
		toolCalls: []llm.ToolCall{toolCall(actionCreateCollection, map[string]any{"name": "notes", "fields": []map[string]any{{"name": "body", "type": "text"}}})},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	req := jsonRequest(t, http.MethodPost, "/api/chat", chatbotRequest{Message: `Create a collection called "notes"`})
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	var doneLine string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"done":true`) {
			doneLine = strings.TrimPrefix(line, "data: ")
		}
	}
	if doneLine == "" {
		t.Fatalf("no done event found in stream body: %s", body)
	}
	var evt streamDoneEvent
	if err := json.Unmarshal([]byte(doneLine), &evt); err != nil {
		t.Fatalf("decode done event: %v (line: %s)", err, doneLine)
	}
	if len(evt.Actions) != 1 {
		t.Fatalf("expected 1 action on the done event, got %d: %s", len(evt.Actions), doneLine)
	}
	if evt.Actions[0].Type != actionCreateCollection {
		t.Fatalf("action type = %q, want %q", evt.Actions[0].Type, actionCreateCollection)
	}
	if strings.Contains(body, "created") || strings.Contains(body, "Created") {
		t.Fatalf("streamed content must never claim the action completed: %s", body)
	}
}

// TestChatbotStreamingNoActionOmitsField pins the omitempty contract: a
// turn with no tool calls must produce a done event with no "actions"
// key at all, not an empty array.
func TestChatbotStreamingNoActionOmitsField(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{reply: "OneBox is a self-hosted backend-as-a-service."}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	req := jsonRequest(t, http.MethodPost, "/api/chat", chatbotRequest{Message: "What is OneBox?"})
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data: {"done":true}`) {
		t.Fatalf(`expected exactly {"done":true} with no actions key, got: %s`, rec.Body.String())
	}
}

// TestChatbotDropsInvalidProposalNonStreaming is the end-to-end pin for
// the validation stage on the non-streaming path: the fake model calls
// create_collection for a collection that already exists — a real
// ActionParser output, structurally perfect — and the HTTP response must
// still come back with no Actions, because ProposalValidator rejects it.
// The reply text is untouched either way, proving validation only ever
// filters Actions, never the transcript.
func TestChatbotDropsInvalidProposalNonStreaming(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	seedCollection(t, db, "notes", Field{Name: "body", Type: FieldText})

	fake := &fakeLLMClient{
		reply:     "Sure, here's a schema.",
		toolCalls: []llm.ToolCall{toolCall(actionCreateCollection, map[string]any{"name": "notes", "fields": []map[string]any{{"name": "body", "type": "text"}}})},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message: `Create a collection called "notes"`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp chatbotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Actions) != 0 {
		t.Fatalf("expected the already-exists proposal to be dropped by validation, got %+v", resp.Actions)
	}
	if resp.Reply != fake.reply {
		t.Fatalf("reply must still come through unaffected by a rejected proposal, got %q", resp.Reply)
	}
}

// TestChatbotDropsInvalidProposalStreaming is the streaming-path
// counterpart — the one that matters in production, since the dashboard
// always requests Accept: text/event-stream.
func TestChatbotDropsInvalidProposalStreaming(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	seedCollection(t, db, "notes", Field{Name: "body", Type: FieldText})

	fake := &fakeLLMClient{
		reply:     "Sure, here's a schema.",
		toolCalls: []llm.ToolCall{toolCall(actionCreateCollection, map[string]any{"name": "notes", "fields": []map[string]any{{"name": "body", "type": "text"}}})},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	req := jsonRequest(t, http.MethodPost, "/api/chat", chatbotRequest{Message: `Create a collection called "notes"`})
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data: {"done":true}`) {
		t.Fatalf(`expected a bare {"done":true} (no actions key) once the only proposal is rejected, got: %s`, rec.Body.String())
	}
}
