# Quickstart

Goal: from a fresh clone to answering a question about an uploaded PDF, in
under two minutes.

## 1. Build the binary

```bash
git clone https://github.com/SuvarnaMandadi/onebox.git
cd onebox
go build -o onebox ./cmd/onebox
```

(Or cross-compile for another OS — see the root [README](../README.md).)

## 2. Run it

```bash
./onebox
```

You should see:

```
onebox listening on :8090 (data dir: ./onebox_data)
```

That's the whole install. One binary, one process, a `./onebox_data`
folder for the SQLite database and uploaded files.

## 3. Open the admin dashboard

Go to **http://localhost:8090/_/** in a browser. Click **"Create first
admin account"** — this only works once, before any admin exists.

## 4. Set an embedding + LLM key (for RAG)

Stop the server (`Ctrl+C`), set two environment variables, and restart:

```bash
export ONEBOX_EMBEDDING_API_KEY=sk-...      # any OpenAI-compatible key
export ONEBOX_ANTHROPIC_API_KEY=sk-ant-...  # for /api/rag/answer
./onebox
```

Or skip the restart entirely: log into the dashboard and set both keys
under **Settings** — they take effect immediately, no restart needed.

## 5. Upload a document and ask about it

In the dashboard, go to **RAG sources → Upload & ingest**, pick a PDF.
Wait a few seconds for its status to flip from `pending` → `processing` →
`done`.

Then, from any HTTP client (or the [JS SDK](../sdk/js/README.md)):

```bash
curl -X POST http://localhost:8090/api/auth/signup \
  -H "Content-Type: application/json" \
  -d '{"email":"me@example.com","password":"hunter22222"}'
# → {"token": "...", "record": {...}}

curl -X POST http://localhost:8090/api/rag/answer \
  -H "Authorization: Bearer <token from above>" \
  -H "Content-Type: application/json" \
  -d '{"query": "what does this document say about pricing?"}'
# → {"answer": "...", "sources": [...]}
```

That's it — a full backend (auth, storage, vector search, and a grounded
LLM answer) from one binary and two API calls.

## Next

- [Tutorial: build a chat-with-your-docs app](tutorial-chat-with-your-docs.md)
- [API reference](api-reference.md)
- Example apps: [`examples/`](../examples/)
