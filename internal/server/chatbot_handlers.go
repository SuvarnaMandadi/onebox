package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"onebox/internal/llm"
)

// chatbotSystemPrompt grounds the admin chatbot in being an AI Superuser
// Copilot — a senior backend engineer working inside OneBox — rather than
// a generic chatbot, API-docs reciter, or help desk that answers a design
// question with another question when it already knows enough to make a
// call. It's organized into the same named sections the product spec for
// this behavior uses (ROLE, DEFAULT BEHAVIOR, WORKSPACE AWARENESS,
// COLLECTION DESIGN, TRUTHFULNESS, USER APPROVAL, ENGINEERING MINDSET,
// INTENT DETECTION, CHAT STYLE, FUTURE AI COWORKER) so each behavioral
// requirement maps to one findable block instead of being scattered prose.
//
// Capability facts (field types, access rule kinds, auto-added columns,
// what's NOT supported yet, whether AI execution is enabled) are
// deliberately NOT hardcoded here — see capabilities.describe() in
// chatbot_capabilities.go, which derives them from the schema engine's own
// validation tables and gets prepended to every request ahead of this
// prompt. That split matters: this prompt describes stable behavior that
// doesn't change when OneBox gains a feature, and the capabilities block
// describes the one thing that does — so a new field type or a shipped
// relations feature never requires editing this string.
//
// Only handleChatbot (the authenticated admin panel) uses this prompt.
// The public share link uses publicChatSystemPrompt instead — see its own
// doc comment for why that has to stay a different, more conservative
// persona; it does not get the proactive schema-design or approval-flow
// behavior below, since designing another admin's schema isn't a public
// visitor's call to make.
// chatbotSystemPrompt used to be ~8,900 characters (every behavioral rule
// spelled out with a worked example and its own named section) — cheap in
// engineering time to write, expensive in tokens to pay for on literally
// every request, including a bare "hi." It's trimmed to the permanent,
// always-true rules only (target: under 2,900 characters — raised from
// 2,500 to fit the ATTACHMENTS and CHAT STYLE sections added for the
// "sound like an engineer, not documentation" + "analyze attachments
// proactively" behavior pass; WORKSPACE AWARENESS and ENGINEERING MINDSET
// were tightened to claw back most of that space first, so this is a
// genuine new-capability cost, not scope creep); anything that was
// previously here purely as elaboration or a worked example either got
// compressed into the rule itself or was dropped as redundant with the
// dynamically-generated CURRENT ONEBOX CAPABILITIES block that already
// gets appended after this prompt (see currentCapabilities().describe() in
// chatbot_capabilities.go) and the page-scoped workspace section (see
// describeWorkspace in chatbot_context.go, which now only injects what's
// relevant to whatever page the admin is actually on instead of a fixed
// bundle of everything). See TestChatbotSystemPromptLeadsWithDefaultSchema
// and TestChatbotSystemPromptCoversSuperuserCopilotBehaviors for the exact
// behaviors this trimmed prompt is still required to preserve.
const chatbotSystemPrompt = `ROLE: you are the OneBox AI Superuser Copilot — an AI backend engineer living inside the
OneBox Admin Dashboard, not a generic AI assistant. Do not behave like a general-purpose
chatbot, API documentation, or a generic coding assistant — behave like a senior backend
engineer who already works here. You are always talking to a logged-in Superuser, never a
regular application user (who can't reach this surface at all). Stay UI-first — no
curl/SDK/code — unless the admin explicitly asks for an API or code example.

WORKSPACE AWARENESS: page/collection/record context and live capability facts are appended
below — use them instead of asking the admin to repeat what's already visible; if nothing
relevant is shown, ask rather than guess.

COLLECTION DESIGN: only propose a collection when actually asked for one, never unprompted
— when asked to create a collection without specified fields, do not
respond by asking what fields they want — design a complete default schema yourself (name,
each field's type and why it belongs, best-practice notes) and lead with it, then ask if
they'd like to customize it. Example only, not a template to reuse verbatim: a "messages collection"
request should get fields like message_text, sender_user_id (text, required), receiver_user_id (text, required), and
is_read (bool) proposed directly — explicitly do NOT propose a
created_at/timestamp field, since created/updated already exist on every collection for
free. Only ask an open-ended question first when the entity is genuinely ambiguous or
multiple designs are equally valid.

TRUTHFULNESS: you have no execution path today, so never say a collection was created, a
field was added, or a record changed unless it actually was. Every reply about a change is
exactly one of: a Recommendation (not yet asked to be built), a Proposed Action (restate
precisely what would be created/changed), or — not available today — an Executed Action.
Never pretend the action happened. If asked to "go ahead," say plainly that OneBox doesn't
support AI execution yet, restate the full proposed action, and give the concrete UI steps
to do it themselves now.

ENGINEERING MINDSET: recommend good practices and say why, briefly. Never describe an
unsupported feature (see the capabilities block) as configurable today — say it isn't there
yet and suggest a workaround with what OneBox has now.

ATTACHMENTS: analyze an attached image/document yourself first (a resume's experience, what
a screenshot shows, a PDF's contents, what code does) — never just ask what to do with it.

CHAT STYLE: talk like a senior engineer, not documentation — cut filler like "Would you
like...", "I recommend...", "This provides flexibility..."; state the call and why. Keep
replies short (5-12 lines, bullets OK), never repeat what's already covered, and ask a
follow-up only when actually needed.`

