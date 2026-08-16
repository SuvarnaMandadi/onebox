package server

import (
	"context"
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
		toolCall(actionRenameCollection, map[string]any{"from": "orders"}),             // missing to
		toolCall(actionAddField, map[string]any{"collection": "users"}),                // missing field/type
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
		// RC4 regression: list_backups/get_recent_errors/get_settings_summary
		// shipped with actionToolDefs+actionMeta entries but no matching case
		// in schemaEngineValidator's switch (chatbot_proposal_validator.go)
		// — every call silently fell through to the "no validator
		// registered" default and got dropped before ever reaching
		// execution, caught only by TestChatbotGetSettingsSummaryToolReadsRealConfig
		// failing during development, not by this coverage test. Payload is
		// deliberately nil: a wrong-type payload for a REGISTERED type still
		// returns that type's own "wrong type" error before ever reaching
		// the switch's default branch — nil db is safe alongside it since
		// every validator checks the payload type before ever touching db
		// (see e.g. validateAddFieldProposal). All this needs to prove is
		// that the type has SOME case, not that nil happens to be a valid
		// proposal.
		err := schemaEngineValidator{}.Validate(context.Background(), nil, proposedAction{Type: tool.Name})
		if err != nil && strings.Contains(err.Error(), "no validator registered") {
			t.Errorf("tool %q has no case in schemaEngineValidator's switch — see chatbot_proposal_validator.go", tool.Name)
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

// TestChatbotAutoExecutesSafeActionNonStreaming is the end-to-end pin for
// the AI-execution milestone on the non-streaming path: create_collection
// is a safe, non-destructive action with a real execution primitive (see
// autoExecutable in chatbot_tool_execution.go), so when the fake provider
// calls it, the backend must actually create the collection for real — not
// just propose it — feed the tool's result back to the model for a second
// round, and return THAT round's reply. Actions must come back empty (an
// executed action isn't something the admin needs to approve), and the
// database must show the collection really exists — this is what
// distinguishes "executed" from the old propose-only architecture this
// test used to pin (see git history for the prior version, which asserted
// the opposite: Reply independent of Actions, nothing in Actions rendered
// as approved-vs-executed).
func TestChatbotAutoExecutesSafeActionNonStreaming(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Sure, creating that now.",
			"Done — I've created the notes collection with a body field.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionCreateCollection, map[string]any{"name": "notes", "fields": []map[string]any{{"name": "body", "type": "text"}}})},
		},
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
		t.Fatalf("expected no actions in response — create_collection auto-executed, nothing left to approve — got %d: %s", len(resp.Actions), rec.Body.String())
	}
	if resp.Reply != fake.roundReplies[1] {
		t.Fatalf("Reply = %q, want round 2's synthesized content %q", resp.Reply, fake.roundReplies[1])
	}
	if fake.callCount != 2 {
		t.Fatalf("provider call count = %d, want exactly 2 (execute, then synthesize)", fake.callCount)
	}
	if len(fake.lastTools) == 0 {
		t.Fatal("actionToolDefs must have been offered to the provider on the full chat path")
	}

	c, err := getCollectionByName(context.Background(), db, "notes")
	if err != nil {
		t.Fatalf("collection %q must actually exist after auto-execution: %v", "notes", err)
	}
	if len(c.Schema.Fields) != 1 || c.Schema.Fields[0].Name != "body" {
		t.Fatalf("collection %q has unexpected fields: %+v", "notes", c.Schema.Fields)
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
// (see streamChatRequest in app.js). This is the streaming counterpart of
// TestChatbotAutoExecutesSafeActionNonStreaming: create_collection auto-
// executes for real, round 1's preamble is never mistaken for the final
// answer, and only round 2's synthesized content — never raw tool-call
// JSON — reaches the client as the final "delta" event ahead of "done".
func TestChatbotAutoExecutesSafeActionStreaming(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Sure, one moment.",
			"Done — I've created the notes collection with a body field.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionCreateCollection, map[string]any{"name": "notes", "fields": []map[string]any{{"name": "body", "type": "text"}}})},
		},
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
	var deltas []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if strings.Contains(payload, `"done":true`) {
			doneLine = payload
			continue
		}
		var d struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &d); err == nil && d.Delta != "" {
			deltas = append(deltas, d.Delta)
		}
	}
	if doneLine == "" {
		t.Fatalf("no done event found in stream body: %s", body)
	}
	var evt streamDoneEvent
	if err := json.Unmarshal([]byte(doneLine), &evt); err != nil {
		t.Fatalf("decode done event: %v (line: %s)", err, doneLine)
	}
	if len(evt.Actions) != 0 {
		t.Fatalf("expected no actions on the done event — create_collection auto-executed, got %d: %s", len(evt.Actions), doneLine)
	}

	// The client must see round 2's synthesized content exactly once as
	// the final delta — never raw tool-call JSON, and never left claiming
	// nothing happened.
	if len(deltas) == 0 {
		t.Fatalf("expected at least one delta event, got none: %s", body)
	}
	finalDelta := deltas[len(deltas)-1]
	if finalDelta != fake.roundReplies[1] {
		t.Fatalf("final delta = %q, want round 2's synthesized content %q", finalDelta, fake.roundReplies[1])
	}
	if strings.Contains(body, `"name":"`+actionCreateCollection+`"`) {
		t.Fatalf("stream body must never contain raw tool-call JSON: %s", body)
	}

	c, err := getCollectionByName(context.Background(), db, "notes")
	if err != nil {
		t.Fatalf("collection %q must actually exist after auto-execution: %v", "notes", err)
	}
	if len(c.Schema.Fields) != 1 || c.Schema.Fields[0].Name != "body" {
		t.Fatalf("collection %q has unexpected fields: %+v", "notes", c.Schema.Fields)
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

// TestChatbotDestructiveActionStaysProposedNotExecuted is the end-to-end
// pin for the other half of the safe/destructive split: delete_collection
// is Destructive (see actionMeta in chatbot_actions.go), so even though
// the proposal is fully valid (the collection really exists), it must
// never auto-execute — the collection must still exist afterward, the
// response's Actions must carry the proposal for the admin to confirm, and
// the loop must resolve in exactly one round (nothing was executed, so
// there's nothing new to report back to the model).
func TestChatbotDestructiveActionStaysProposedNotExecuted(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	seedCollection(t, db, "notes", Field{Name: "body", Type: FieldText})

	fake := &fakeLLMClient{
		reply:     "I'll get that deleted — please confirm.",
		toolCalls: []llm.ToolCall{toolCall(actionDeleteCollection, map[string]any{"name": "notes"})},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message: `Delete the notes collection`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp chatbotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != actionDeleteCollection {
		t.Fatalf("expected 1 delete_collection proposal in response, got %+v", resp.Actions)
	}
	if resp.Reply != fake.reply {
		t.Fatalf("Reply = %q, want the model's own (only) round content %q", resp.Reply, fake.reply)
	}
	if fake.callCount != 1 {
		t.Fatalf("provider call count = %d, want exactly 1 (nothing executed -> no second round)", fake.callCount)
	}

	if _, err := getCollectionByName(context.Background(), db, "notes"); err != nil {
		t.Fatalf("collection %q must still exist — delete_collection must never auto-execute: %v", "notes", err)
	}
}

// TestChatbotDescribeOneboxToolAnswersGeneralQuestion is the end-to-end pin
// for the exact regression this whole milestone traces back to: "What is
// OneBox?" must execute describe_onebox for real and answer with a
// synthesized natural-language reply — never the raw
// {"name":"describe_onebox",...} tool-call JSON the original bug report
// described for this exact prompt.
func TestChatbotDescribeOneboxToolAnswersGeneralQuestion(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Let me check.",
			"OneBox is a single-binary backend with dynamic collections, auth, files, RAG, and an LLM gateway.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionDescribeOnebox, map[string]any{})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "What is OneBox?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp chatbotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Reply != fake.roundReplies[1] {
		t.Fatalf("Reply = %q, want round 2's synthesized content %q — never the raw tool call", resp.Reply, fake.roundReplies[1])
	}
	if strings.Contains(resp.Reply, actionDescribeOnebox) {
		t.Fatalf("Reply must never contain the raw tool name/JSON: %q", resp.Reply)
	}
	if len(resp.Actions) != 0 {
		t.Fatalf("expected no actions — describe_onebox is read-only and auto-executed, got %+v", resp.Actions)
	}
}

// TestChatbotListCollectionsToolAnswersListRequest is the end-to-end pin
// for the second explicitly required verification prompt: "List my
// collections." must execute list_collections for real (reading the
// actual _collections registry) and answer with a synthesized reply that
// reflects the real data — not a guess from workspace context alone, and
// never raw tool-call JSON.
func TestChatbotListCollectionsToolAnswersListRequest(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	seedCollection(t, db, "notes", Field{Name: "body", Type: FieldText})
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber})

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Let me check.",
			"You have two collections: notes and orders.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionListCollections, map[string]any{})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "List my collections."})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp chatbotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Reply != fake.roundReplies[1] {
		t.Fatalf("Reply = %q, want round 2's synthesized content %q", resp.Reply, fake.roundReplies[1])
	}
	if len(resp.Actions) != 0 {
		t.Fatalf("expected no actions — list_collections is read-only and auto-executed, got %+v", resp.Actions)
	}

	// Confirm the tool result the model actually saw reflected the real
	// registry, not a guess — round 2's request must carry a "tool"
	// message mentioning both real collection names.
	if fake.callCount != 2 {
		t.Fatalf("provider call count = %d, want exactly 2", fake.callCount)
	}
	var sawToolResult bool
	for _, m := range fake.lastMessages {
		if m.Role == "tool" && strings.Contains(m.Content, "notes") && strings.Contains(m.Content, "orders") {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Fatalf("expected round 2's messages to include a tool result naming both real collections, got: %+v", fake.lastMessages)
	}
}

