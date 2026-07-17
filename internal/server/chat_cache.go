package server

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"onebox/internal/llm"
)

const chatCacheTTL = 1 * time.Hour

type chatCacheEntry struct {
	result    llm.ChatResult
	expiresAt time.Time
}

// chatCache is an in-memory, hash(model+messages)-keyed cache of chat
// completions. Only non-streaming requests are cached — a streamed
// response doesn't have a natural "replay" beyond bypassing the cache
// (see handleLLMChat), which is a fine v0.1 simplification.
type chatCache struct {
	mu      sync.Mutex
	entries map[string]chatCacheEntry
}

func newChatCache() *chatCache {
	return &chatCache{entries: make(map[string]chatCacheEntry)}
}

func chatCacheKey(req llm.ChatRequest) string {
	h := sha256.New()
	h.Write([]byte(req.Model))
	for _, m := range req.Messages {
		h.Write([]byte{0})
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *chatCache) Get(key string) (llm.ChatResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return llm.ChatResult{}, false
	}
	return e.result, true
}

func (c *chatCache) Set(key string, result llm.ChatResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = chatCacheEntry{result: result, expiresAt: time.Now().Add(chatCacheTTL)}
}
