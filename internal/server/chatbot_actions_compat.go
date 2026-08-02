package server

import (
	"encoding/json"

	"onebox/internal/llm"
)

// -- compatibility shim: re-stringified nested arguments --------------------
//
// Confirmed live against Ollama 0.32.1 + llama3.2:3b: a tool call's
// array/object arguments sometimes come back JSON-encoded as a *string*
// instead of as a native JSON value —
//
//	{"name":"notes","fields":"[{\"name\":\"body\",\"type\":\"text\"}]"}
//
// instead of the well-formed
//
//	{"name":"notes","fields":[{"name":"body","type":"text"}]}
//
// Anthropic and OpenAI never do this; it's a quirk of how some local
// model chat templates serialize nested tool-call arguments, not
// something this codebase's own request shape causes, and not every
// Ollama-served model does it either. Rather than let payloadStruct
// unmarshaling — or the payload structs' own field types — bend to
// accommodate it, compatNormalizingParser is the one isolated place that
// compensates, by unwrapping any double-encoded value before handing the
// arguments to the real parser. nativeToolCallParser and every payload
// struct stay written against the clean, spec-shaped JSON a well-behaved
// provider actually sends.
//
// This still never inspects English text: it only ever looks at whether
// a JSON *value* is a string that itself parses as a JSON array or
// object — a structural check, not a language one.
//
// To remove this shim once it's no longer needed (every model this
// codebase talks to sends clean native arguments): change actionParser's
// initialization in chatbot_actions.go back to plain
// nativeToolCallParser{}, then delete this file. No other file changes.
type compatNormalizingParser struct {
	inner ActionParser
}

func (p compatNormalizingParser) ParseActions(result llm.ChatResult) []proposedAction {
	if len(result.ToolCalls) == 0 {
		return p.inner.ParseActions(result)
	}
	normalized := make([]llm.ToolCall, len(result.ToolCalls))
	for i, tc := range result.ToolCalls {
		tc.Arguments = normalizeToolArguments(tc.Arguments)
		normalized[i] = tc
	}
	result.ToolCalls = normalized
	return p.inner.ParseActions(result)
}

// normalizeToolArguments unwraps any top-level value in a tool call's
// arguments object that is itself a JSON string containing valid JSON —
// see this file's doc comment. Arguments that don't exhibit the quirk
// (the overwhelming majority — including everything Anthropic/OpenAI
// ever send) pass through byte-for-byte unchanged.
func normalizeToolArguments(args json.RawMessage) json.RawMessage {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return args // not a JSON object at all — nothing this shim can help with
	}
	changed := false
	for key, val := range raw {
		if unwrapped, ok := unwrapDoubleEncoded(val); ok {
			raw[key] = unwrapped
			changed = true
		}
	}
	if !changed {
		return args
	}
	out, err := json.Marshal(raw)
	if err != nil {
		return args
	}
	return out
}

// unwrapDoubleEncoded reports whether val is a JSON string whose content
// is itself a valid JSON array or object, returning that inner value
// un-stringified if so.
func unwrapDoubleEncoded(val json.RawMessage) (json.RawMessage, bool) {
	var s string
	if err := json.Unmarshal(val, &s); err != nil {
		return nil, false // not a JSON string — nothing to unwrap
	}
	if s == "" || (s[0] != '[' && s[0] != '{') || !json.Valid([]byte(s)) {
		return nil, false
	}
	return json.RawMessage(s), true
}