// TestChatbotListRecordsToolReadsRealData is the Milestone 5 pin:
// list_records must be auto-executed (read-only, safe) and the model's
// next round must actually see the real record's field values — not a
// guess, not a proposal card, proving the model can genuinely reason
// about record-level data instead of only schema.
func TestChatbotListRecordsToolReadsRealData(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber, Required: true})

	rec := doAuth(t, srv, http.MethodPost, "/api/collections/orders/records", adminToken, map[string]any{"total": 42})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed record: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Let me check the records.",
			"There is one order, totaling 42.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionListRecords, map[string]any{"collection": "orders"})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	resp := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "Do any orders look unusual?"})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", resp.Code, resp.Body.String())
	}
	var out chatbotResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Actions) != 0 {
		t.Fatalf("expected no proposal actions — list_records is read-only and auto-executed, got %+v", out.Actions)
	}
	if len(out.ExecutedActions) != 1 || out.ExecutedActions[0].Type != actionListRecords {
		t.Fatalf("expected exactly one executed_actions entry for list_records, got %+v", out.ExecutedActions)
	}

	var sawRealTotal bool
	for _, m := range fake.lastMessages {
		if m.Role == "tool" && strings.Contains(m.Content, "42") && strings.Contains(m.Content, "orders") {
			sawRealTotal = true
		}
	}
	if !sawRealTotal {
		t.Fatalf("expected round 2's messages to include the real record's total (42), got: %+v", fake.lastMessages)
	}
}