// greetingSystemPrompt backs the fast path for lightweight pleasantries
// (see isLightweightGreeting) — deliberately tiny, since a plain "hi" or
// "thanks" needs none of chatbotSystemPrompt's behavioral rules, the
// capabilities block, or any workspace context to answer well, and every
// one of those would otherwise cost real prompt-eval time for zero benefit
// on a message with no actual question in it.
const greetingSystemPrompt = `You are the OneBox AI Superuser Copilot, embedded in the OneBox admin dashboard. The
administrator just sent a short greeting or pleasantry with no real question in it. Reply
warmly in one short sentence, and invite them to ask about their collections, schema, or
backend if they'd like help.`

// publicGreetingSystemPrompt is greetingSystemPrompt's public-share
// counterpart, used when the fast path is reached via handlePublicChat.
// The fast path bypasses publicChatSystemPrompt entirely for a bare
// greeting (that's the performance win), which means the "no
// Superuser-copilot persona for an anonymous visitor" boundary
// publicChatSystemPrompt normally enforces has to be reasserted here too
// — see TestPublicChatDoesNotLeakAdminWorkspaceContext, which caught this
// leaking "Superuser Copilot"/"admin dashboard" framing to a public "hi"
// before this constant existed.
const publicGreetingSystemPrompt = `You are the assistant embedded in a onebox dashboard. The visitor just sent a short
greeting or pleasantry with no real question in it. Reply warmly in one short sentence,
and invite them to ask about this onebox instance if they'd like help.`

// midConversationGreetingSystemPrompt is greetingSystemPrompt's counterpart
// for a lightweight pleasantry ("thanks", "ok", "cool") that arrives in the
// middle of an already-active conversation rather than as its opening
// message. The fast path is deliberately history-less for performance (see
// answerFastPath), so without this distinction a "thanks" on turn 12 would
// get the exact same "Hi! ... how can I help" framing as turn 1 — read by
// an admin as the assistant repeating its greeting or forgetting the
// conversation mid-stream. Selected via answerFastPath's hasHistory param.
const midConversationGreetingSystemPrompt = `You are the OneBox AI Superuser Copilot, embedded in the OneBox admin dashboard, partway
through an ongoing conversation with the administrator. They just sent a short
acknowledgment or pleasantry (like "thanks" or "ok") with no real question in it. Reply
briefly and naturally in one short sentence — do NOT reintroduce yourself, do NOT say a
greeting like "Hi" or "Hello", and do NOT re-explain what you can help with, since all of
that was already covered earlier in this same conversation.`

// publicMidConversationGreetingSystemPrompt is midConversationGreetingSystemPrompt's
// public-share counterpart, same relationship as publicGreetingSystemPrompt
// has to greetingSystemPrompt.
const publicMidConversationGreetingSystemPrompt = `You are the assistant embedded in a onebox dashboard, partway through an ongoing
conversation with the visitor. They just sent a short acknowledgment or pleasantry with no
real question in it. Reply briefly and naturally in one short sentence — do NOT reintroduce
yourself or say a greeting, since that was already covered earlier in this conversation.`

// lightweightGreetings are exact (post-normalization) matches for
// isLightweightGreeting's fast path — deliberately a small, explicit list
// rather than a fuzzy/substring/prefix check, so a genuine question that
// happens to start with a greeting word ("hi, can you design a users
// collection for me") is never short-circuited into the tiny prompt above.
var lightweightGreetings = map[string]bool{
	"hi": true, "hello": true, "hey": true,
	"thanks": true, "thank you": true,
	"good morning": true, "good evening": true,
	"bye": true,
}

// isLightweightGreeting reports whether message is nothing more than a
// short greeting/pleasantry — trimmed, lowercased, and stripped of common
// trailing punctuation before matching, so "Hi!", "Hey.", "thanks!" etc.
// all still qualify for the fast path in answerChatbotQuestion.
// looksLikeLeakedToolCallJSON reports whether content is (almost) nothing
// but a bare {"name": ..., "parameters": ...}-shaped JSON object — the
// signature of a weak or small local model (llama3.2:3b over Ollama is the
// case that surfaced this) attempting a tool call by writing it out as
// plain text instead of using the provider's native structured
// tool-calling field. Showing that raw JSON to the admin as if it were a
// real answer is worse than showing nothing — it reads as the app being
// broken — so callers treat it the same as an empty reply: see its use in
// streamChatReply/writeNonStreamingReply.
func looksLikeLeakedToolCallJSON(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || trimmed[0] != '{' {
		return false
	}
	var probe struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
		Arguments  json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return false
	}
	return probe.Name != "" && (len(probe.Parameters) > 0 || len(probe.Arguments) > 0)
}

// replyHasNothingUsable reports whether a completed, error-free generation
// still amounts to nothing worth showing: no validated action AND either
// no text or text that's really a leaked tool-call attempt (see
// looksLikeLeakedToolCallJSON). A request can succeed at the HTTP/provider
// level and still land here — that's exactly the gap that let a "Hello,
// what can you help with?" turn silently render as a blank bubble with no
// visible sign anything went wrong.
func replyHasNothingUsable(content string, proposals []proposedAction) bool {
	if len(proposals) > 0 {
		return false
	}
	return strings.TrimSpace(content) == "" || looksLikeLeakedToolCallJSON(content)
}

func isLightweightGreeting(message string) bool {
	m := strings.ToLower(strings.TrimSpace(message))
	m = strings.TrimRight(m, "!.,? ")
	return lightweightGreetings[m]
}

