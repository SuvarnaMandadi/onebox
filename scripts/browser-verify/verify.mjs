#!/usr/bin/env node
// onebox browser-verify — the official browser verification workflow.
//
// Runs a checklist of UI smoke checks against a running onebox server using
// an isolated, throwaway Chromium instance. This is the ONLY sanctioned way
// to do browser-based verification of onebox locally — see
// ../../ARCHITECTURE.md ("Browser verification workflow") and the top-level
// README for the policy this implements: never point Claude's own Chrome
// automation at your personal browser for this project; always use this
// script instead, unless you explicitly ask for Claude's browser tool.
//
// SAFETY / ISOLATION GUARANTEES (please read before changing this file):
//
//   1. This script only ever drives Playwright's OWN BUNDLED Chromium
//      binary (installed under Playwright's browser cache by `npm
//      install` in this directory). It never touches, launches, or
//      attaches to the system/installed Chrome or Edge on this machine —
//      there is no browser-extension bridge here, no "connect to existing
//      session" step, nothing that enumerates already-running browsers.
//      Structurally, `chromium.launchPersistentContext(...)` spawns a
//      brand-new OS process running Playwright's own browser binary; it
//      has no code path that can discover or address a different Chrome
//      process, so it cannot close a window it didn't open.
//
//   2. Every run gets a FRESH temporary user-data-dir (via
//      fs.mkdtempSync), created right before launch and deleted right
//      after. Nothing is ever pointed at a real Chrome profile directory,
//      so there are no shared cookies, extensions, history, or logins
//      between runs or with your personal browser.
//
//   3. Cleanup (`context.close()`) is called in a `finally` block and only
//      ever closes the one context/process this run created — the handle
//      returned by launchPersistentContext refers to that single process.
//      There is no taskkill, pkill, killall, or "close all Chrome windows"
//      logic anywhere in this script, and there must never be.
//
//   4. This script never mutates onebox application code or behavior — it
//      is a pure external test client against the HTTP/UI surface.
//
// Usage:
//   npm install
//   npm run verify              # unauthenticated smoke pass, headless
//   npm run verify:headless     # same, explicit headless
//   npm run verify:auth         # full authenticated checklist (needs creds)
//   npm run verify:headed       # show the browser window (debugging)
//
// Authenticated checks read credentials from environment variables (never
// from a CLI flag, to keep them out of shell history / `ps` output):
//   ONEBOX_VERIFY_EMAIL, ONEBOX_VERIFY_PASSWORD
//
// Optional: ONEBOX_VERIFY_PROPOSALS=1 additionally exercises the chatbot's
// action-proposal flow (see the "Proposals" check below) — off by default
// because it depends on a real LLM provider being configured and its
// response is not deterministic.

import { chromium } from "playwright";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

function parseArgs(argv) {
  const out = {
    serverUrl: "http://localhost:8090",
    headed: false,
    requireAuth: false,
    outDir: path.join(__dirname, "artifacts"),
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--base-url") out.serverUrl = argv[++i];
    else if (a === "--headed") out.headed = true;
    else if (a === "--headless") out.headed = false;
    else if (a === "--require-auth") out.requireAuth = true;
    else if (a === "--out-dir") out.outDir = argv[++i];
  }
  out.serverUrl = out.serverUrl.replace(/\/+$/, "");
  out.dashboardUrl = out.serverUrl + "/_/"; // webui.Handler() is mounted at /_/, see server.go
  // Credentials are read ONLY from env vars, never a CLI flag — a flag
  // would land in shell history and `ps`/task-manager output. Keep it
  // that way; don't add --email/--password back.
  out.email = process.env.ONEBOX_VERIFY_EMAIL || "";
  out.password = process.env.ONEBOX_VERIFY_PASSWORD || "";
  out.testProposals = process.env.ONEBOX_VERIFY_PROPOSALS === "1";
  return out;
}

// Thrown by a check to mean "inconclusive/not applicable", not "broken".
class Skip extends Error {}

// Console messages that are known-benign noise, not real regressions. Keep
// this list short and specific — the point of the "no console errors"
// check is to catch real breakage, not to be silenced into uselessness.
const CONSOLE_ERROR_ALLOWLIST = [/favicon\.ico/i];

const results = []; // { name, status: 'pass'|'fail'|'skip', detail }

