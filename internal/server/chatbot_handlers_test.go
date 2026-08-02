package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onebox/internal/llm"
)

// TestChatbotSystemPromptLeadsWithDefaultSchema pins the prompt-engineering
// behavior change requested for the admin assistant: given a bare "create
// a collection" request, it must be instructed to lead with a concrete
// default schema instead of asking an open-ended "what fields would you
// like?" question. There's no real model call in this test suite to
// exercise the actual reply, so this is a content guard on the prompt
// itself — it fails loudly if a future edit accidentally drops the
// behavior, the worked "messages collection" example, or the "don't
// propose a redundant created_at field" guidance.
func TestChatbotSystemPromptLeadsWithDefaultSchema(t *testing.T) {
	mustContain := []string{
		"do not\nrespond by asking what fields they want",
		"messages collection",
		"sender_user_id (text, required",
		"receiver_user_id (text, required",
		"is_read (bool",
		"explicitly do NOT propose a\ncreated_at/timestamp field",
		"Only ask an open-ended question first when the entity is genuinely ambiguous",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(chatbotSystemPrompt, phrase) {
			t.Errorf("chatbotSystemPrompt missing expected guidance: %q", phrase)
		}
	}
}

// TestChatbotSystemPromptCoversSuperuserCopilotBehaviors is the broader
// content guard for the consolidated "AI Superuser Copilot" behavior
// spec: role framing, truthfulness/no-hallucinated-execution, and the
// approval-flow reply pattern must all survive future edits to the prompt.
//
// This prompt was deliberately shrunk from ~8,900 to under 2,500
// characters (see chatbotSystemPrompt's doc comment) as a performance
// change — the old intent-detection taxonomy and the "skip generic
// greetings" chat-style note were dropped rather than condensed, because
// they're now handled elsewhere instead of being lost: intent-gating for
// code/API examples is covered by the shorter "unless the admin explicitly
// asks" clause below, and real greetings never reach this prompt at all
// anymore — they're intercepted by the fast path (see
// TestLightweightGreetingSkipsFullPrompt) before chatbotSystemPrompt is
// even assembled. The budget was raised to under 2,900 when ATTACHMENTS
// and CHAT STYLE were added (proactive-attachment-analysis and
// engineer-not-documentation tone) — WORKSPACE AWARENESS and ENGINEERING
// MINDSET were tightened first to keep this a real new-capability cost
// rather than unchecked growth; see chatbotSystemPrompt's doc comment.
func TestChatbotSystemPromptCoversSuperuserCopilotBehaviors(t *testing.T) {
	mustContain := []string{
		"OneBox AI Superuser Copilot",
		"Do not behave like a general-purpose\nchatbot",
		"never a\nregular application user",
		"Recommendation", "Proposed Action", "Executed Action",
		"Never pretend the action happened",
		"OneBox doesn't\nsupport AI execution yet",
		"unless the admin explicitly asks for an API or code example",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(chatbotSystemPrompt, phrase) {
			t.Errorf("chatbotSystemPrompt missing expected guidance: %q", phrase)
		}
	}
	if len(chatbotSystemPrompt) >= 2900 {
		t.Errorf("chatbotSystemPrompt = %d characters, want under 2900 (performance target)", len(chatbotSystemPrompt))
	}
}

// TestChatbotSystemPromptAnalyzesAttachmentsProactively pins the "sound
// like ChatGPT/Cursor, not a documentation bot" behavior pass: an attached
// resume/screenshot/PDF/code file must be analyzed unprompted (summarize,
// explain, walk through) rather than answered with a bare "what would you
// like me to do with this?" — see the ATTACHMENTS section.
func TestChatbotSystemPromptAnalyzesAttachmentsProactively(t *testing.T) {
	mustContain := []string{
		"analyze an attached image/document yourself first",
		"never just ask what to do with it",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(chatbotSystemPrompt, phrase) {
			t.Errorf("chatbotSystemPrompt missing proactive-attachment-analysis guidance: %q", phrase)
		}
	}
}

