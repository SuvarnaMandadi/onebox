package llm

import "strings"

// VisionCapable reports whether a given provider+model combination is
// expected to accept image inputs. There's no live "capabilities" endpoint
// any of the three providers expose, so this is a name-based heuristic —
// used only to decide whether to attach image bytes to an outgoing chat
// request (see resolveAttachments in internal/server/chatbot_attachments.go)
// or leave them out and tell the admin why instead.
//
// This deliberately leans conservative: getting it wrong by treating a
// vision-capable model as text-only just means an attached image silently
// isn't sent (the admin still gets a reply, plus a note explaining why —
// see resolveAttachments), which is a much smaller failure than getting it
// wrong the other way and sending an image block to a model that doesn't
// understand the "images"/"image_url"/"image" shape, which several
// providers reject outright with a 4xx for the whole request — including
// the text the admin actually cared about.
func VisionCapable(provider, model string) bool {
	m := strings.ToLower(model)
	switch provider {
	case "anthropic":
		// Every current Claude 3+ model (Sonnet/Opus/Haiku, and the naming
		// onebox itself ships as an example, "claude-sonnet-5") is
		// vision-capable — only the legacy claude-1/claude-2/claude-instant
		// lines were text-only.
		for _, frag := range []string{"claude-1", "claude-2", "claude-instant"} {
			if strings.HasPrefix(m, frag) {
				return false
			}
		}
		return true
	case "openai":
		for _, frag := range []string{"gpt-4o", "gpt-4-turbo", "gpt-4.1", "gpt-4.5", "gpt-5", "o1", "o3", "o4", "vision"} {
			if strings.Contains(m, frag) {
				return true
			}
		}
		return false
	case "ollama":
		// Ollama has no fixed naming scheme, so this checks for the name
		// fragments of the vision-capable model families it's commonly
		// used to pull: llava/bakllava/moondream (dedicated vision
		// models), and the vision variants of otherwise-text-only
		// families (llama3.2-vision, llama4, qwen2-vl, minicpm-v).
		for _, frag := range []string{"llava", "vision", "bakllava", "moondream", "pixtral", "qwen2-vl", "qwen-vl", "minicpm-v", "llama4"} {
			if strings.Contains(m, frag) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