// publicChatSystemPrompt backs the unauthenticated public share link
// (handlePublicChat) — deliberately a separate, more conservative prompt
// from chatbotSystemPrompt: no Superuser-copilot persona, no proactive
// schema design, no approval-flow behavior, since designing another
// admin's schema isn't a public visitor's call to make. Whoever holds the
// link isn't necessarily a Superuser (that's the entire point of the
// share feature), so this persona never receives workspaceContext (see
// describeWorkspace's doc comment) and stays scoped to what was already
// being shown here before the Superuser-copilot behavior existed: general
// orientation on this onebox instance and its API, using only the
// collection summary (already shown to any caller of this endpoint) —
// nothing from Settings, Logs, or individual record contents. It DOES
// still get the capabilities block (see answerChatbotQuestion) since
// that's just product facts, not instance-sensitive data.
const publicChatSystemPrompt = `You are the assistant embedded in a onebox dashboard. onebox is a single-binary
backend: dynamic collections (schema-defined tables with a REST CRUD API and realtime
subscriptions), email/password auth with per-collection access rules, file storage,
a RAG engine (ingest PDF/TXT/MD/DOCX, then /api/rag/query or /api/rag/answer for
grounded answers), and an LLM gateway (/api/llm/chat, routes to Anthropic/OpenAI/Ollama
by model name). Collection records are at /api/collections/:name/records (GET list,
POST create, GET/PATCH/DELETE /:id). Answer questions about this specific instance
using the live data summary below, and general questions about how to use onebox's
API concisely. If you don't know, say so — don't invent endpoints or data.`

type chatbotRequest struct {
	Message string `json:"message"`
	// Context is the dashboard's current page/collection/record — see
	// workspaceContext. Only meaningful (and only ever populated by the
	// frontend) for the authenticated admin panel; ignored in spirit for
	// the public share link since handlePublicChat never forwards it.
	Context workspaceContext `json:"context"`
	// History is this conversation's prior turns, oldest first, as kept by
	// the dashboard's chat widget — see chatHistoryTurn. Trimmed to the
	// most recent maxChatHistoryTurns before being replayed to the model.
	History []chatHistoryTurn `json:"history"`
	// AttachmentIDs are chat_attachment file IDs (see POST
	// /api/chat-attachments) the admin attached to THIS message —
	// resolved into image bytes / extracted document text by
	// resolveAttachments (chatbot_attachments.go). Omitted/empty for the
	// overwhelming majority of requests, which carry no attachments at
	// all; only handleChatbot ever forwards this — handlePublicChat
	// ignores it, same as it already ignores Context, since attachments
	// aren't supported on the unauthenticated public share link. A file
	// the admin dragged/@-mentioned in from the Files browser (rather
	// than freshly uploaded) rides in this same slice — see
	// resolveAttachments' fallback for a file with no _chat_attachments
	// row.
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
	// ContextRefs are collections/records the admin explicitly attached
	// via drag-and-drop or an @mention — see contextRefInput
	// (chatbot_context_refs.go) for why this is independent of Context
	// (workspaceContext only ever describes the current page). Admin-only,
	// same as AttachmentIDs.
	ContextRefs []contextRefInput `json:"context_refs,omitempty"`
	// ConversationExcerpts are #mentioned OTHER conversations' already-
	// extracted text — see conversationExcerptInput for why the backend
	// never looks these up itself. Admin-only, same as AttachmentIDs.
	ConversationExcerpts []conversationExcerptInput `json:"conversation_excerpts,omitempty"`
}

// chatbotResponse is the admin chatbot's reply. Actions is populated by
// actionParser (chatbot_actions.go) from whatever structured tool calls
// the model actually made this turn — never by inspecting Reply — and is
// omitted entirely when it made none. Nothing in this response, or
// anywhere upstream of it, ever executes an action — see proposedAction's
// doc comment for the still-manual approve/execute step this is one half
// of.
type chatbotResponse struct {
	Reply   string           `json:"reply"`
	Actions []proposedAction `json:"actions,omitempty"`
}

// handleChatbot is admin-only: the floating chat panel in the dashboard.
// This is the only caller that gets the Superuser-copilot prompt and live
// workspace context — see chatbotSystemPrompt and describeWorkspace.
func (s *Server) handleChatbot(w http.ResponseWriter, r *http.Request) {
	var req chatbotRequest
	// A message with at least one attachment and no typed text is a valid
	// "just look at this" turn (dropping an image with nothing else to
	// say is normal ChatGPT-style behavior) — only reject the request if
	// BOTH the text and the attachment list are empty.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (strings.TrimSpace(req.Message) == "" && len(req.AttachmentIDs) == 0) {
		writeError(w, http.StatusBadRequest, "invalid_body", `expected {"message": "..."} and/or at least one attachment_id`, nil)
		return
	}
	s.answerChatbotQuestion(w, r, chatbotSystemPrompt, req.Context, req.History, req.Message, req.AttachmentIDs, req.ContextRefs, req.ConversationExcerpts)
}

// handlePublicChat is unauthenticated by design — gated instead by
// possession of the share token, which an admin can revoke at any time
// by disabling or regenerating it (see handleChatShareStatus etc.). It
// deliberately never forwards a workspaceContext or conversation history
// from the request body — see publicChatSystemPrompt's doc comment for
// why an anonymous visitor must never get the admin-only live-data
// sections describeWorkspace can produce.
func (s *Server) handlePublicChat(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	stored, err := getAllSettings(r.Context(), s.db, s.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check chat availability", nil)
		return
	}
	if token == "" || stored[settingChatShareToken] == "" || stored[settingChatShareToken] != token {
		writeError(w, http.StatusNotFound, "not_found", "this chat link is invalid or has been revoked", nil)
		return
	}

	var req chatbotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", `expected {"message": "..."}`, nil)
		return
	}
	s.answerChatbotQuestion(w, r, publicChatSystemPrompt, workspaceContext{}, nil, req.Message, nil, nil, nil)
}

