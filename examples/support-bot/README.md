# Example: support bot

RAG + the LLM gateway as a chat widget — a support-bot starter that
answers from your team's uploaded documents.

## Run it

1. Start onebox with an embedding + Anthropic key configured.
2. Upload your FAQ/knowledge base docs — via the admin dashboard
   (`http://localhost:8090/_/` → RAG sources), or reuse
   [`examples/docs-qa`](../docs-qa).
3. Serve this folder and open it:

   ```bash
   cd examples/support-bot
   python3 -m http.server 5500
   ```

4. Ask a question in the chat widget.

## How it works

Every message is one call to `POST /api/rag/answer` (see
[`app.js`](app.js)) — retrieval and the grounded answer happen
server-side in a single request. A throwaway onebox identity is created
transparently on the first message, the same pattern a real support
widget would use before a visitor has an account.

## Extending this

- Swap `/api/rag/answer` for `/api/llm/chat` with `stream: true` to
  stream the answer token-by-token instead of waiting for the full
  response — see the [API reference](../../docs/api-reference.md#llm-gateway).
- Scope the knowledge base per customer by having each customer's admin
  upload to their own onebox instance (or extend collection rules to your
  own multi-tenant scheme).