// TestChatbotListRecordsRejectsUnknownCollection confirms the proposal
// validator's collection-existence check applies to list_records same as
// every other collection-scoped tool — the model naming a collection
// that doesn't exist must not silently succeed or crash the loop.
func TestChatbotListRecordsRejectsUnknownCollection(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Let me check.",
			"That collection doesn't exist.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionListRecords, map[string]any{"collection": "ghost"})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	resp := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "What's in the ghost collection?"})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", resp.Code, resp.Body.String())
	}
	var out chatbotResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.ExecutedActions) != 0 {
		t.Fatalf("expected nothing executed for a nonexistent collection, got %+v", out.ExecutedActions)
	}
}

// TestRunToolLoopRetriesAfterAMalformedToolCall pins the RC2 fix to
// runToolLoop's !anyExecuted branch: previously, a round whose only tool
// call failed validation (bad collection name, malformed arguments, etc.)
// with no accompanying text returned immediately with the empty-content
// fallback message — even though the tool-result message explaining what
// went wrong had already been built, it was simply never sent back to the
// model. That's the exact "tool call didn't go through cleanly" symptom:
// the model never got a chance to read its own mistake and self-correct.
// This exercises runToolLoop directly (rather than through the /api/chat
// HTTP layer, like every other test in this file) specifically so round
// 0's Content can be forced truly empty — fakeLLMClient.Chat always
// substitutes "fake answer" for an empty roundReplies entry, which would
// mask this exact bug (a real provider does return empty Content for a
// pure tool-call response, per this project's own live-testing notes).
func TestRunToolLoopRetriesAfterAMalformedToolCall(t *testing.T) {
	srv, db := newTestServer(t)
	seedCollection(t, db, "orders", Field{Name: "total", Type: FieldNumber, Required: true})
	rec := doAuth(t, srv, http.MethodPost, "/api/collections/orders/records", bootstrapAdmin(t, srv), map[string]any{"total": 42})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed record: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Round 0 (handed in directly as round0Result, bypassing the fake
	// client) is a pure tool call with no text — the real-world shape a
	// truncated/malformed provider response takes — naming a collection
	// that doesn't exist. Round 1 (served by the fake client, its own
	// round index 0) is the self-correction: the same tool, now aimed at
	// the real "orders" collection. Round 2 is the model's final answer
	// once it can see the real data.
	fake := &fakeLLMClient{
		roundReplies: []string{
			"",
			"There is one order, totaling 42.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionListRecords, map[string]any{"collection": "orders"})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	round0 := &llm.ChatResult{
		Content:   "",
		ToolCalls: []llm.ToolCall{toolCall(actionListRecords, map[string]any{"collection": "ghost-collection"})},
	}
	messages := []llm.Message{{Role: "user", Content: "How many orders do we have?"}}
	out, err := srv.runToolLoop(context.Background(), &bundle, bundle.chat, messages, actionToolDefs, round0)
	if err != nil {
		t.Fatalf("runToolLoop: %v", err)
	}

	if out.Reply != "There is one order, totaling 42." {
		t.Fatalf("Reply = %q, want the model's real final answer after self-correcting — the loop must not give up after round 0's failed call", out.Reply)
	}
	if len(out.ExecutedActions) != 1 || out.ExecutedActions[0].Type != actionListRecords {
		t.Fatalf("expected exactly one executed_actions entry from the corrected round-1 call, got %+v", out.ExecutedActions)
	}

	// The tool-result message only names the failed tool, not the bad
	// argument value that caused strictUnmarshal to reject it (see
	// chatbot_tool_execution.go's !ok branch) — check for that, not for
	// "ghost-collection" appearing verbatim.
	var sawFailureFeedback bool
	for _, m := range fake.lastMessages {
		if m.Role == "tool" && strings.Contains(m.Content, actionListRecords) && strings.Contains(m.Content, "could not be validated") {
			sawFailureFeedback = true
		}
	}
	if !sawFailureFeedback {
		t.Fatalf("expected round 0's failure explanation to actually reach the model (not just be computed and discarded), got messages: %+v", fake.lastMessages)
	}
}

// TestChatbotCreateCollectionProposesRelationRequiredAndRules is the RC3
// pin: create_collection previously could only propose flat name+type
// fields with always-default Rules{} — the model had no channel to
// propose a relation field, a required flag, or access rules at all (see
// executeCreateCollection's old comment, now removed). This drives the
// exact same "design a CRM" shape a real admin request would produce —
// two collections, the second relating to the first, one field required,
// custom owner-scoped rules — through the real /api/chat auto-execute
// path and confirms every one of those actually landed in the created
// collection, not just the flat fields.
func TestChatbotCreateCollectionProposesRelationRequiredAndRules(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)
	seedCollection(t, db, "customers", Field{Name: "name", Type: FieldText, Required: true})

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Creating the orders collection now.",
			"Done — orders relates to customers, with total required and owner-only access.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionCreateCollection, map[string]any{
				"name": "orders",
				"fields": []map[string]any{
					{"name": "total", "type": "number", "required": true},
					{"name": "customer_id", "type": "relation", "relation_collection": "customers"},
				},
				"rules": map[string]any{"list": "owner", "view": "owner", "create": "authenticated", "update": "owner", "delete": "owner"},
			})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "Design a simple CRM: orders belonging to customers."})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp chatbotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Actions) != 0 {
		t.Fatalf("expected no pending actions — create_collection auto-executes — got %+v", resp.Actions)
	}

	created, err := getCollectionByName(context.Background(), db, "orders")
	if err != nil {
		t.Fatalf("orders was not actually created: %v", err)
	}
	if created.Rules.List != RuleOwner || created.Rules.Create != RuleAuthenticated {
		t.Fatalf("Rules = %+v, want the proposed owner/authenticated mix, not the flat default", created.Rules)
	}
	var total, customerID *Field
	for i, f := range created.Schema.Fields {
		switch f.Name {
		case "total":
			total = &created.Schema.Fields[i]
		case "customer_id":
			customerID = &created.Schema.Fields[i]
		}
	}
	if total == nil || !total.Required {
		t.Fatalf("total field = %+v, want Required = true", total)
	}
	if customerID == nil || customerID.Type != FieldRelation || customerID.RelationCollection != "customers" {
		t.Fatalf("customer_id field = %+v, want a relation to customers", customerID)
	}
}

