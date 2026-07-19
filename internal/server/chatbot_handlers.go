package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"onebox/internal/llm"
)

// chatbotSystemPrompt grounds the admin chatbot (and its public share
// variant) in what onebox itself is and how its API works, so it can
// answer "how do I..." questions about the instance it's embedded in —
// not just generic chat.
const chatbotSystemPrompt = `You are the assistant embedded in a onebox dashboard. onebox is a single-binary
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
}

type chatbotResponse struct {
	Reply string `json:"reply"`
}

// handleChatbot is admin-only: the floating chat panel in the dashboard.
func (s *Server) handleChatbot(w http.ResponseWriter, r *http.Request) {
	var req chatbotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", `expected {"message": "..."}`, nil)
		return
	}
	s.answerChatbotQuestion(w, r, req.Message)
}

// handlePublicChat is unauthenticated by design — gated instead by
// possession of the share token, which an admin can revoke at any time
// by disabling or regenerating it (see handleChatShareStatus etc.).
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
	s.answerChatbotQuestion(w, r, req.Message)
}

func (s *Server) answerChatbotQuestion(w http.ResponseWriter, r *http.Request, message string) {
	llmRouter := s.providers.Load().llm
	if llmRouter == nil {
		writeError(w, http.StatusServiceUnavailable, "no_provider", "no LLM provider configured — set one in Settings", nil)
		return
	}

	collections, err := listCollections(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load collection summary", nil)
		return
	}
	var summary strings.Builder
	if len(collections) == 0 {
		summary.WriteString("This onebox instance has no collections yet.")
	} else {
		summary.WriteString("This onebox instance's collections:\n")
		for _, c := range collections {
			fmt.Fprintf(&summary, "- %q: %d record(s), fields: ", c.Name, c.RecordCount)
			names := make([]string, len(c.Schema.Fields))
			for i, f := range c.Schema.Fields {
				names[i] = f.Name + ":" + string(f.Type)
			}
			summary.WriteString(strings.Join(names, ", "))
			summary.WriteString("\n")
		}
	}

	result, err := llmRouter.Chat(r.Context(), llm.ChatRequest{
		Model: s.cfg.AnthropicModel,
		Messages: []llm.Message{
			{Role: "system", Content: chatbotSystemPrompt + "\n\n" + summary.String()},
			{Role: "user", Content: message},
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "chat request failed: "+err.Error(), nil)
		return
	}
	s.logUsage(r.Context(), llm.ProviderKind(s.cfg.AnthropicModel), s.cfg.AnthropicModel, result.TokensIn, result.TokensOut, false)
	writeJSON(w, http.StatusOK, chatbotResponse{Reply: result.Content})
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
