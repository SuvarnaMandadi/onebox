# Example: docs Q&A

onebox's flagship demo — upload a document, ask a question about it, get
a grounded answer with citations. ~100 lines of plain HTML/JS, no build
step, no framework.

## Run it

1. Start onebox with an embedding provider and an LLM key configured
   (env vars, or set them live in the dashboard under **Settings**):

   ```bash
   ONEBOX_EMBEDDING_API_KEY=sk-... ONEBOX_ANTHROPIC_API_KEY=sk-ant-... ./onebox
   ```

2. Serve this folder (opening `index.html` directly as a `file://` URL
   works in some browsers but not others, since the page's origin
   becomes `null`; a tiny static server is more reliable):

   ```bash
   cd examples/docs-qa
   python3 -m http.server 5500
   # or: npx serve .
   ```

3. Open `http://localhost:5500`, upload a `.pdf`/`.txt`/`.md`, wait for
   ingestion to finish, and ask a question.

## How it works

See [`app.js`](app.js) — three calls: `POST /api/auth/signup` (a
throwaway identity to own the upload), `POST /api/rag/sources`
(multipart upload, ingestion runs in the background), and
`POST /api/rag/answer` once ingestion status is `done`. Full walkthrough:
[`docs/tutorial-chat-with-your-docs.md`](../../docs/tutorial-chat-with-your-docs.md).