// TestChatbotFindRelatedRecordsTraversesForwardAndReverse is the Milestone
// 6 end-to-end pin: given an order that relates to a customer,
// find_related_records must (a) resolve the forward relation — the order's
// customer_id field — to the real customer record, and (b) find the
// reverse reference — the same order showing up as a record in "orders"
// that points back at the customer — when asked from the customer's side.
// Both directions must reach the model as real record data, never a guess.
func TestChatbotFindRelatedRecordsTraversesForwardAndReverse(t *testing.T) {
	srv, db := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	seedCollection(t, db, "customers", Field{Name: "name", Type: FieldText, Required: true})
	seedCollection(t, db, "orders",
		Field{Name: "total", Type: FieldNumber, Required: true},
		Field{Name: "customer_id", Type: FieldRelation, RelationCollection: "customers"},
	)

	custRec := doAuth(t, srv, http.MethodPost, "/api/collections/customers/records", adminToken, map[string]any{"name": "Ada Lovelace"})
	if custRec.Code != http.StatusCreated {
		t.Fatalf("seed customer: status = %d, body = %s", custRec.Code, custRec.Body.String())
	}
	var cust map[string]any
	if err := json.Unmarshal(custRec.Body.Bytes(), &cust); err != nil {
		t.Fatalf("decode customer: %v", err)
	}
	customerID := cust["id"].(string)

	orderRec := doAuth(t, srv, http.MethodPost, "/api/collections/orders/records", adminToken, map[string]any{
		"total": 99, "customer_id": customerID,
	})
	if orderRec.Code != http.StatusCreated {
		t.Fatalf("seed order: status = %d, body = %s", orderRec.Code, orderRec.Body.String())
	}
	var order map[string]any
	if err := json.Unmarshal(orderRec.Body.Bytes(), &order); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	orderID := order["id"].(string)

	// Forward: starting from the order, find its customer.
	t.Run("forward", func(t *testing.T) {
		fake := &fakeLLMClient{
			roundReplies: []string{
				"Let me check.",
				"This order belongs to Ada Lovelace.",
			},
			roundToolCalls: [][]llm.ToolCall{
				{toolCall(actionFindRelatedRecords, map[string]any{"collection": "orders", "record_id": orderID})},
			},
		}
		bundle := *srv.providers.Load()
		bundle.llm = &llm.Router{Anthropic: fake}
		bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
		srv.providers.Store(&bundle)

		resp := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "Which customer placed this order?"})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", resp.Code, resp.Body.String())
		}
		var out chatbotResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(out.ExecutedActions) != 1 || out.ExecutedActions[0].Type != actionFindRelatedRecords {
			t.Fatalf("expected exactly one executed find_related_records, got %+v", out.ExecutedActions)
		}
		var sawCustomerName bool
		for _, m := range fake.lastMessages {
			if m.Role == "tool" && strings.Contains(m.Content, "Ada Lovelace") && strings.Contains(m.Content, "customers") {
				sawCustomerName = true
			}
		}
		if !sawCustomerName {
			t.Fatalf("expected the tool result to name the real related customer, got: %+v", fake.lastMessages)
		}
	})

	// Reverse: starting from the customer, find every order that references it.
	t.Run("reverse", func(t *testing.T) {
		fake := &fakeLLMClient{
			roundReplies: []string{
				"Let me check.",
				"Ada has one order, totaling 99.",
			},
			roundToolCalls: [][]llm.ToolCall{
				{toolCall(actionFindRelatedRecords, map[string]any{"collection": "customers", "record_id": customerID})},
			},
		}
		bundle := *srv.providers.Load()
		bundle.llm = &llm.Router{Anthropic: fake}
		bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
		srv.providers.Store(&bundle)

		resp := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "What orders has this customer placed?"})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", resp.Code, resp.Body.String())
		}
		var out chatbotResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(out.ExecutedActions) != 1 || out.ExecutedActions[0].Type != actionFindRelatedRecords {
			t.Fatalf("expected exactly one executed find_related_records, got %+v", out.ExecutedActions)
		}
		var sawReverseOrder bool
		for _, m := range fake.lastMessages {
			if m.Role == "tool" && strings.Contains(m.Content, "orders") && strings.Contains(m.Content, "99") {
				sawReverseOrder = true
			}
		}
		if !sawReverseOrder {
			t.Fatalf("expected the tool result to include the real order referencing this customer, got: %+v", fake.lastMessages)
		}
	})
}