// answerChatbotQuestion is instrumented end-to-end (see the "CHAT REQUEST
// START"/"TOTAL" log pair it emits for every call) so a slow reply can be
// attributed to a specific stage instead of guessed at. This is diagnostic
// logging only — nothing here changes behavior, response shape, or timing
// on the happy path beyond the handful of time.Now() calls themselves
// (each cheap enough — a monotonic clock read — to not be the thing you'd
// ever see show up in the very numbers it's printing).
func (s *Server) answerChatbotQuestion(w http.ResponseWriter, r *http.Request, systemPrompt string, wc workspaceContext, history []chatHistoryTurn, message string, attachmentIDs []string, contextRefs []contextRefInput, conversationExcerpts []conversationExcerptInput) {
	requestStart := time.Now()
	log.Printf("========================\nCHAT REQUEST START\n========================")

	settingsStart := time.Now()
	bundle := s.providers.Load()
	settingsDur := time.Since(settingsStart)
	if bundle.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "no_provider", "no LLM provider configured — set one in Settings", nil)
		return
	}
	chat := bundle.chat
	if chat.Model == "" {
		writeError(w, http.StatusServiceUnavailable, "no_chat_model", "no chat model chosen — pick one for your Chat Provider in Settings", nil)
		return
	}

	// Fast path (performance): a bare greeting/pleasantry has no real
	// question in it, so it skips workspace loading, capability
	// generation, and conversation history entirely and goes out with a
	// tiny fixed prompt instead of chatbotSystemPrompt — see
	// greetingSystemPrompt and isLightweightGreeting. The provider/model
	// checks above still apply first, so a misconfigured instance still
	// reports that clearly instead of pretending a greeting "worked."
	// Gated on every extra-context slice being empty: an image/document,
	// dragged-in collection/record, or #mentioned conversation attached to
	// an otherwise-greeting-shaped message ("hi" + a screenshot) still
	// needs the full path so it actually gets resolved and sent — the
	// fast path has none of this machinery.
	if len(attachmentIDs) == 0 && len(contextRefs) == 0 && len(conversationExcerpts) == 0 && isLightweightGreeting(message) {
		s.answerFastPath(w, r, bundle, chat, message, requestStart, settingsDur, systemPrompt == publicChatSystemPrompt, len(history) > 0)
		return
	}

	// describeWorkspace is page-scoped (item 4 of the performance pass):
	// it now injects only what's relevant to whatever page the admin is
	// actually on — a collections-page request gets that one collection's
	// schema, a home-page request gets a collection count, a logs-page
	// request gets recent errors only, and so on — instead of the old
	// behavior of also unconditionally listing every collection and every
	// one of its fields on every single request regardless of page. It's a
	// no-op (returns "") for the public share path, since that caller
	// always passes the zero-value workspaceContext.
	workspaceStart := time.Now()
	workspace := s.describeWorkspace(r.Context(), wc)
	workspaceDur := time.Since(workspaceStart)

	// capabilities().describe() is safe for both callers — unlike
	// workspace, it's product facts (field types, rule kinds, what's not
	// supported yet), not instance-sensitive admin data. See
	// chatbot_capabilities.go for why this is derived from the schema
	// engine's own validation tables instead of hardcoded prompt prose.
	capsStart := time.Now()
	caps := currentCapabilities().describe()
	capsDur := time.Since(capsStart)

	assemblyStart := time.Now()
	// History cap: most recent 5 user/assistant exchanges (10 turns), down
	// from the previous 20 turns — see maxChatHistoryTurns' doc comment
	// for why this is a hard truncation rather than a summarization call
	// (summarizing would itself cost an extra model round trip, working
	// against the point of this change).
	if len(history) > maxChatHistoryTurns {
		history = history[len(history)-maxChatHistoryTurns:]
	}
	systemContent := systemPrompt + "\n\n" + caps + workspace
	messages := make([]llm.Message, 0, len(history)+2)
	messages = append(messages, llm.Message{Role: "system", Content: systemContent})
	historyChars, historyTurns := 0, 0
	for _, h := range history {
		if h.Role != "user" && h.Role != "assistant" {
			continue // drop anything malformed rather than forwarding an unexpected role to the provider
		}
		// Historical attachments get a filename-only note, never their
		// original bytes/text — see chatAttachmentRef's doc comment.
		content := h.Content + describeHistoryAttachments(h.Attachments)
		messages = append(messages, llm.Message{Role: h.Role, Content: content})
		historyChars += len(content)
		historyTurns++
	}

	// Attachment resolution (only ever non-trivial when the admin actually
	// attached something to THIS message — resolveAttachments returns the
	// zero value instantly for the common no-attachment case): images go
	// onto the outgoing llm.Message.Images, document text gets folded into
	// its Content, same as workspace/RAG context already is.
	var resolved resolvedAttachments
	var attachmentNote string
	if len(attachmentIDs) > 0 {
		visionCapable := llm.VisionCapable(chat.Provider, chat.Model)
		resolved = s.resolveAttachments(r.Context(), attachmentIDs, visionCapable)
		var imageRefs, docRefs int
		for _, ref := range resolved.Refs {
			if ref.Kind == "image" {
				imageRefs++
			} else {
				docRefs++
			}
		}
		attachmentNote = fmt.Sprintf(
			"Attachments: %d referenced (%d image(s), %d document(s)) — %d image(s) actually sent, %d extraction-text char(s) injected (capped at %d)",
			len(resolved.Refs), imageRefs, docRefs, len(resolved.Images), len(resolved.DocsText), maxAttachmentTextChars,
		)
	}
	// Explicit context refs (drag-and-drop / @mention) and #mentioned
	// conversation excerpts — independent of, and additive to, both the
	// page-scoped workspace context above and the attachment resolution
	// below. See contextRefInput/conversationExcerptInput's doc comments
	// (chatbot_context_refs.go) for why these are two different
	// resolution paths (one a DB lookup, one already-extracted client
	// text) despite ending up in the same place.
	contextRefsText := s.describeContextRefs(r.Context(), contextRefs)
	conversationExcerptsText := describeConversationExcerpts(conversationExcerpts)

	userText := message
	if strings.TrimSpace(userText) == "" && len(attachmentIDs) > 0 {
		userText = "(no message — see attached file(s))"
	}
	finalContent := userText + resolved.DocsText + resolved.Notes + contextRefsText + conversationExcerptsText
	messages = append(messages, llm.Message{Role: "user", Content: finalContent, Images: resolved.Images})
	assemblyDur := time.Since(assemblyStart)

	// Character counts below are measured straight off the actual strings
	// sent to the provider (systemContent, history contents, message) —
	// not recomputed separately — so this can't drift from what the model
	// really has to read.
	systemPromptChars := len(systemPrompt)
	capsChars := len(caps)
	workspaceChars := len(workspace)
	userChars := len(finalContent)
	totalChars := len(systemContent) + historyChars + userChars
	estimatedTokens := totalChars / 4 // rough: ~4 chars/token for English text; not model-specific

	log.Printf(
		"Settings loading: %s\n"+
			"Workspace context (page-scoped): %s\n"+
			"Capabilities generation: %s\n"+
			"Prompt assembly: %s\n"+
			"\n"+
			"Prompt size:\n"+
			"- System prompt:     %6d characters\n"+
			"- Capabilities:      %6d characters\n"+
			"- Workspace context: %6d characters\n"+
			"- History:           %6d characters (%d turn(s), capped at %d)\n"+
			"- User message:      %6d characters (includes any attached-document text/notes)\n"+
			"- TOTAL:             %6d characters\n"+
			"- Estimated tokens:  ~%d (rough: characters / 4, not model-specific)",
		settingsDur, workspaceDur, capsDur, assemblyDur,
		systemPromptChars, capsChars, workspaceChars,
		historyChars, historyTurns, maxChatHistoryTurns, userChars, totalChars, estimatedTokens,
	)
	if attachmentNote != "" {
		log.Printf("%s", attachmentNote)
	}

	wantsStream := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	if wantsStream {
		s.streamChatReply(w, r, bundle, chat, messages, requestStart, actionToolDefs)
		return
	}

	log.Printf("Sending request to %s (non-streaming)... (model=%s)", chat.Provider, chat.Model)
	llmStart := time.Now()
	result, err := bundle.llm.ChatWithProvider(r.Context(), chat.Provider, llm.ChatRequest{
		Model:    chat.Model,
		Messages: messages,
		Tools:    actionToolDefs,
	})
	llmDur := time.Since(llmStart)
	if err != nil {
		log.Printf("Chat request to %s FAILED after %s: %v", chat.Provider, llmDur, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "chat request failed: "+err.Error(), nil)
		return
	}

	parseStart := time.Now()
	resp := chatbotResponse{Reply: result.Content, Actions: s.validateProposals(r.Context(), actionParser.ParseActions(result))}
	parseDur := time.Since(parseStart)

	usageStart := time.Now()
	s.logUsage(r.Context(), chat.Provider, chat.Model, result.TokensIn, result.TokensOut, false)
	usageDur := time.Since(usageStart)

	renderStart := time.Now()
	writeJSON(w, http.StatusOK, resp)
	renderDur := time.Since(renderStart)

	// t.TotalDuration/TimeToFirstByte/RequestBuild are populated by
	// OllamaClient.Chat (see llm.ChatTiming's doc comment); Anthropic/
	// OpenAI don't populate them yet, so fall back to the black-box
	// llmDur measured above rather than print misleading zeros.
	t := result.Timing
	detailedTiming := t.TotalDuration > 0
	totalLLM, ttfb, reqBuild := llmDur, time.Duration(0), time.Duration(0)
	genNote := "(sub-request timing not instrumented for this provider — showing total call time only)"
	if detailedTiming {
		totalLLM, ttfb, reqBuild = t.TotalDuration, t.TimeToFirstByte, t.RequestBuild
		genNote = ""
		if chat.Provider == "ollama" {
			genNote = "(non-streaming: Ollama sends nothing until generation is fully done, so this interval IS the network + prompt-eval + generation time — see below)"
		}
	}
	postFirstByte := totalLLM - ttfb
	if postFirstByte < 0 {
		postFirstByte = 0
	}
	total := time.Since(requestStart)

	log.Printf(
		"Time until HTTP request sent (marshal + build request): %s\n"+
			"Time to first byte: %s %s\n"+
			"Generation time (time spent after the first byte, reading/decoding the rest of the body): %s\n"+
			"  -> total LLM call (send to fully decoded): %s\n"+
			"Parsing (building the chatbotResponse struct): %s\n"+
			"Usage logging (DB insert): %s\n"+
			"Rendering (JSON-encode + write the HTTP response — the actual chat-bubble render happens client-side in app.js and isn't measurable from the backend): %s\n"+
			"========================\n"+
			"TOTAL: %s\n"+
			"========================",
		reqBuild, ttfb, genNote, postFirstByte, totalLLM,
		parseDur, usageDur, renderDur, total,
	)
}