function record(name, status, detail) {
  results.push({ name, status, detail: detail || "" });
  const marker = { pass: "✔", fail: "✘", skip: "–" }[status];
  console.log(`[verify] ${marker} ${name}${detail ? " — " + detail : ""}`);
}

// Runs one checklist item. `fn` may throw a `Skip` to record "skip" instead
// of "fail" — used for checks that are genuinely inconclusive (e.g. no
// collections exist yet to click into) rather than broken.
async function check(name, fn) {
  try {
    const detail = await fn();
    record(name, "pass", typeof detail === "string" ? detail : "");
  } catch (err) {
    if (err instanceof Skip) {
      record(name, "skip", err.message);
    } else {
      record(name, "fail", err && err.message ? err.message : String(err));
    }
  }
}

function skip(name, reason) {
  record(name, "skip", reason);
}

// Polls .page-header-title's text until it matches `regex` or times out.
//
// Why this exists (not just waitForSelector + one read): `.page-header-
// title` is present on EVERY dashboard page, so after a route change the
// PREVIOUS page's header element can still be attached to the DOM for a
// brief moment while navigate()'s async render is still in flight —
// app.js does `clear(app)` then synchronously appends the new header, but
// that happens inside an async function invoked (not awaited) from the
// "hashchange" listener, so there's a real window where the old header is
// still what's in the DOM. `waitForSelector(".page-header-title")` is
// satisfied instantly by that stale node and a single immediate read can
// return the previous page's title — this bit us for real: the Files
// check once read "user" (the name of the collection the Records check
// had just been viewing) instead of "File Storage". Polling until the
// text actually matches what THIS check expects removes that race
// without loosening what's being asserted.
async function waitForHeaderText(page, regex, timeout = 10000) {
  const deadline = Date.now() + timeout;
  let lastText = "";
  while (Date.now() < deadline) {
    lastText = (await page.locator(".page-header-title").first().textContent().catch(() => "")) || "";
    if (regex.test(lastText)) return lastText;
    await page.waitForTimeout(100);
  }
  throw new Error(`header never matched ${regex}; last seen: "${lastText.trim()}"`);
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  fs.mkdirSync(opts.outDir, { recursive: true });
  // Clear screenshots (and any leftover temp attachment file) from a
  // previous run before starting — otherwise stale evidence from an old
  // run number sits next to fresh evidence from this one (e.g. a renamed
  // check step leaves both a "05-files.png" and a "06-files.png" around,
  // and it's not obvious which one is current). .gitkeep is untouched.
  for (const f of fs.readdirSync(opts.outDir)) {
    if (f.endsWith(".png") || f.endsWith(".txt")) fs.rmSync(path.join(opts.outDir, f), { force: true });
  }

  // -- 1. Server starts / is reachable -----------------------------------
  // A pre-flight check against /api/health, done before touching a
  // browser at all, so a clear "server isn't running" message shows up
  // immediately instead of a confusing Playwright navigation timeout.
  let serverUp = false;
  await check("Server reachable (GET /api/health)", async () => {
    const res = await fetch(opts.serverUrl + "/api/health", { signal: AbortSignal.timeout(5000) });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const body = await res.json();
    if (body.status !== "ok") throw new Error(`unexpected health response: ${JSON.stringify(body)}`);
    serverUp = true;
    return `version ${body.version || "?"}`;
  });

  if (!serverUp) {
    console.error(
      "\n[verify] Server is not reachable at " +
        opts.serverUrl +
        ". Start it first, e.g.:\n  go run ./cmd/onebox\n  (or) onebox.exe serve\nthen re-run this script.\n"
    );
    printSummary();
    process.exit(1);
  }

  if (opts.requireAuth && !(opts.email && opts.password)) {
    console.error(
      "\n[verify] --require-auth was set but no credentials were found. Set\n" +
        "  ONEBOX_VERIFY_EMAIL and ONEBOX_VERIFY_PASSWORD\n" +
        "before running `npm run verify:auth`.\n"
    );
    process.exit(1);
  }

  // Fresh, throwaway profile directory — never a real Chrome profile path.
  const userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), "onebox-verify-"));
  console.log(`[verify] launching isolated Chromium (temp profile: ${userDataDir})`);

  const consoleErrors = [];
  const pageErrors = [];
  let context, page;
  let shotIndex = 1;
  const shot = async (label) => {
    if (!page) return;
    const file = path.join(opts.outDir, `${String(shotIndex++).padStart(2, "0")}-${label}.png`);
    await page.screenshot({ path: file }).catch(() => {});
  };

  try {
    context = await chromium.launchPersistentContext(userDataDir, {
      headless: !opts.headed,
      viewport: { width: 1280, height: 900 },
    });
    page = context.pages()[0] || (await context.newPage());
    page.on("console", (msg) => {
      if (msg.type() !== "error") return;
      if (CONSOLE_ERROR_ALLOWLIST.some((re) => re.test(msg.text()))) return;
      consoleErrors.push(msg.text());
    });
    page.on("pageerror", (err) => pageErrors.push(String(err)));

    await page.goto(opts.dashboardUrl, { waitUntil: "domcontentloaded", timeout: 30000 });
    await page.waitForSelector("#loginRoot, #shell", { state: "attached", timeout: 15000 });
    await shot("initial-load");

    let authenticated = await page.locator("#shell").isVisible().catch(() => false);

    // -- 2. Login works -----------------------------------------------------
    if (opts.email && opts.password) {
      await check("Login works", async () => {
        if (!authenticated) {
          await page.locator('input[type="email"]').first().fill(opts.email);
          await page.locator('input[type="password"]').first().fill(opts.password);
          await page.getByRole("button", { name: /log in/i }).click();
          await page.waitForSelector("#shell:not(.hidden)", { timeout: 15000 });
          authenticated = true;
        }
        await shot("logged-in");
        return "dashboard shell visible";
      });
    } else {
      skip("Login works", "no ONEBOX_VERIFY_EMAIL/ONEBOX_VERIFY_PASSWORD supplied — run npm run verify:auth with those set for the full checklist");
    }

    const authDependentChecks = [
      "Navigation works",
      "Collections page",
      "Records page",
      "Files page",
      "AI chat page",
      "Attachments",
      "Proposals",
    ];

    if (!authenticated) {
      for (const name of authDependentChecks) {
        skip(name, "requires authentication (npm run verify:auth)");
      }
    } else {
      // -- 3. Navigation works ------------------------------------------
      await check("Navigation works", async () => {
        const routes = await page.locator("#mainNav a[data-route]").all();
        if (routes.length === 0) throw new Error("no nav links found (#mainNav a[data-route])");
        for (const link of routes) {
          await link.click();
          await page.waitForSelector(".page-header-title, #app", { timeout: 10000 }).catch(() => {});
          await page.waitForTimeout(200);
        }
        return `visited ${routes.length} route(s)`;
      });

      // -- 4. Collections page -------------------------------------------
      await check("Collections page", async () => {
        await page.locator('#mainNav a[data-route="collections"]').click();
        await waitForHeaderText(page, /collections/i);
        await page.getByRole("button", { name: /new collection/i }).waitFor({ timeout: 5000 });
        await shot("collections");
        return 'header + "New collection" control present';
      });

      // -- 5. Records page -------------------------------------------------
      await check("Records page", async () => {
        const recordLinks = await page.locator('a[href^="#/records/"]').all();
        if (recordLinks.length === 0) {
          throw new Skip("no collections exist yet — create one first to exercise the records page");
        }
        await recordLinks[0].click();
        await page.waitForSelector("#recordsTabBtn, #recordsPane", { timeout: 10000 });
        await shot("records");
        return "records view for an existing collection rendered";
      });

      // -- 6. Files page -----------------------------------------------
      await check("Files page", async () => {
        await page.locator('#mainNav a[data-route="files"]').click();
        await waitForHeaderText(page, /file storage/i);
        await shot("files");
        return "File Storage header present";
      });

      // -- 7. AI chat page -----------------------------------------------
      let chatOpened = false;
      await check("AI chat page", async () => {
        await page.locator(".chatbot-fab").click();
        await page.waitForSelector(".chatbot-panel:not(.hidden)", { timeout: 10000 });
        await page.locator(".chatbot-panel textarea").waitFor({ timeout: 5000 });
        await page.locator(".chatbot-panel .chatbot-send").waitFor({ timeout: 5000 });
        chatOpened = true;
        await shot("ai-chat");
        return "chat panel opens with composer";
      });

      // -- 8. Attachments ---------------------------------------------
      if (!chatOpened) {
        skip("Attachments", "AI chat page did not open");
      } else {
        await check("Attachments", async () => {
          const testFile = path.join(opts.outDir, "verify-attachment.txt");
          fs.writeFileSync(testFile, "onebox browser-verify smoke test attachment\n");
          try {
            // Scoped to the composer's own data-testid (app.js, initChatbot)
            // rather than a generic `input[type=file]` CSS match — the page
            // can legitimately have other file inputs (Files page, RAG
            // sources, avatar upload, backup restore, ...), and a type-only
            // selector isn't guaranteed to resolve to exactly the chat
            // composer's input. See ARCHITECTURE.md §15.
            const fileInput = page.locator('.chatbot-panel [data-testid="chat-attachment-input"]');
            await fileInput.setInputFiles(testFile);
            const chip = page.locator(".attachment-chip").first();
            await chip.waitFor({ timeout: 15000 });
            await shot("attachment-added");
            await chip.locator(".attachment-chip-remove").click();
            await page.locator(".attachment-chip").first().waitFor({ state: "detached", timeout: 5000 });
            return "attach + remove round-trip via /api/chat-attachments";
          } finally {
            fs.rmSync(testFile, { force: true });
          }
        });
      }

      // -- 9. Proposals -------------------------------------------------
      if (!chatOpened) {
        skip("Proposals", "AI chat page did not open");
      } else if (!opts.testProposals) {
        skip(
          "Proposals",
          "set ONEBOX_VERIFY_PROPOSALS=1 to exercise this — it sends a live prompt to your configured LLM provider and the result depends on the model, so it's opt-in; a deterministic non-UI check lives in internal/server/chatbot_proposal_validator_test.go"
        );
      } else {
        await check("Proposals", async () => {
          const textarea = page.locator(".chatbot-panel textarea");
          await textarea.fill('Propose creating a record in a collection called "verify_smoke_test" — do not create anything else.');
          await page.locator(".chatbot-panel .chatbot-send").click();
          const actionCard = page.locator(".chatbot-action-card").first();
          const appeared = await actionCard
            .waitFor({ timeout: 60000 })
            .then(() => true)
            .catch(() => false);
          if (!appeared) {
            throw new Skip("no action card appeared within 60s — the configured model may not have proposed an action for this prompt; not necessarily a bug");
          }
          await shot("proposal");
          // Never approve here — this is a smoke check, not meant to
          // mutate real data. Reject so the run is side-effect-free.
          await actionCard.getByRole("button", { name: /reject/i }).click();
          return "action-card proposal rendered and rejected";
        });
      }
    }

    // -- 10. No console errors -----------------------------------------
    await check("No console errors", async () => {
      if (pageErrors.length > 0) {
        throw new Error(`${pageErrors.length} uncaught page error(s): ${pageErrors.slice(0, 3).join(" | ")}`);
      }
      if (consoleErrors.length > 0) {
        throw new Error(`${consoleErrors.length} console.error message(s): ${consoleErrors.slice(0, 3).join(" | ")}`);
      }
      return "clean";
    });
  } catch (err) {
    record("Unexpected harness error", "fail", err && err.message ? err.message : String(err));
  } finally {
    // Only ever closes the single process this run launched.
    if (context) await context.close();
    fs.rmSync(userDataDir, { recursive: true, force: true });
    console.log("[verify] isolated browser closed and temp profile removed");
  }

  printSummary();
  const failed = results.some((r) => r.status === "fail");
  process.exit(failed ? 1 : 0);
}

function printSummary() {
  console.log("\n[verify] ==================== Checklist summary ====================");
  const width = Math.max(...results.map((r) => r.name.length), 10);
  for (const r of results) {
    const label = { pass: "PASS", fail: "FAIL", skip: "SKIP" }[r.status];
    console.log(`  ${label.padEnd(4)}  ${r.name.padEnd(width)}  ${r.detail}`);
  }
  const passCount = results.filter((r) => r.status === "pass").length;
  const failCount = results.filter((r) => r.status === "fail").length;
  const skipCount = results.filter((r) => r.status === "skip").length;
  console.log(`[verify] ${passCount} passed, ${failCount} failed, ${skipCount} skipped`);
  console.log("[verify] =============================================================\n");
}

main();