// TestChatbotListBackupsToolReadsRealHistory is the RC4 pin for
// list_backups: the model must see the workspace's real backup history
// (filename, trigger, status), not a guess — this closes the gap where a
// question about backup health had no grounded tool to call at all before
// RC4. Uses newBackupTestServer since a real backup writes an actual .zip
// to disk (backupsDir), unlike most of this package's in-memory-only tests.
func TestChatbotListBackupsToolReadsRealHistory(t *testing.T) {
	srv, _ := newBackupTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	createRec := doAuth(t, srv, http.MethodPost, "/api/backups", adminToken, nil)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("seed backup: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var meta backupMeta
	if err := json.Unmarshal(createRec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode backup metadata: %v", err)
	}

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Let me check the backup history.",
			"You have one backup on file.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionListBackups, map[string]any{})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "Do we have any backups?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp chatbotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Actions) != 0 {
		t.Fatalf("expected no pending actions — list_backups is read-only and auto-executed, got %+v", resp.Actions)
	}
	var sawRealBackup bool
	for _, m := range fake.lastMessages {
		if m.Role == "tool" && strings.Contains(m.Content, meta.Filename) && strings.Contains(m.Content, "complete") {
			sawRealBackup = true
		}
	}
	if !sawRealBackup {
		t.Fatalf("expected round 2's messages to include the real backup's filename and status, got: %+v", fake.lastMessages)
	}
}

