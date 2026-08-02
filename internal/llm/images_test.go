package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestToOllamaMessagesEncodesImagesAsFlatBase64Array(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "what's in this?", Images: []MessageImage{{MediaType: "image/png", Data: []byte("fake-bytes")}}},
	}
	out := toOllamaMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("got %d messages, want 1", len(out))
	}
	if out[0].Content != "what's in this?" {
		t.Fatalf("content = %q", out[0].Content)
	}
	if len(out[0].Images) != 1 {
		t.Fatalf("got %d images, want 1", len(out[0].Images))
	}
	want := base64.StdEncoding.EncodeToString([]byte("fake-bytes"))
	if out[0].Images[0] != want {
		t.Fatalf("image[0] = %q, want %q (no data: prefix)", out[0].Images[0], want)
	}
	if strings.HasPrefix(out[0].Images[0], "data:") {
		t.Fatal("ollama images must be raw base64, not a data: URI")
	}
}

func TestToOllamaMessagesTextOnlyOmitsImagesField(t *testing.T) {
	out := toOllamaMessages([]Message{{Role: "user", Content: "hi"}})
	b, _ := json.Marshal(out[0])
	if strings.Contains(string(b), "images") {
		t.Fatalf("text-only message must not emit an images key at all: %s", b)
	}
}

func TestToOpenAIMessagesBuildsDataURIContentParts(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "describe this", Images: []MessageImage{{MediaType: "image/jpeg", Data: []byte("jpg-bytes")}}},
	}
	out := toOpenAIMessages(msgs)
	parts, ok := out[0].Content.([]openAIContentPart)
	if !ok {
		t.Fatalf("Content = %T, want []openAIContentPart", out[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2 (text + image_url)", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "describe this" {
		t.Fatalf("part[0] = %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil {
		t.Fatalf("part[1] = %+v", parts[1])
	}
	wantURI := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("jpg-bytes"))
	if parts[1].ImageURL.URL != wantURI {
		t.Fatalf("image url = %q, want %q", parts[1].ImageURL.URL, wantURI)
	}
}

func TestToOpenAIMessagesTextOnlyStaysPlainString(t *testing.T) {
	out := toOpenAIMessages([]Message{{Role: "user", Content: "hi"}})
	s, ok := out[0].Content.(string)
	if !ok || s != "hi" {
		t.Fatalf("Content = %#v, want plain string %q — must be byte-identical to pre-image-support wire shape", out[0].Content, "hi")
	}
	b, err := json.Marshal(out[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"content":"hi"`) {
		t.Fatalf("marshaled content must stay a plain JSON string: %s", b)
	}
}

func TestAnthropicContentBuildsImageBlocks(t *testing.T) {
	m := Message{Role: "user", Content: "what is this", Images: []MessageImage{{MediaType: "image/png", Data: []byte("png-bytes")}}}
	blocks, ok := anthropicContent(m).([]anthropicContentBlock)
	if !ok {
		t.Fatalf("anthropicContent = %T, want []anthropicContentBlock", anthropicContent(m))
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (text + image)", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "what is this" {
		t.Fatalf("block[0] = %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil {
		t.Fatalf("block[1] = %+v", blocks[1])
	}
	if blocks[1].Source.MediaType != "image/png" {
		t.Fatalf("media type = %q", blocks[1].Source.MediaType)
	}
	wantData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	if blocks[1].Source.Data != wantData {
		t.Fatalf("data = %q, want %q", blocks[1].Source.Data, wantData)
	}
}

func TestAnthropicContentTextOnlyStaysPlainString(t *testing.T) {
	m := Message{Role: "user", Content: "hi"}
	s, ok := anthropicContent(m).(string)
	if !ok || s != "hi" {
		t.Fatalf("anthropicContent(text-only) = %#v, want plain string %q — must match pre-image-support wire shape exactly", anthropicContent(m), "hi")
	}
}

func TestVisionCapableHeuristics(t *testing.T) {
	cases := []struct {
		provider, model string
		want            bool
	}{
		{"anthropic", "claude-sonnet-5", true},
		{"anthropic", "claude-3-5-sonnet-20241022", true},
		{"anthropic", "claude-2.1", false},
		{"anthropic", "claude-instant-1.2", false},
		{"openai", "gpt-4o", true},
		{"openai", "gpt-4o-mini", true},
		{"openai", "gpt-3.5-turbo", false},
		{"openai", "o3-mini", true},
		{"ollama", "llava:13b", true},
		{"ollama", "llama3.2-vision:11b", true},
		{"ollama", "llama3.2:3b", false},
		{"ollama", "nomic-embed-text", false},
		{"unknown-provider", "anything", false},
	}
	for _, tc := range cases {
		if got := VisionCapable(tc.provider, tc.model); got != tc.want {
			t.Errorf("VisionCapable(%q, %q) = %v, want %v", tc.provider, tc.model, got, tc.want)
		}
	}
}