// answerFastPath is the lightweight-greeting branch of answerChatbotQuestion
// (see isLightweightGreeting) — same provider dispatch and usage logging as
// the full path, just with a tiny greeting prompt and no history, so a
// plain "hi" doesn't pay for context it doesn't need. isPublic selects
// which of the two greeting prompts to use (see publicGreetingSystemPrompt's
// doc comment for why this can't just always use greetingSystemPrompt) —
// the caller passes systemPrompt == publicChatSystemPrompt, since the fast
// path is reached from both handleChatbot and handlePublicChat. hasHistory
// selects the mid-conversation variant of whichever prompt (see
// midConversationGreetingSystemPrompt) so a "thanks" on turn 12 doesn't get
// the same fresh-introduction framing as turn 1 — the fast path skips
// loading the actual history either way, since only whether it's empty
// changes which prompt is used, not its content. It still honors the same
// streaming opt-in as the full path.
func (s *Server) answerFastPath(w http.ResponseWriter, r *http.Request, bundle *providerBundle, chat chatSelection, message string, requestStart time.Time, settingsDur time.Duration, isPublic bool, hasHistory bool) {
	prompt := greetingSystemPrompt
	switch {
	case isPublic && hasHistory:
		prompt = publicMidConversationGreetingSystemPrompt
	case isPublic:
		prompt = publicGreetingSystemPrompt
	case hasHistory:
		prompt = midConversationGreetingSystemPrompt
	}
	messages := []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: message},
	}
	totalChars := len(prompt) + len(message)
	log.Printf(
		"Settings loading: %s\n"+
			"Fast path: lightweight greeting detected — workspace, capabilities, and history all skipped.\n"+
			"Prompt size:\n"+
			"- System prompt (tiny greeting prompt): %d characters\n"+
			"- User message: %d characters\n"+
			"- TOTAL: %d characters\n"+
			"- Estimated tokens: ~%d",
		settingsDur, len(prompt), len(message), totalChars, totalChars/4,
	)

	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		// No tools offered here — a greeting/pleasantry (that's how the
		// fast path was reached — see isLightweightGreeting) never has an
		// operation to propose, so there's nothing for the model to call
		// even if it were offered one; skipping it keeps this path's
		// request as small as the rest of its "skip everything but the
		// tiny prompt" design.
		s.streamChatReply(w, r, bundle, chat, messages, requestStart, nil)
		return
	}

	llmStart := time.Now()
	result, err := bundle.llm.ChatWithProvider(r.Context(), chat.Provider, llm.ChatRequest{Model: chat.Model, Messages: messages})
	llmDur := time.Since(llmStart)
	if err != nil {
		log.Printf("Chat request to %s FAILED after %s: %v", chat.Provider, llmDur, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "chat request failed: "+err.Error(), nil)
		return
	}
	s.logUsage(r.Context(), chat.Provider, chat.Model, result.TokensIn, result.TokensOut, false)
	writeJSON(w, http.StatusOK, chatbotResponse{Reply: result.Content, Actions: s.validateProposals(r.Context(), actionParser.ParseActions(result))})
	log.Printf("========================\nTOTAL (fast path): %s\n========================", time.Since(requestStart))
}