// TestChatbotGetRecentErrorsToolReadsRealLog is the RC4 pin for
// get_recent_errors: the model must see real failed requests from _logs
// (a genuine 404 from hitting a nonexistent collection here), not a guess.
func TestChatbotGetRecentErrorsToolReadsRealLog(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	notFound := doAuth(t, srv, http.MethodGet, "/api/collections/does-not-exist/records", adminToken, nil)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("seed a 404: status = %d, want 404, body = %s", notFound.Code, notFound.Body.String())
	}

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Let me check recent errors.",
			"There's one recent 404 on the records endpoint.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionGetRecentErrors, map[string]any{})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "Any recent errors?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var sawRealError bool
	for _, m := range fake.lastMessages {
		if m.Role == "tool" && strings.Contains(m.Content, "404") && strings.Contains(m.Content, "does-not-exist") {
			sawRealError = true
		}
	}
	if !sawRealError {
		t.Fatalf("expected round 2's messages to include the real 404, got: %+v", fake.lastMessages)
	}
}

// TestChatbotGetSettingsSummaryToolReadsRealConfig is the RC4 pin for
// get_settings_summary: the model must see the actual configured chat
// provider/model, reachable regardless of which dashboard page the admin
// is on (unlike describeWorkspace's settings-page-only equivalent).
func TestChatbotGetSettingsSummaryToolReadsRealConfig(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Let me check the settings.",
			"You're on Anthropic with claude-sonnet-5.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionGetSettingsSummary, map[string]any{})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "What chat provider are we using?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var sawRealConfig bool
	for _, m := range fake.lastMessages {
		if m.Role == "tool" && strings.Contains(m.Content, "anthropic") && strings.Contains(m.Content, "claude-sonnet-5") {
			sawRealConfig = true
		}
	}
	if !sawRealConfig {
		t.Fatalf("expected round 2's messages to include the real provider/model, got: %+v", fake.lastMessages)
	}
}

