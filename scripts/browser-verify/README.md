# browser-verify

The official browser verification workflow for onebox. This replaces "ask
Claude to click through the UI in my real Chrome" as a verification step —
see [ARCHITECTURE.md §15](../../ARCHITECTURE.md#15-browser-verification-workflow)
for why that policy exists and the full checklist reference.

**Policy: use this script for all browser-based UI verification of onebox.
Never point Claude's own Chrome-automation tool at your personal browser
for this project unless you explicitly ask for it by name for a specific
one-off task.**

## Why this exists

OneBox's Go codebase has no code path that launches, attaches to, or closes
any browser — confirmed by an exhaustive repo-wide search (no
Playwright/Selenium/chromedp/exec.Command/taskkill/pkill anywhere in the
project). Chrome windows disappearing during ad hoc "browser verification"
was traced to a different mechanism entirely: Claude's own Chrome-automation
feature, which drives your **actual installed Chrome browser** (via an
extension, in your existing browser session) rather than a disposable,
isolated instance. This script is the isolated alternative.

## Isolation guarantees

- Launches **Playwright's own bundled Chromium** — a separate binary,
  fetched into Playwright's local browser cache by `npm install`, not your
  system-installed Chrome/Edge. There is no extension, no "attach to
  existing browser session" step, and no code path that can enumerate or
  address a different Chrome process.
- Every run gets a **fresh temporary profile directory**
  (`fs.mkdtempSync`), created immediately before launch and deleted
  immediately after. Nothing is ever pointed at a real Chrome profile — no
  shared cookies, history, extensions, or logins.
- Cleanup only closes the one browser process this run started (the handle
  from `launchPersistentContext` refers to exactly that process). There is
  no `taskkill`, `pkill`, or "close all Chrome" logic anywhere in this
  script, and there must never be — please keep it that way in any future
  edit.
- Pure external test client: this script never edits onebox application
  code or changes its runtime behavior.

## Usage

```sh
go run ./cmd/onebox            # or ./onebox / onebox.exe serve — in another terminal
cd scripts/browser-verify
npm install                    # fetches Playwright + its bundled Chromium (first run only)
npm run verify                 # quick unauthenticated smoke pass, headless
```

| Script | What it does |
|---|---|
| `npm run verify` | Headless, unauthenticated — checks the server responds and the login screen loads. |
| `npm run verify:headless` | Same as `verify`, explicit about headless mode. |
| `npm run verify:auth` | The full checklist, authenticated. Fails fast with a clear message if credentials aren't set. |
| `npm run verify:headed` | Shows the browser window — useful when debugging the harness itself. |

### Authenticated checklist

`verify:auth` (or `verify`/`verify:headless` when credentials happen to be
set) reads credentials from environment variables — never a CLI flag, to
keep them out of shell history / `ps` output:

```sh
ONEBOX_VERIFY_EMAIL=you@example.com ONEBOX_VERIFY_PASSWORD=yourpassword \
  npm run verify:auth
```

Use a throwaway/dev admin account for this, not a production credential.
With credentials set, the script also walks every nav route and exercises
collections, records, files, the AI chat panel, and the chat attachment
upload/remove round-trip.

### Proposals (opt-in)

The "Proposals" check sends a live prompt designed to make the chatbot
propose an action (see `ARCHITECTURE.md` §4–§5), then clicks **Reject**. It
depends on a real, configured LLM provider and the model's own behavior, so
it's off by default — set `ONEBOX_VERIFY_PROPOSALS=1` to run it. Skipping
(or a "no action card appeared" result) is not treated as a failure, since
that's expected when no provider is configured or the model declines to
propose anything. Note the **Approve** control is permanently disabled
client-side today, so this check can never mutate real data either way. A
deterministic, non-UI check of the same validation pipeline lives in
`internal/server/chatbot_proposal_validator_test.go`.

### Other flags

- `--base-url <url>` — the onebox server root (default
  `http://localhost:8090`; the script appends `/_/` itself for the
  dashboard, matching `server.go`'s `r.Mount("/_/", ...)`).
- `--headed` / `--headless` — show or hide the browser window.
- `--require-auth` — fail immediately (rather than skipping checks) if
  credentials aren't set; this is what `verify:auth` uses.
- `--out-dir <path>` — where screenshots are written (default:
  `./artifacts`).

## The checklist

Each run prints a pass/fail/skip line per item and a summary table at the
end; exit code is non-zero if anything failed (skips don't fail the run).

1. **Server reachable** — `GET /api/health`, checked before a browser is
   even launched, so a stopped server gives an immediate, actionable
   message instead of a confusing navigation timeout.
2. **Login works**
3. **Navigation works** — every `#mainNav` route is clicked
4. **Collections page**
5. **Records page** — skipped if no collections exist yet to click into
6. **Files page**
7. **AI chat page**
8. **Attachments** — a real upload + remove round-trip through
   `/api/chat-attachments`
9. **Proposals** — opt-in, see above
10. **No console errors** — fails on any uncaught page error or
    `console.error`, including the browser's own "failed to load resource"
    logging for broken requests

Screenshots from each step land in `./artifacts/*.png` as evidence of what
was actually checked; that directory is gitignored except for a
`.gitkeep`. Each run clears any `*.png`/`*.txt` left over from a previous
run before starting, so `artifacts/` only ever reflects the most recent
run — it won't accumulate stale, differently-numbered screenshots from
earlier versions of the checklist.