// TestChatbotSystemPromptAvoidsDocumentationTone pins the tone half of the
// same behavior pass: no reflexive filler phrases, short replies, and
// follow-up questions only when actually needed — see the CHAT STYLE
// section.
func TestChatbotSystemPromptAvoidsDocumentationTone(t *testing.T) {
	mustContain := []string{
		"\"Would you\nlike...\", \"I recommend...\", \"This provides flexibility...\"",
		"Keep\nreplies short (5-12 lines, bullets OK)",
		"ask a\nfollow-up only when actually needed",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(chatbotSystemPrompt, phrase) {
			t.Errorf("chatbotSystemPrompt missing chat-style guidance: %q", phrase)
		}
	}
}

// TestPublicChatSystemPromptDoesNotProposeSchemas keeps the scoping
// decision honest: proactive schema design and the approval-flow behavior
// are admin-copilot behaviors for whoever's actually building on this
// instance, not something a public share-link visitor's prompt should
// encourage.
func TestPublicChatSystemPromptDoesNotProposeSchemas(t *testing.T) {
	if strings.Contains(publicChatSystemPrompt, "default schema") {
		t.Fatal("publicChatSystemPrompt must not include the proactive schema-design behavior — that's admin-only")
	}
	if strings.Contains(publicChatSystemPrompt, "Superuser Copilot") {
		t.Fatal("publicChatSystemPrompt must not adopt the admin Superuser Copilot persona")
	}
}

// TestChatbotDefaultsToOllamaWithNoModelChosen pins the fixed bug down at
// the HTTP layer: a fresh instance with no Chat Provider settings saved,
// and no Anthropic key configured anywhere, must NOT try Anthropic and
// fail with "invalid x-api-key" (the exact symptom reported). It should
// resolve to the new default — Ollama — and fail with a clear
// "choose a model in Settings" error instead, since no Ollama model has
// been picked yet either.
func TestChatbotDefaultsToOllamaWithNoModelChosen(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	if got := srv.providers.Load().chat.Provider; got != "ollama" {
		t.Fatalf("default chat provider = %q, want %q", got, "ollama")
	}

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "hi"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, "Settings") {
		t.Fatalf("expected an actionable error mentioning Settings, got %s", got)
	}
	if strings.Contains(rec.Body.String(), "x-api-key") || strings.Contains(rec.Body.String(), "anthropic") {
		t.Fatalf("chatbot must not attempt Anthropic when it isn't the configured provider: got %s", rec.Body.String())
	}
}

// TestChatbotSwitchingProviderChangesBackend is the admin-chatbot half of
// "switching Chat Provider changes the backend used": with two fake
// providers installed, only the one named by bundle.chat.Provider should
// ever see a request — flipping chat.Provider must flip which one that is,
// live, with no other change.
func TestChatbotSwitchingProviderChangesBackend(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	anthropicFake := &fakeLLMClient{}
	ollamaFake := &fakeLLMClient{}
	router := &llm.Router{Anthropic: anthropicFake, Ollama: ollamaFake}

	setChat := func(provider, model string) {
		bundle := *srv.providers.Load()
		bundle.llm = router
		bundle.chat = chatSelection{Provider: provider, Model: model}
		srv.providers.Store(&bundle)
	}

	setChat("anthropic", "claude-sonnet-5")
	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "hi"})
	if rec.Code != http.StatusOK {
		t.Fatalf("anthropic: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if anthropicFake.lastUser == "" || ollamaFake.lastUser != "" {
		t.Fatalf("chat_provider=anthropic should call the Anthropic backend only (anthropic saw %q, ollama saw %q)", anthropicFake.lastUser, ollamaFake.lastUser)
	}

	anthropicFake.lastUser, ollamaFake.lastUser = "", ""
	setChat("ollama", "llama3.2:3b")
	rec = doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "hi again"})
	if rec.Code != http.StatusOK {
		t.Fatalf("ollama: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if ollamaFake.lastUser == "" || anthropicFake.lastUser != "" {
		t.Fatalf("chat_provider=ollama should call the Ollama backend only (ollama saw %q, anthropic saw %q)", ollamaFake.lastUser, anthropicFake.lastUser)
	}
}