// TestSchemaAdviceSuggestsEmailFormatAndUnique is the direct unit pin for
// schemaAdvice (chatbot_tool_execution.go): an "email" text field with no
// validation at all should get both suggestions (format and unique) — the
// two independent, well-understood patterns it's designed to catch.
func TestSchemaAdviceSuggestsEmailFormatAndUnique(t *testing.T) {
	c := &collection{Schema: Schema{Fields: []Field{{Name: "email", Type: FieldText}}}}
	advice := schemaAdvice(c)
	if len(advice) != 2 {
		t.Fatalf("advice = %+v, want exactly 2 suggestions (format + unique)", advice)
	}
	joined := strings.Join(advice, " | ")
	if !strings.Contains(joined, "format") || !strings.Contains(joined, "unique") {
		t.Fatalf("advice = %+v, want both a format and a unique suggestion", advice)
	}
}

// TestSchemaAdviceStaysQuietWhenValidationAlreadySet confirms schemaAdvice
// never nags about a field that's already correctly configured — the
// low-noise guarantee its own doc comment promises.
func TestSchemaAdviceStaysQuietWhenValidationAlreadySet(t *testing.T) {
	c := &collection{Schema: Schema{Fields: []Field{
		{Name: "email", Type: FieldText, Validation: &FieldValidation{Format: "email", Unique: true}},
		{Name: "notes", Type: FieldText},
	}}}
	if advice := schemaAdvice(c); len(advice) != 0 {
		t.Fatalf("advice = %+v, want none — email is already fully validated and notes matches no pattern", advice)
	}
}

// TestChatbotCreateCollectionRelaysSchemaAdvice is the end-to-end pin: an
// AI-created collection with an unvalidated "email" field must carry a
// "Suggestion:" line in the tool result the model sees, so
// chatbotSystemPrompt's ENGINEERING MINDSET instruction has something real
// to relay — not just a unit-level check of schemaAdvice in isolation.
func TestChatbotCreateCollectionRelaysSchemaAdvice(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{
		roundReplies: []string{
			"Creating the subscribers collection now.",
			"Done — and you should add email format validation.",
		},
		roundToolCalls: [][]llm.ToolCall{
			{toolCall(actionCreateCollection, map[string]any{
				"name": "subscribers",
				"fields": []map[string]any{
					{"name": "email", "type": "text"},
				},
			})},
		},
	}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "Create a subscribers collection with an email field."})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var sawSuggestion bool
	for _, m := range fake.lastMessages {
		if m.Role == "tool" && strings.Contains(m.Content, "Suggestion:") && strings.Contains(m.Content, "email") {
			sawSuggestion = true
		}
	}
	if !sawSuggestion {
		t.Fatalf("expected the tool result to carry a Suggestion: line about the unvalidated email field, got: %+v", fake.lastMessages)
	}
}