// streamChatReply is the streaming branch shared by the full path and the
// greeting fast path (item 3 of the performance pass): it calls the
// existing ChatStreamWithProvider — already implemented for all three
// providers (see internal/llm) — and forwards each delta to the client as
// a server-sent event the instant it arrives, instead of buffering the
// whole reply. This is strictly opt-in (only reached when the caller sends
// Accept: text/event-stream) so every existing caller of POST /api/chat and
// POST /api/chat/{token} — including the public share page's plain
// fetch().then(r => r.json()) — keeps getting exactly the same single-JSON
// response it always has; the REST contract itself is unchanged, streaming
// is just available to callers that ask for it.
// streamDoneEvent is the final SSE event of a streamed reply — sent once
// the full turn is known, so actionParser can run against its ToolCalls
// just like the non-streaming path does. The frontend's Accept header
// always asks for streaming (see streamChatRequest in app.js), so this is
// the only place actions actually reach the dashboard in practice.
type streamDoneEvent struct {
	Done    bool             `json:"done"`
	Actions []proposedAction `json:"actions,omitempty"`
}

func (s *Server) streamChatReply(w http.ResponseWriter, r *http.Request, bundle *providerBundle, chat chatSelection, messages []llm.Message, requestStart time.Time, tools []llm.Tool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Shouldn't happen with net/http's server, but degrade to the
		// plain non-streaming response rather than hang if it ever does.
		s.writeNonStreamingReply(w, r, bundle, chat, messages, requestStart, tools)
		return
	}
	s.activeStreams.Add(1)
	defer s.activeStreams.Add(-1)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	writeEvent := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	// Reliability backstop: once WriteHeader(200) has been called above,
	// the outer middleware.Recoverer (server.go) can no longer turn a
	// panic into a proper 500 — chi's Timeout/Recoverer middleware wrap
	// the handler, not this in-flight SSE writer, and a second
	// WriteHeader call from up there is a silent no-op on an already-
	// started response. Without this, a panic anywhere below (a bug in
	// ActionParser, ProposalValidator, usage logging, ...) would end the
	// connection with no "done" and no "error" event — exactly the
	// "stream just stops" failure mode streamChatRequest (app.js) now
	// treats as an error, but only if something actually tells it to.
	// This is the something: best-effort, but on an already-flushed SSE
	// stream this really is the only thing left to try.
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("PANIC in streamChatReply (provider=%s model=%s): %v\n%s", chat.Provider, chat.Model, rec, debug.Stack())
			writeEvent(map[string]bool{"error": true})
		}
	}()

	llmStart := time.Now()
	var ttfb time.Duration
	gotFirstDelta := false
	deltaCount := 0
	onDelta := func(chunk string) {
		if !gotFirstDelta {
			ttfb = time.Since(llmStart)
			gotFirstDelta = true
		}
		deltaCount++
		writeEvent(map[string]string{"delta": chunk})
	}

	result, err := bundle.llm.ChatStreamWithProvider(r.Context(), chat.Provider, llm.ChatRequest{Model: chat.Model, Messages: messages, Tools: tools}, onDelta)
	if err != nil {
		// ctx.Err() distinguishes "the client hung up" (nothing more to
		// do, don't bother falling back or logging as if it were a real
		// provider failure) from every other failure mode (network error,
		// non-2xx, malformed response, ...), which all get the same
		// fallback-then-error treatment below regardless of cause — this
		// is deliberately not a switch over error types, since callers
		// (including a future provider) can't be expected to return a
		// closed set of sentinel errors.
		if ctxErr := r.Context().Err(); ctxErr != nil {
			// Still attempt to write the error event even though the
			// context is done: if this is the server's own 120s
			// middleware.Timeout firing (server.go) rather than the
			// client actually disconnecting, the connection is very much
			// still open and waiting — an explicit event here is a much
			// faster, cleaner signal than making the client wait for
			// streamChatRequest's "stream ended without done" fallback
			// detection to kick in. If the client really did hang up,
			// this write is a harmless no-op against a dead connection.
			log.Printf("stream from %s (model=%s) aborted after %d delta(s) and %s: %v (client disconnect or request-level timeout)",
				chat.Provider, chat.Model, deltaCount, time.Since(requestStart), ctxErr)
			writeEvent(map[string]bool{"error": true})
			return
		}
		if !gotFirstDelta {
			// Nothing shown to the client yet, so it's safe to fall back
			// to one non-streaming call rather than fail the whole turn
			// just because this provider's streaming path hiccuped —
			// "automatically fall back to the current non-streaming
			// implementation" per the performance requirements.
			log.Printf("stream from %s (model=%s) failed before any output after %s (%v) — falling back to non-streaming",
				chat.Provider, chat.Model, time.Since(llmStart), err)
			fallback, ferr := bundle.llm.ChatWithProvider(r.Context(), chat.Provider, llm.ChatRequest{Model: chat.Model, Messages: messages, Tools: tools})
			if ferr != nil {
				log.Printf("non-streaming fallback to %s (model=%s) also failed after %s: %v — sending error event so the client never treats this as a silent empty reply",
					chat.Provider, chat.Model, time.Since(requestStart), ferr)
				writeEvent(map[string]bool{"error": true})
				return
			}
			fallbackProposals := s.validateProposals(r.Context(), actionParser.ParseActions(fallback))
			s.logUsage(r.Context(), chat.Provider, chat.Model, fallback.TokensIn, fallback.TokensOut, false)
			if replyHasNothingUsable(fallback.Content, fallbackProposals) {
				log.Printf("non-streaming fallback to %s (model=%s) completed with no error but produced neither usable text nor a valid action after %s (content=%q) — sending error event instead of a silently empty/garbled reply",
					chat.Provider, chat.Model, time.Since(requestStart), fallback.Content)
				writeEvent(map[string]bool{"error": true})
				return
			}
			writeEvent(map[string]string{"delta": fallback.Content})
			writeEvent(streamDoneEvent{Done: true, Actions: fallbackProposals})
			log.Printf("========================\nTOTAL (stream->fallback): %s\n========================", time.Since(requestStart))
			return
		}
		// Partial content already reached the client — never retry mid-
		// stream (that would duplicate what's already shown); send an
		// explicit error event so the client can tell "cut off mid-reply"
		// apart from "the model finished cleanly" (see streamChatRequest's
		// gotDone tracking in app.js) instead of silently accepting
		// whatever text arrived as if it were the complete answer.
		log.Printf("stream from %s (model=%s) failed after %d delta(s) and %s: %v",
			chat.Provider, chat.Model, deltaCount, time.Since(llmStart), err)
		writeEvent(map[string]bool{"error": true})
		return
	}

	proposals := s.validateProposals(r.Context(), actionParser.ParseActions(result))
	s.logUsage(r.Context(), chat.Provider, chat.Model, result.TokensIn, result.TokensOut, false)
	if replyHasNothingUsable(result.Content, proposals) {
		// The provider call itself succeeded (err == nil) but produced
		// nothing an admin could use — either truly empty, or (observed
		// with a small local Ollama model offered the schema-action tools
		// for an unrelated question) a bare tool-call-shaped JSON blob
		// leaked into plain content instead of a real answer or a native
		// tool call. Either way this must not render as a silently blank
		// "successful" bubble — see replyHasNothingUsable.
		log.Printf("stream from %s (model=%s) completed with no error but produced neither usable text nor a valid action after %d delta(s) and %s (content=%q) — sending error event instead of a silently empty/garbled reply",
			chat.Provider, chat.Model, deltaCount, time.Since(llmStart), result.Content)
		writeEvent(map[string]bool{"error": true})
		return
	}
	writeEvent(streamDoneEvent{Done: true, Actions: proposals})
	total := time.Since(requestStart)
	log.Printf(
		"Streaming reply: %d delta(s)\n"+
			"Time to first byte: %s\n"+
			"========================\n"+
			"TOTAL: %s\n"+
			"========================",
		deltaCount, ttfb, total,
	)
}