// TestChatbotIncludesWorkspaceContext is the end-to-end "workspace
// awareness" regression test: POSTing /api/chat with a context describing
// the collection currently open must get that collection's live schema
// into what the model actually receives — proving describeWorkspace is
// wired into the real request path, not just unit-tested in isolation.
func TestChatbotIncludesWorkspaceContext(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	createRec := doAuth(t, srv, http.MethodPost, "/api/collections", adminToken, createCollectionRequest{
		Name:   "messages",
		Schema: Schema{Fields: []Field{{Name: "sender", Type: FieldText, Required: true}}},
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create collection: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message: "add an email field",
		Context: workspaceContext{Page: "records", Collection: "messages"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fake.lastSystem, `collection "messages"`) || !strings.Contains(fake.lastSystem, "sender: text") {
		t.Fatalf("system prompt sent to the model is missing the open collection's live schema: %s", fake.lastSystem)
	}
}

// TestChatbotRepliesUseConversationHistory is the "conversation memory"
// regression test: prior turns sent in the request's history must be
// replayed to the model as actual message-list entries (so "make it
// unique" can resolve "it"), not silently dropped, and roles outside
// user/assistant must be ignored rather than forwarded.
func TestChatbotRepliesUseConversationHistory(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message: "make it unique",
		History: []chatHistoryTurn{
			{Role: "user", Content: "create a users collection"},
			{Role: "assistant", Content: "Done — created \"users\"."},
			{Role: "user", Content: "add an email field"},
			{Role: "assistant", Content: "Added \"email\" (text)."},
			{Role: "system", Content: "should never reach the model — not user/assistant"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	// 1 system (ours) + 4 history turns + 1 new user message = 6.
	if len(fake.lastMessages) != 6 {
		t.Fatalf("messages sent to model = %d, want 6 (got %+v)", len(fake.lastMessages), fake.lastMessages)
	}
	if fake.lastMessages[1].Role != "user" || fake.lastMessages[1].Content != "create a users collection" {
		t.Fatalf("first history turn not replayed correctly: %+v", fake.lastMessages[1])
	}
	if fake.lastMessages[len(fake.lastMessages)-1].Content != "make it unique" {
		t.Fatalf("current message must be last: %+v", fake.lastMessages[len(fake.lastMessages)-1])
	}
	for _, m := range fake.lastMessages {
		if m.Content == "should never reach the model — not user/assistant" {
			t.Fatal("a history turn with an unexpected role must be dropped, not forwarded to the provider")
		}
	}
}

// TestPublicChatDoesNotLeakAdminWorkspaceContext is the safety regression
// test for the admin-vs-public prompt split: the public share endpoint
// must always use the conservative prompt and must never let a caller
// (even one who knows the request shape) pull admin-only live data —
// record contents, request logs, provider configuration — into the
// model's system prompt via a forged `context` field.
func TestPublicChatDoesNotLeakAdminWorkspaceContext(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	enableRec := doAuth(t, srv, http.MethodPost, "/api/chat-share/enable", adminToken, nil)
	var share chatShareStatusResponse
	json.Unmarshal(enableRec.Body.Bytes(), &share)
	token := share.URL[len(share.URL)-32:]

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doJSON(t, srv, http.MethodPost, "/api/chat/"+token, chatbotRequest{
		Message: "hi",
		Context: workspaceContext{Page: "settings"}, // an attempt to smuggle in admin-only context
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(fake.lastSystem, "Chat provider:") {
		t.Fatal("public chat must never receive admin-only workspace context (Settings provider info leaked)")
	}
	if strings.Contains(fake.lastSystem, "Superuser Copilot") {
		t.Fatal("public chat must use publicChatSystemPrompt, not the Superuser-copilot chatbotSystemPrompt")
	}
}

// TestChatbotIncludesCapabilitiesBlock is the end-to-end wiring check for
// the capabilities-injection architecture (chatbot_capabilities.go): the
// live-derived capabilities text must actually reach the model on a real
// request, not just exist as an unused function.
func TestChatbotIncludesCapabilitiesBlock(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	// Deliberately not "hi" — that now takes the lightweight-greeting fast
	// path (see isLightweightGreeting), which skips the capabilities block
	// entirely by design. This test asserts the capabilities-injection
	// wiring for a real question, which still gets the full treatment.
	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "what field types are supported?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fake.lastSystem, "CURRENT ONEBOX CAPABILITIES") {
		t.Fatalf("system prompt sent to the model is missing the capabilities block: %s", fake.lastSystem)
	}
	if !strings.Contains(fake.lastSystem, "bool, date, json, number, text") {
		t.Fatalf("system prompt missing the schema-engine-derived field-type list: %s", fake.lastSystem)
	}
}

// TestPublicChatIncludesCapabilitiesBlock: capabilities are product facts,
// not admin-sensitive data (unlike workspaceContext — see
// TestPublicChatDoesNotLeakAdminWorkspaceContext above), so the public
// share chat gets them too.
func TestPublicChatIncludesCapabilitiesBlock(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	enableRec := doAuth(t, srv, http.MethodPost, "/api/chat-share/enable", adminToken, nil)
	var share chatShareStatusResponse
	json.Unmarshal(enableRec.Body.Bytes(), &share)
	token := share.URL[len(share.URL)-32:]

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	// See the comment in TestChatbotIncludesCapabilitiesBlock — "hi" now
	// takes the lightweight-greeting fast path, which skips the
	// capabilities block by design.
	rec := doJSON(t, srv, http.MethodPost, "/api/chat/"+token, chatbotRequest{Message: "what can this instance do?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fake.lastSystem, "CURRENT ONEBOX CAPABILITIES") {
		t.Fatalf("public chat system prompt is missing the capabilities block: %s", fake.lastSystem)
	}
}

// TestIsLightweightGreeting pins the exact-match-only fast-path detector:
// plain pleasantries (any casing/punctuation) qualify, but anything with
// real question content — even if it starts with a greeting word — must
// not, since that would silently downgrade a real request to the tiny
// greeting prompt.
func TestIsLightweightGreeting(t *testing.T) {
	yes := []string{"hi", "Hi!", "HELLO", "hey.", "  thanks  ", "Thank you.", "good morning", "bye?"}
	for _, m := range yes {
		if !isLightweightGreeting(m) {
			t.Errorf("isLightweightGreeting(%q) = false, want true", m)
		}
	}
	no := []string{"hi, can you design a users collection for me", "hello world", "", "what fields does messages have?"}
	for _, m := range no {
		if isLightweightGreeting(m) {
			t.Errorf("isLightweightGreeting(%q) = true, want false", m)
		}
	}
}

// TestLightweightGreetingSkipsFullPrompt is the end-to-end regression test
// for the fast path (performance requirement — see isLightweightGreeting
// and greetingSystemPrompt): a plain "hi" must reach the model with the
// tiny greeting prompt only, never the full chatbotSystemPrompt or the
// capabilities block, and with no history/workspace padding.
func TestLightweightGreetingSkipsFullPrompt(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
		Message: "hi",
		Context: workspaceContext{Page: "collections", Collection: "messages"},
		History: []chatHistoryTurn{{Role: "user", Content: "earlier turn"}, {Role: "assistant", Content: "earlier reply"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(fake.lastSystem, "CURRENT ONEBOX CAPABILITIES") {
		t.Fatalf("greeting fast path must skip the capabilities block, got: %s", fake.lastSystem)
	}
	if strings.Contains(fake.lastSystem, "OneBox AI Superuser Copilot — an AI backend engineer living") {
		t.Fatalf("greeting fast path must not use the full chatbotSystemPrompt, got: %s", fake.lastSystem)
	}
	if len(fake.lastMessages) != 2 {
		t.Fatalf("greeting fast path must skip history, got %d messages: %+v", len(fake.lastMessages), fake.lastMessages)
	}
}

// TestLightweightGreetingUsesMidConversationPromptWhenHistoryPresent pins
// the fix for a real observed failure mode: a bare "thanks"/"hi" mid-
// conversation was reaching the model with the exact same
// fresh-introduction greetingSystemPrompt as turn 1, since the fast path
// (see isLightweightGreeting) never looked at whether history was empty —
// only at the message text. Deployed against a real model this produced a
// re-greeting ("Hi! I'm the OneBox AI Superuser Copilot...") in the middle
// of an active conversation. The fast path still deliberately skips
// sending the actual history content (performance — see
// TestLightweightGreetingSkipsFullPrompt), so the only thing history's
// presence should change is which of the two prompt variants is selected.
func TestLightweightGreetingUsesMidConversationPromptWhenHistoryPresent(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	t.Run("first message of a conversation", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{Message: "hi"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		if fake.lastSystem != greetingSystemPrompt {
			t.Fatalf("first-turn greeting must use greetingSystemPrompt, got: %s", fake.lastSystem)
		}
	})

	t.Run("mid-conversation pleasantry", func(t *testing.T) {
		rec := doAuth(t, srv, http.MethodPost, "/api/chat", adminToken, chatbotRequest{
			Message: "thanks",
			History: []chatHistoryTurn{{Role: "user", Content: "earlier turn"}, {Role: "assistant", Content: "earlier reply"}},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		if fake.lastSystem != midConversationGreetingSystemPrompt {
			t.Fatalf("mid-conversation pleasantry must use midConversationGreetingSystemPrompt, got: %s", fake.lastSystem)
		}
		if strings.Contains(fake.lastSystem, "administrator just sent a short\ngreeting") {
			t.Fatal("mid-conversation reply must not reuse the fresh-introduction greeting prompt")
		}
	})
}

// TestChatbotStreamingSendsDeltaEvents is the end-to-end regression test
// for item 3 of the performance pass (streaming): a caller that sends
// Accept: text/event-stream must get a text/event-stream response with at
// least one "delta" event and a final "done" event, instead of the plain
// single-JSON body. fakeLLMClient's ChatStream (see rag_handlers_test.go)
// delegates to Chat and fires onDelta once with the full fake reply, which
// is enough to prove the SSE plumbing itself — headers, event framing,
// flushing — works end to end.
func TestChatbotStreamingSendsDeltaEvents(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	req := jsonRequest(t, http.MethodPost, "/api/chat", chatbotRequest{Message: "what fields does this support?"})
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data: {"delta":`) {
		t.Fatalf("expected at least one delta event, got: %s", body)
	}
	if !strings.Contains(body, `data: {"done":true}`) {
		t.Fatalf("expected a final done event, got: %s", body)
	}
}

// TestChatbotStreamingGreetingFastPathStillStreams pins that the fast path
// (item 5) and streaming (item 3) compose — a greeting sent with Accept:
// text/event-stream must still get an SSE response, using the tiny
// greeting prompt, not the full one.
func TestChatbotStreamingGreetingFastPathStillStreams(t *testing.T) {
	srv, _ := newTestServer(t)
	adminToken := bootstrapAdmin(t, srv)

	fake := &fakeLLMClient{}
	bundle := *srv.providers.Load()
	bundle.llm = &llm.Router{Anthropic: fake}
	bundle.chat = chatSelection{Provider: "anthropic", Model: "claude-sonnet-5"}
	srv.providers.Store(&bundle)

	req := jsonRequest(t, http.MethodPost, "/api/chat", chatbotRequest{Message: "hi"})
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data: {"done":true}`) {
		t.Fatalf("expected a final done event, got: %s", rec.Body.String())
	}
	if strings.Contains(fake.lastSystem, "CURRENT ONEBOX CAPABILITIES") {
		t.Fatalf("streaming greeting fast path must still skip the capabilities block, got: %s", fake.lastSystem)
	}
}

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
