# Tutorial: build a chat-with-your-docs app

This walks through building a minimal app where a user uploads a document
and asks questions about it — the same flow as [`examples/docs-qa`](../examples/docs-qa),
explained step by step. You'll use the [JS SDK](../sdk/js/README.md), but
every step is a plain REST call underneath, so this works the same way
from any language.

## What you're building

A single page with three parts:

1. A signup/login form (so the app has a user identity to own the upload)
2. A file picker that uploads a document for ingestion
3. A question box that calls `/api/rag/answer` and shows the grounded answer

## 1. Start onebox and set provider keys

```bash
./onebox
```

Then, in the admin dashboard (`http://localhost:8090/_/`) → **Settings**,
set:
- **Embedding provider**: `openai` (or `ollama` if you're running a local model)
- **Embedding API key**
- **Anthropic API key** (RAG's answer endpoint uses this by default)

These are encrypted at rest and take effect immediately — no restart.

## 2. Get a user identity

Every upload and query needs to be attributed to *someone*, so the app
signs the visitor up as a onebox `_users` account transparently:

```ts
import { OneboxClient } from "onebox-js";

const client = new OneboxClient({ baseUrl: "http://localhost:8090" });

async function ensureSession() {
  const saved = localStorage.getItem("token");
  if (saved) {
    client.setToken(saved);
    return;
  }
  // For a real app you'd show a signup/login form. For this tutorial,
  // a random throwaway account keeps things simple.
  const email = `visitor-${Date.now()}@example.com`;
  const { token } = await client.auth.signup(email, crypto.randomUUID());
  localStorage.setItem("token", token);
  client.setToken(token);
}
```

## 3. Upload a document

```ts
async function uploadDocument(file: File) {
  const source = await client.rag.uploadSource(file, file.name);
  return source.id; // status starts "pending"
}
```

Ingestion (extract → chunk → embed) runs in the background on the
server. Poll until it's done:

```ts
async function waitForIngestion(sourceId: string) {
  for (;;) {
    const source = await client.rag.getSource(sourceId);
    if (source.status === "done") return source;
    if (source.status === "error") throw new Error(source.error);
    await new Promise((r) => setTimeout(r, 1000));
  }
}
```

## 4. Ask a question

```ts
async function ask(question: string) {
  const { answer, sources } = await client.rag.answer(question, 5);
  return { answer, sources };
}
```

`sources` is the list of chunks the answer was grounded in — each with a
`source_id` and a cosine-similarity `score` — useful for showing "here's
where this came from" citations in your UI.

## 5. Put it together

```ts
await ensureSession();

fileInput.addEventListener("change", async () => {
  const file = fileInput.files[0];
  statusEl.textContent = "uploading...";
  const id = await uploadDocument(file);
  statusEl.textContent = "ingesting...";
  await waitForIngestion(id);
  statusEl.textContent = "ready — ask a question below";
});

askButton.addEventListener("click", async () => {
  const { answer, sources } = await ask(questionInput.value);
  answerEl.textContent = answer;
  sourcesEl.textContent = sources.map((s) => `"${s.text.slice(0, 80)}..."`).join("\n");
});
```

That's the whole app. See [`examples/docs-qa/`](../examples/docs-qa) for
the complete, runnable version (plain HTML/JS, no build step — open
`index.html` after starting `./onebox`).

## Where to go from here

- Swap the throwaway-account signup for a real login form — see
  [`internal/webui`](../internal/webui) (the admin dashboard) for a plain-JS
  reference implementation of a login flow.
- Scope documents per-user automatically: `/api/rag/query` and
  `/api/rag/answer` already only search the caller's own ingested chunks
  (unless the caller is an admin), so multi-tenant document isolation is
  free.
- Add realtime: subscribe to a `notes` collection with
  `client.subscribeRealtime()` to show new records as they're created by
  other clients — see the [API reference](api-reference.md#realtime).