// writeNonStreamingReply is the plain, single-JSON-response path — the
// same shape answerChatbotQuestion used before streaming existed, factored
// out so streamChatReply can fall back to it if the ResponseWriter doesn't
// support flushing.
func (s *Server) writeNonStreamingReply(w http.ResponseWriter, r *http.Request, bundle *providerBundle, chat chatSelection, messages []llm.Message, requestStart time.Time, tools []llm.Tool) {
	result, err := bundle.llm.ChatWithProvider(r.Context(), chat.Provider, llm.ChatRequest{Model: chat.Model, Messages: messages, Tools: tools})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "chat request failed: "+err.Error(), nil)
		return
	}
	s.logUsage(r.Context(), chat.Provider, chat.Model, result.TokensIn, result.TokensOut, false)
	writeJSON(w, http.StatusOK, chatbotResponse{Reply: result.Content, Actions: s.validateProposals(r.Context(), actionParser.ParseActions(result))})
	log.Printf("========================\nTOTAL (non-flushable fallback): %s\n========================", time.Since(requestStart))
}

type chatShareStatusResponse struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

func chatShareStatus(r *http.Request, stored map[settingKey]string) chatShareStatusResponse {
	token := stored[settingChatShareToken]
	if token == "" {
		return chatShareStatusResponse{Enabled: false}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return chatShareStatusResponse{Enabled: true, URL: scheme + "://" + r.Host + "/chat/" + token}
}

// handleGetChatShare is admin-only: reports whether the public chat page
// is currently enabled, and its URL if so.
func (s *Server) handleGetChatShare(w http.ResponseWriter, r *http.Request) {
	stored, err := getAllSettings(r.Context(), s.db, s.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load chat-share status", nil)
		return
	}
	writeJSON(w, http.StatusOK, chatShareStatus(r, stored))
}

// handleEnableChatShare mints a fresh token (idempotent if already
// enabled — repeated calls don't invalidate an existing link; use
// /api/chat-share/regenerate to rotate it deliberately).
func (s *Server) handleEnableChatShare(w http.ResponseWriter, r *http.Request) {
	stored, err := getAllSettings(r.Context(), s.db, s.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load chat-share status", nil)
		return
	}
	if stored[settingChatShareToken] == "" {
		if err := regenerateChatShareToken(r.Context(), s); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to enable chat share", nil)
			return
		}
	}
	s.handleGetChatShare(w, r)
}

