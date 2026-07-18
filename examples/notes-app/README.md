# Example: AI notes

Collections CRUD (real signup/login, not a throwaway account) plus the
LLM gateway — a "Summarize with AI" button per note.

## Run it

1. Start onebox: `./onebox`
2. Create the `notes` collection (schema is admin-defined infrastructure;
   only an admin can create collections — see the root
   [ROADMAP.md](../../ROADMAP.md) design). Easiest via the dashboard at
   `http://localhost:8090/_/` → **Collections → New collection**, name
   `notes`, one field `body` (text, required). Or via curl, after
   bootstrapping an admin:

   ```bash
   curl -X POST http://localhost:8090/api/collections \
     -H "Authorization: Bearer <admin token>" -H "Content-Type: application/json" \
     -d '{"name":"notes","schema":{"fields":[{"name":"body","type":"text","required":true}]}}'
   ```

3. Set an Anthropic key (env var `ONEBOX_ANTHROPIC_API_KEY`, or live in
   the dashboard's Settings) for the summarize button to work.

4. Serve this folder and open it:

   ```bash
   cd examples/notes-app
   python3 -m http.server 5500
   ```

5. Sign up, add a few notes, click **Summarize with AI** on one.

## How it works

Real signup/login this time (see [`app.js`](app.js)), then plain
collections CRUD: `GET/POST/DELETE /api/collections/notes/records`. The
summarize button calls `POST /api/llm/chat` with the note's text —
nothing is saved, it's a live example of the LLM gateway.