// handleDisableChatShare clears the token, immediately revoking the
// existing public link.
func (s *Server) handleDisableChatShare(w http.ResponseWriter, r *http.Request) {
	if err := setSetting(r.Context(), s.db, s.cfg.JWTSecret, settingChatShareToken, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to disable chat share", nil)
		return
	}
	writeJSON(w, http.StatusOK, chatShareStatusResponse{Enabled: false})
}

func (s *Server) handleRegenerateChatShare(w http.ResponseWriter, r *http.Request) {
	if err := regenerateChatShareToken(r.Context(), s); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to regenerate chat share token", nil)
		return
	}
	s.handleGetChatShare(w, r)
}

func regenerateChatShareToken(ctx context.Context, s *Server) error {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	return setSetting(ctx, s.db, s.cfg.JWTSecret, settingChatShareToken, hex.EncodeToString(buf))
}

// publicChatPageHTML is a minimal, dependency-free standalone page (not
// part of the dashboard SPA) a developer can hyperlink to directly —
// "no frontend code needed" means exactly this: it's already a complete
// page, just needs a link.
const publicChatPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Chat</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 560px; margin: 2rem auto; padding: 0 1rem; background: #f9f9f7; color: #0b0b0b; }
  #log { min-height: 300px; border: 1px solid #e1e0d9; border-radius: 10px; padding: 1rem; margin-bottom: 1rem; background: #fff; }
  .msg { margin-bottom: 0.75rem; white-space: pre-wrap; }
  .msg.user { font-weight: 600; }
  .msg.assistant { color: #333; }
  form { display: flex; gap: 0.5rem; }
  input { flex: 1; font: inherit; padding: 0.6rem 0.8rem; border: 1px solid #c3c2b7; border-radius: 8px; }
  button { font: inherit; padding: 0.6rem 1rem; border: none; border-radius: 8px; background: #2a78d6; color: #fff; cursor: pointer; }
  button:disabled { opacity: 0.6; }
</style>
</head>
<body>
<h2>Ask about this onebox instance</h2>
<div id="log"></div>
<form id="f">
  <input id="msg" type="text" placeholder="Ask a question…" autocomplete="off" />
  <button type="submit">Send</button>
</form>
<script>
  const log = document.getElementById("log");
  const form = document.getElementById("f");
  const input = document.getElementById("msg");
  const token = location.pathname.split("/").pop();

  function append(role, text) {
    const div = document.createElement("div");
    div.className = "msg " + role;
    div.textContent = (role === "user" ? "You: " : "") + text;
    log.appendChild(div);
    log.scrollTop = log.scrollHeight;
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const message = input.value.trim();
    if (!message) return;
    append("user", message);
    input.value = "";
    input.disabled = true;
    try {
      const res = await fetch("/api/chat/" + token, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ message }),
      });
      const body = await res.json();
      append("assistant", res.ok ? body.reply : (body.message || "Something went wrong."));
    } catch (err) {
      append("assistant", "Network error.");
    }
    input.disabled = false;
    input.focus();
  });
</script>
</body>
</html>`

func handlePublicChatPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(publicChatPageHTML))
}
