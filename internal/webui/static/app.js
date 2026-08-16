// onebox admin dashboard — plain JS, no build step, no framework.
// Hash-based routing; every view is a render(container) function.

const TOKEN_KEY = "onebox_admin_token";
const ROLE_KEY = "onebox_role"; // "admin" | "user" — which login/signup endpoint issued the current token
const THEME_KEY = "onebox_theme";

// Tokens live in localStorage (survives browser restarts) when the user
// checked "Remember me" at login, sessionStorage (cleared when the tab/
// browser closes) otherwise — read checks both since either may hold it.
//
// Final fallback reads the React dashboard's (/app/) token key. The two
// UIs authenticate against the same backend and already share the
// "onebox_role" key verbatim (see ROLE_KEY above and web/src/lib/auth.tsx),
// but historically stored the token itself under different names — so an
// admin already signed into /app/ who clicked "Open classic settings" (or
// any other /_/ link) hit a second, redundant login screen for the exact
// same session. Falling back here closes that gap without touching how
// either UI writes its own token.
function getToken() { return localStorage.getItem(TOKEN_KEY) || sessionStorage.getItem(TOKEN_KEY) || localStorage.getItem("onebox_token") || ""; }
function setToken(t, remember = true) {
  (remember ? localStorage : sessionStorage).setItem(TOKEN_KEY, t);
  (remember ? sessionStorage : localStorage).removeItem(TOKEN_KEY);
}
function clearToken() { localStorage.removeItem(TOKEN_KEY); sessionStorage.removeItem(TOKEN_KEY); }

function getRole() { return localStorage.getItem(ROLE_KEY) || sessionStorage.getItem(ROLE_KEY) || "user"; }
function setRole(r, remember = true) {
  (remember ? localStorage : sessionStorage).setItem(ROLE_KEY, r);
  (remember ? sessionStorage : localStorage).removeItem(ROLE_KEY);
}
function clearRole() { localStorage.removeItem(ROLE_KEY); sessionStorage.removeItem(ROLE_KEY); }
function isAdminRole() { return getRole() === "admin"; }

// accountCache holds the signed-in identity's display info (name, email,
// avatar) so the sidebar and Home page don't each re-fetch it. Cleared on
// logout/login and refreshed whenever the Account page saves changes.
let accountCache = null;

async function loadAccount(force = false) {
  if (accountCache && !force) return accountCache;
  accountCache = await api(isAdminRole() ? "/api/admins/me" : "/api/auth/me");
  return accountCache;
}

function initials(nameOrEmail) {
  const trimmed = (nameOrEmail || "").trim();
  if (!trimmed) return "?";
  const parts = trimmed.split(/\s+/);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return trimmed.slice(0, 2).toUpperCase();
}

// displayName never falls back to the email prefix — an email address is
// not a name, and showing one read as a bug (hand-tested feedback).
function displayName(account) {
  if (!account) return "";
  if (account.display_name) return account.display_name;
  return [account.first_name, account.last_name].filter(Boolean).join(" ").trim();
}

// avatarBlobCache maps a file id to an object URL — avatar images are
// served from an authenticated endpoint (/api/files/:id), and a plain
// <img src="..."> can't send an Authorization header, so every avatar is
// fetched once via api()-style auth and reused as a blob: URL after that.
const avatarBlobCache = new Map();

async function resolveAvatarURL(fileId) {
  if (avatarBlobCache.has(fileId)) return avatarBlobCache.get(fileId);
  try {
    const res = await fetch("/api/files/" + fileId, { headers: { Authorization: "Bearer " + getToken() } });
    if (!res.ok) return null;
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    avatarBlobCache.set(fileId, url);
    return url;
  } catch (e) {
    return null;
  }
}

function avatarNode(account, size = "avatar-sm") {
  const node = el("span", { class: "avatar " + size });
  fillAvatarNode(node, account);
  return node;
}

// fillAvatarNode populates an existing avatar <span> in place (so callers
// that already hold a reference to a fixed-id slot, like the sidebar's
// #accountAvatar, don't have to juggle replacing/re-tagging DOM nodes).
// Shows initials immediately, then swaps in the real photo once the
// authenticated fetch resolves (see resolveAvatarURL).
function fillAvatarNode(node, account) {
  clear(node);
  node.textContent = initials(displayName(account) || (account && account.email));
  if (account && account.avatar_file_id) {
    const fileId = account.avatar_file_id;
    resolveAvatarURL(fileId).then((url) => {
      if (!url) return;
      clear(node);
      node.appendChild(el("img", { src: url, alt: "" }));
    });
  }
}

async function refreshAccountSummary() {
  const nameEl = document.getElementById("accountName");
  const roleEl = document.querySelector("#accountSummary .account-summary-role");
  const avatarSlot = document.getElementById("accountAvatar");
  if (!nameEl) return;
  try {
    const account = await loadAccount();
    nameEl.textContent = displayName(account) || "Account";
    roleEl.textContent = isAdminRole() ? "Admin" : "User";
    fillAvatarNode(avatarSlot, account);
  } catch (e) {
    nameEl.textContent = isAdminRole() ? "Admin" : "Account";
  }
}

// Endpoints where a 401 means "your credentials were wrong" (login,
// signup, and the two forgot-password flows), never "your session
// expired" — there is no session yet at the point any of these are
// called. api() must not treat their 401s as a dead session, or a wrong
// password on the login form gets silently rewritten into a confusing
// "session expired" message (a real bug hand-tested and reported).
const CREDENTIAL_ENDPOINTS = [
  "/api/login",
  "/api/auth/login", "/api/auth/signup",
  "/api/admins/login", "/api/admins/signup",
  "/api/auth/recover-password", "/api/auth/reset-password",
];

// api() wraps fetch: sends the bearer token, parses JSON, and throws a
// readable Error (with a .code from the server's {code,message} envelope
// so callers can branch on specific failures) using that envelope.
async function api(path, opts = {}) {
  const headers = Object.assign({}, opts.headers || {});
  const token = getToken();
  if (token) headers["Authorization"] = "Bearer " + token;
  if (opts.body && !(opts.body instanceof FormData)) {
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(path, Object.assign({}, opts, { headers }));

  const isCredentialEndpoint = CREDENTIAL_ENDPOINTS.some((p) => path.startsWith(p));
  if (res.status === 401 && !isCredentialEndpoint) {
    clearToken();
    clearRole();
    accountCache = null;
    location.hash = "#/login";
    throw new Error("Your session has expired — please log in again.");
  }
  if (res.status === 204) return null;
  const isJSON = (res.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await res.json() : await res.text();
  if (!res.ok) {
    const msg = isJSON && body && body.message ? body.message : String(body);
    const err = new Error(msg);
    if (isJSON && body && body.code) err.code = body.code;
    err.status = res.status;
    throw err;
  }
  return body;
}

// -- tiny DOM helpers --------------------------------------------------

function el(tag, attrs = {}, children = []) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "text") node.textContent = v;
    else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v);
  }
  for (const child of [].concat(children)) {
    if (child == null) continue;
    node.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
  }
  return node;
}

function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

// -- icon system -----------------------------------------------------------
// One original, hand-drawn line-icon set (outline style, 24x24 viewBox,
// currentColor stroke) used everywhere the UI needs an icon-shaped visual —
// nav, page headers, empty states — so the product draws from a single
// consistent set instead of ad hoc emoji sprinkled through the markup.
const ICONS = {
  home: '<path d="M4 11.5 12 4l8 7.5"/><path d="M6 10v9a1 1 0 0 0 1 1h3v-5h4v5h3a1 1 0 0 0 1-1v-9"/>',
  collections: '<rect x="4" y="4" width="16" height="4.5" rx="1.2"/><rect x="4" y="10" width="16" height="4.5" rx="1.2"/><rect x="4" y="16" width="10" height="4.5" rx="1.2"/>',
  files: '<path d="M4 6.5a1 1 0 0 1 1-1h4.5l1.5 2H19a1 1 0 0 1 1 1V18a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6.5Z"/>',
  rag: '<path d="M4 5.5C6 4.5 9 4.5 12 5.5V19c-3-1-6-1-8 0V5.5Z"/><path d="M20 5.5C18 4.5 15 4.5 12 5.5V19c3-1 6-1 8 0V5.5Z"/>',
  usage: '<rect x="4.5" y="11" width="3" height="8" rx="1" fill="currentColor" stroke="none"/><rect x="10.5" y="6" width="3" height="13" rx="1" fill="currentColor" stroke="none"/><rect x="16.5" y="13" width="3" height="6" rx="1" fill="currentColor" stroke="none"/>',
  logs: '<path d="M6 3h9l3 3v15H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Z"/><path d="M9 9h6M9 13h6M9 17h4"/>',
  backups: '<rect x="3.5" y="4" width="17" height="4.5" rx="1"/><path d="M5 8.5V19a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8.5"/><path d="M10 13h4"/>',
  settings: '<circle cx="12" cy="12" r="3.2"/><path d="M12 3v2.4M12 18.6V21M21 12h-2.4M5.4 12H3M18.4 5.6l-1.7 1.7M7.3 16.7l-1.7 1.7M18.4 18.4l-1.7-1.7M7.3 7.3 5.6 5.6"/>',
  account: '<circle cx="12" cy="8.5" r="3.2"/><path d="M5 19.2c1.4-3 4-4.7 7-4.7s5.6 1.7 7 4.7"/>',
  record: '<path d="M7 3h7l4 4v13a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Z"/><path d="M9 12h6M9 16h6"/><path d="M14 3v4h4"/>',
  inbox: '<path d="M4 12h4.5l1.5 3h4l1.5-3H20"/><path d="M4 12 6 5a1 1 0 0 1 1-.7h10a1 1 0 0 1 1 .7l2 7v6a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-6Z"/>',
  rocket: '<path d="M12 3c2.5 1.5 4 4.3 4 8 0 2-.6 3.6-1.4 5H9.4C8.6 14.6 8 13 8 11c0-3.7 1.5-6.5 4-8Z"/><circle cx="12" cy="9.5" r="1.4"/><path d="M9 16 6.5 20M15 16l2.5 4"/>',
  sparkle: '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3Z"/>',
  chat: '<path d="M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v9A1.5 1.5 0 0 1 18.5 16H9l-4 4v-4H5.5A1.5 1.5 0 0 1 4 14.5v-9Z"/>',
  close: '<path d="M6 6l12 12M18 6 6 18"/>',
  cpu: '<rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3"/>',
  globe: '<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5c2.5 2.5 3.8 5.5 3.8 8.5s-1.3 6-3.8 8.5c-2.5-2.5-3.8-5.5-3.8-8.5s1.3-6 3.8-8.5Z"/>',
  server: '<rect x="4" y="4" width="16" height="6" rx="1.2"/><rect x="4" y="14" width="16" height="6" rx="1.2"/><circle cx="7.5" cy="7" r="0.8" fill="currentColor" stroke="none"/><circle cx="7.5" cy="17" r="0.8" fill="currentColor" stroke="none"/>',
  lock: '<rect x="5" y="10.5" width="14" height="9" rx="1.5"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"/>',
  key: '<circle cx="8" cy="15" r="3"/><path d="M10.2 12.8 18 5M15 8l2 2M18 5l2 2"/>',
  minimize: '<path d="M6 18h12"/>',
  maximize: '<rect x="5.5" y="5.5" width="13" height="13" rx="1.5"/>',
  restore: '<rect x="7" y="7" width="10" height="10" rx="1.2"/><path d="M10 7V5.5A1.5 1.5 0 0 1 11.5 4h7A1.5 1.5 0 0 1 20 5.5v7a1.5 1.5 0 0 1-1.5 1.5H17"/>',
  history: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 1.8"/>',
  copy: '<rect x="8" y="8" width="11" height="11" rx="1.5"/><path d="M5 15V6a1 1 0 0 1 1-1h9"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
};

function icon(name, opts = {}) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", String(opts.size || 18));
  svg.setAttribute("height", String(opts.size || 18));
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.8");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  svg.classList.add("icon");
  svg.innerHTML = ICONS[name] || "";
  return svg;
}

// applyStaticIcons fills in the icon placeholders declared in index.html —
// nav links carry a data-icon attribute rather than inline SVG, so the
// icon set stays defined in exactly one place (the ICONS map above)
// instead of drifting between the static shell and the JS-rendered pages.
function applyStaticIcons() {
  document.querySelectorAll("[data-icon]").forEach((node) => node.appendChild(icon(node.dataset.icon)));
}

function emptyState(iconName, title, hint) {
  const ic = icon(iconName, { size: 32 });
  ic.classList.add("empty-icon");
  return el("div", { class: "empty-state" }, [
    ic,
    el("div", { text: title }),
    hint ? el("div", { class: "empty-hint", text: hint }) : null,
  ]);
}

// pageHeader is the one page-title pattern every top-level page uses:
// icon + title on the left, the page's primary action button(s) on the
// right, consistent spacing below. Previously every page hand-rolled its
// own `el("h2", ...)` with no consistent place for a primary action,
// so "New collection"/"Upload"/etc. ended up buried in a card instead of
// where users expect it — top-right, next to the title.
// cardTitle is pageHeader's small-scale sibling — icon + text for an
// h3-level card heading (provider cards on Settings, etc.) instead of a
// full page header, using the same icon set so a card title never falls
// back to emoji just because it's not a top-level page.
function cardTitle(iconName, text) {
  const ic = icon(iconName, { size: 16 });
  ic.classList.add("card-title-icon");
  return el("h3", { class: "card-title" }, [ic, text]);
}

function pageHeader(iconName, title, actions) {
  const ic = iconName ? icon(iconName, { size: 20 }) : null;
  if (ic) ic.classList.add("page-header-icon");
  return el("div", { class: "page-header" }, [
    el("h2", { class: "page-header-title" }, [ic, title]),
    el("div", { class: "row page-header-actions" }, actions || []),
  ]);
}

// -- toasts --------------------------------------------------------------
// Every action in this dashboard reports its outcome via a toast — success
// or failure — instead of a silent refresh, per the "nothing may silently
// refresh" design requirement.

const toastRoot = document.getElementById("toasts");

// opts.action = { label, onClick } renders an inline button inside the
// toast (used by the chatbot's "both retries failed" toast to offer a
// one-click Retry without inserting anything into the conversation log
// itself — see initChatbot's sendWithRetry).
function toast(message, type = "info", opts = {}) {
  const children = [document.createTextNode(message)];
  if (opts.action && opts.action.label && typeof opts.action.onClick === "function") {
    children.push(el("button", {
      type: "button",
      class: "toast-action",
      text: opts.action.label,
      onclick: () => { opts.action.onClick(); node.remove(); },
    }));
  }
  const node = el("div", { class: "toast toast-" + type }, children);
  toastRoot.appendChild(node);
  const life = opts.action ? 8000 : 3200;
  setTimeout(() => {
    node.style.transition = "opacity 0.2s ease";
    node.style.opacity = "0";
    setTimeout(() => node.remove(), 200);
  }, life);
}
const toastSuccess = (msg) => toast(msg, "success");
const toastError = (msg, opts) => toast(msg, "error", opts);

// -- confirm dialog --------------------------------------------------------
// Replaces native confirm() with a themed modal, returning a Promise<bool>.

const modalRoot = document.getElementById("modalRoot");

// modalA11y wires the dialog semantics + keyboard behavior shared by every
// modal overlay: role/aria-modal for assistive tech, Escape-to-close (when
// the modal permits dismissal — the emergency-kit modal deliberately opts
// out by passing no onEscape, since losing an unsaved recovery phrase to a
// stray Escape press is the one outcome that screen exists to prevent),
// and moving initial focus onto the dialog so keyboard users don't land
// back on whatever triggered it.
function modalA11y(overlay, onEscape) {
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "true");
  const focusable = overlay.querySelector("input, textarea, select, button");
  if (focusable) focusable.focus();
  if (!onEscape) return;
  function onKey(e) {
    if (e.key === "Escape") {
      document.removeEventListener("keydown", onKey);
      onEscape();
    }
  }
  document.addEventListener("keydown", onKey);
}

function confirmDialog(message, confirmLabel = "Delete") {
  return new Promise((resolve) => {
    clear(modalRoot);
    function close(result) {
      clear(modalRoot);
      resolve(result);
    }
    const overlay = el("div", { class: "modal-overlay", onclick: (e) => { if (e.target === overlay) close(false); } }, [
      el("div", { class: "modal-card" }, [
        el("p", { text: message }),
        el("div", { class: "modal-actions" }, [
          el("button", { class: "btn-secondary", text: "Cancel", onclick: () => close(false) }),
          el("button", { class: "btn-danger", text: confirmLabel, onclick: () => close(true) }),
        ]),
      ]),
    ]);
    modalRoot.appendChild(overlay);
    modalA11y(overlay, () => close(false));
  });
}

// -- loading-state button wrapper ---------------------------------------
// Wraps an async action: disables the button and shows a spinner in place
// of its label for the duration of the call, restores it afterward
// (success or failure) — every submit button in this dashboard uses this.

function withLoading(button, fn) {
  return async (...args) => {
    const originalChildren = Array.from(button.childNodes);
    button.disabled = true;
    clear(button);
    button.appendChild(el("span", { class: "spinner" }));
    button.appendChild(document.createTextNode(" " + (button.dataset.loadingLabel || "Working...")));
    try {
      await fn(...args);
    } finally {
      button.disabled = false;
      clear(button);
      for (const c of originalChildren) button.appendChild(c);
    }
  };
}

function actionButton(label, attrs, handler) {
  attrs = attrs || {};
  const { loadingLabel, ...domAttrs } = attrs;
  const btn = el("button", Object.assign({ text: label }, domAttrs));
  btn.dataset.loadingLabel = loadingLabel || label + "...";
  btn.addEventListener("click", withLoading(btn, handler));
  return btn;
}

// deleteButton shows the confirm dialog in the button's *normal* state —
// the spinner/disabled loading state only starts after the user actually
// confirms, not while they're still deciding (a button that looks like
// it's already deleting before you've confirmed is a real bug, not just
// a cosmetic one).
function deleteButton(label, confirmMessage, fn) {
  const btn = el("button", { class: "btn-danger", text: label });
  btn.dataset.loadingLabel = "Deleting...";
  const runWithLoading = withLoading(btn, fn);
  btn.addEventListener("click", async () => {
    const ok = await confirmDialog(confirmMessage, label);
    if (!ok) return;
    await runWithLoading();
  });
  return btn;
}

// -- theme toggle ----------------------------------------------------------

function applyTheme(theme) {
  if (theme === "light" || theme === "dark") {
    document.documentElement.setAttribute("data-theme", theme);
  } else {
    document.documentElement.removeAttribute("data-theme");
  }
}
applyTheme(localStorage.getItem(THEME_KEY));

document.getElementById("themeToggle").addEventListener("click", () => {
  const current = localStorage.getItem(THEME_KEY);
  const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  const currentlyDark = current === "dark" || (!current && prefersDark);
  const next = currentlyDark ? "light" : "dark";
  localStorage.setItem(THEME_KEY, next);
  applyTheme(next);
});

// -- router --------------------------------------------------------------

const shell = document.getElementById("shell");
const loginRoot = document.getElementById("loginRoot");
const app = document.getElementById("app");

// dashboardContext mirrors "what page/collection/record is the admin
// currently looking at" — kept in sync by navigate() on every route
// change (and by showRecordFormModal while a record is open in a modal)
// — sent on every /api/chat request (see the chatbot panel below) so the
// assistant has real workspace awareness instead of the admin having to
// repeat what's already on screen. See internal/server/chatbot_context.go
// for how the backend uses this.
const dashboardContext = { page: "", collection: "", record_id: "" };
// 10 turns = 5 user/assistant exchanges — kept in sync with the backend's
// own cap (maxChatHistoryTurns in internal/server/chatbot_context.go); see
// that constant's doc comment for why this is a hard cap rather than a
// summarization call.
const maxChatHistoryTurns = 10;

// Conversation transcripts used to live in a single in-memory array; they
// now live in localStorage as a small set of named conversations (see
// initChatbot below) so the widget can offer a ChatGPT/Claude-style
// history panel without any backend changes. Keys versioned ("_v1") so a
// future incompatible shape change can migrate cleanly instead of crashing
// on old data.
const CHAT_STORE_KEY = "onebox_chat_conversations_v1";
const CHAT_ACTIVE_KEY = "onebox_chat_active_conversation_v1";
const CHAT_SIZE_KEY = "onebox_chat_widget_size_v1";
const MAX_STORED_CONVERSATIONS = 40;

// -- multimodal chat attachments -------------------------------------------
// Mirrors chatAttachmentExtensions in internal/server/chat_attachments.go —
// kept in sync by hand (the same relationship maxChatHistoryTurns already
// has with its backend counterpart): the accept="" attribute below is only
// a picker-dialog filter hint, not real validation, so ATTACHMENT_EXT_RE is
// what actually gates drag-and-drop/paste/file-picker uploads client-side.
// The backend re-validates independently regardless (see
// handleUploadChatAttachment) — this is purely to avoid a wasted upload
// round trip for an obviously-unsupported file.
const ATTACHMENT_ACCEPT = ".png,.jpg,.jpeg,.pdf,.docx,.txt,.md,.csv,.xlsx";
const ATTACHMENT_EXT_RE = /\.(png|jpe?g|pdf|docx|txt|md|csv|xlsx)$/i;
const ATTACHMENT_IMAGE_EXT_RE = /\.(png|jpe?g)$/i;
const MAX_ATTACHMENTS_PER_MESSAGE = 6;

// Reassigned by initChatbot() below; performLogout calls it so a shared
// machine never shows one admin's chat transcript to the next login.
let resetChatbotForLogout = () => {};

// Populate the sidebar's version footer once at load — lets a self-hoster
// (or anyone debugging a "why does this look old" report) confirm which
// build is actually running without checking the binary from a shell.
fetch("/api/health")
  .then((r) => r.json())
  .then((body) => {
    const el = document.getElementById("versionFooter");
    if (el && body && body.version) el.textContent = "v" + body.version;
  })
  .catch(() => {});

// The floating admin chatbot — built once at load (its container is
// hidden/shown per role by applyRoleVisibility, not re-built on every
// navigate() call).
(function initChatbot() {
  const root = document.getElementById("chatbotRoot");

  // -- conversation store (localStorage) -----------------------------------
  function loadConversations() {
    try {
      const raw = JSON.parse(localStorage.getItem(CHAT_STORE_KEY) || "[]");
      return Array.isArray(raw) ? raw : [];
    } catch (e) {
      return [];
    }
  }
  function saveConversations(list) {
    try {
      localStorage.setItem(CHAT_STORE_KEY, JSON.stringify(list.slice(-MAX_STORED_CONVERSATIONS)));
    } catch (e) {
      // localStorage full or unavailable (private browsing, quota, etc.) —
      // the widget still works for this tab, it just won't persist.
    }
  }
  function uid() { return Date.now().toString(36) + Math.random().toString(36).slice(2, 8); }
  function newConversation() { return { id: uid(), title: "New conversation", messages: [], updatedAt: Date.now() }; }

  let conversations = loadConversations();
  let activeId = localStorage.getItem(CHAT_ACTIVE_KEY) || "";
  if (!conversations.some((c) => c.id === activeId)) {
    if (conversations.length === 0) conversations.push(newConversation());
    activeId = conversations[conversations.length - 1].id;
  }
  function activeConversation() { return conversations.find((c) => c.id === activeId) || conversations[0]; }
  function persist() {
    saveConversations(conversations);
    try { localStorage.setItem(CHAT_ACTIVE_KEY, activeId); } catch (e) {}
  }
  function titleFromFirstMessage(text) {
    const trimmed = text.trim().replace(/\s+/g, " ");
    return trimmed.length > 40 ? trimmed.slice(0, 40) + "…" : trimmed || "New conversation";
  }

  // -- widget chrome (built once at load, never rebuilt per-message) -------
  const fab = el("button", { type: "button", class: "chatbot-fab", title: "Ask about this onebox instance", "aria-label": "Ask about this onebox instance" }, [icon("chat", { size: 22 })]);

  const log = el("div", { class: "chatbot-log" });
  const emptyHint = el("div", { class: "chatbot-empty", text: "Ask anything about your collections, files, or documents." });
  const typingRow = el("div", { class: "chatbot-msg assistant chatbot-typing hidden" }, [
    el("span", { class: "chatbot-dot" }), el("span", { class: "chatbot-dot" }), el("span", { class: "chatbot-dot" }),
  ]);

  const historyList = el("div", { class: "chatbot-history-list" });
  const newChatBtn = el("button", { type: "button", class: "icon-btn", title: "New conversation", "aria-label": "New conversation" }, [icon("plus", { size: 14 })]);
  const historyPanel = el("div", { class: "chatbot-history hidden" }, [
    el("div", { class: "chatbot-history-header" }, ["Conversations", newChatBtn]),
    historyList,
  ]);

  const textarea = el("textarea", { rows: "1", placeholder: "Ask a question… (Enter to send, Shift+Enter for a new line)" });
  const sendBtn = el("button", { type: "submit", class: "chatbot-send", text: "Send" });
  // -- attachments: composer chrome (logic lives in the "attachments"
  // section below, near autoGrow) — file picker button + hidden input +
  // the row of in-progress/uploaded chips shown above the textarea.
  const attachmentsRow = el("div", { class: "chatbot-attachments-row hidden" });
  // data-testid: a stable hook for scripts/browser-verify — see
  // ARCHITECTURE.md §15. This is the ONE attachment file input the chat
  // composer ever creates (built once here, in initChatbot's IIFE); if a
  // future change adds another <input type=file> anywhere on the page,
  // this attribute keeps automated checks pointed at the right one
  // instead of silently matching whichever input happens to come first.
  const attachInput = el("input", {
    type: "file", multiple: "multiple", accept: ATTACHMENT_ACCEPT, class: "hidden",
    "data-testid": "chat-attachment-input",
  });
  const attachBtn = el("button", {
    type: "button", class: "icon-btn chatbot-attach-btn", title: "Attach a file", "aria-label": "Attach a file",
    onclick: () => attachInput.click(),
  }, [icon("files", { size: 16 })]);
  const inputRow = el("div", { class: "chatbot-input-row" }, [attachBtn, textarea, sendBtn]);
  const form = el("form", { class: "chatbot-form" }, [attachmentsRow, inputRow, attachInput]);
  const dropOverlay = el("div", { class: "chatbot-drop-overlay hidden" }, [icon("files", { size: 22 }), el("span", { text: "Drop to attach" })]);

  const historyToggleBtn = el("button", { type: "button", class: "icon-btn", title: "Conversation history", "aria-label": "Conversation history", onclick: () => toggleHistory() }, [icon("history", { size: 15 })]);
  const minimizeBtn = el("button", { type: "button", class: "icon-btn", title: "Minimize", "aria-label": "Minimize" }, [icon("minimize", { size: 14 })]);
  const maximizeBtn = el("button", { type: "button", class: "icon-btn", title: "Maximize", "aria-label": "Maximize" }, [icon("maximize", { size: 14 })]);
  const closeBtn = el("button", { type: "button", class: "icon-btn", title: "Close chat", "aria-label": "Close chat", onclick: () => closePanel() }, [icon("close", { size: 14 })]);

  const headerTitle = el("span", { class: "chatbot-panel-title", text: "Ask about your OneBox" });
  const header = el("div", { class: "chatbot-panel-header" }, [
    headerTitle,
    el("div", { class: "chatbot-panel-actions" }, [historyToggleBtn, minimizeBtn, maximizeBtn, closeBtn]),
  ]);

  const body = el("div", { class: "chatbot-body" }, [historyPanel, el("div", { class: "chatbot-main" }, [log, form])]);
  const panel = el("div", { class: "chatbot-panel hidden" }, [header, body, dropOverlay]);
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-label", "Ask about your OneBox");

  root.appendChild(panel);
  root.appendChild(fab);

  // -- size/state persistence: normal / maximized / minimized -------------
  const DEFAULT_SIZE = { state: "normal", width: 420, height: 620 };
  function safeReadSize() {
    try { return JSON.parse(localStorage.getItem(CHAT_SIZE_KEY) || "{}"); } catch (e) { return {}; }
  }
  let sizeState = Object.assign({}, DEFAULT_SIZE, safeReadSize());
  function saveSize() { try { localStorage.setItem(CHAT_SIZE_KEY, JSON.stringify(sizeState)); } catch (e) {} }

  function applySizeState() {
    panel.classList.remove("state-normal", "state-maximized", "state-minimized");
    panel.classList.add("state-" + sizeState.state);
    maximizeBtn.replaceChildren(icon(sizeState.state === "maximized" ? "restore" : "maximize", { size: 14 }));
    maximizeBtn.title = sizeState.state === "maximized" ? "Restore" : "Maximize";
    maximizeBtn.setAttribute("aria-label", maximizeBtn.title);
    if (sizeState.state === "normal") {
      panel.style.width = (sizeState.width || DEFAULT_SIZE.width) + "px";
      panel.style.height = (sizeState.height || DEFAULT_SIZE.height) + "px";
      panel.style.left = "";
      panel.style.top = "";
    } else if (sizeState.state === "maximized") {
      panel.style.width = "";
      panel.style.height = "";
      if (sizeState.left != null && sizeState.top != null) {
        panel.style.left = sizeState.left + "px";
        panel.style.top = sizeState.top + "px";
      } else {
        panel.style.left = "";
        panel.style.top = "";
      }
    } else {
      panel.style.width = (sizeState.width || DEFAULT_SIZE.width) + "px";
      panel.style.height = "";
      panel.style.left = "";
      panel.style.top = "";
    }
  }
  applySizeState();

  minimizeBtn.addEventListener("click", () => {
    sizeState.state = sizeState.state === "minimized" ? "normal" : "minimized";
    saveSize();
    applySizeState();
  });
  maximizeBtn.addEventListener("click", () => {
    sizeState.state = sizeState.state === "maximized" ? "normal" : "maximized";
    saveSize();
    applySizeState();
    scrollToBottom(true);
  });

  // Persists a manual resize made via the native `resize: both` handle
  // (normal state only) — ResizeObserver only fires on real size changes,
  // no polling needed.
  const resizeObserver = new ResizeObserver(() => {
    if (sizeState.state !== "normal") return;
    const rect = panel.getBoundingClientRect();
    sizeState.width = Math.round(rect.width);
    sizeState.height = Math.round(rect.height);
    saveSize();
  });
  resizeObserver.observe(panel);

  // Dragging repositions the panel only in maximized mode — normal mode
  // stays anchored bottom-right like a typical launcher and is resized via
  // the native corner handle instead.
  let dragging = null;
  header.addEventListener("pointerdown", (e) => {
    if (sizeState.state !== "maximized" || e.target.closest("button")) return;
    const rect = panel.getBoundingClientRect();
    dragging = { dx: e.clientX - rect.left, dy: e.clientY - rect.top };
    header.setPointerCapture(e.pointerId);
  });
  header.addEventListener("pointermove", (e) => {
    if (!dragging) return;
    const left = Math.min(Math.max(0, e.clientX - dragging.dx), window.innerWidth - panel.offsetWidth);
    const top = Math.min(Math.max(0, e.clientY - dragging.dy), window.innerHeight - panel.offsetHeight);
    panel.style.left = left + "px";
    panel.style.top = top + "px";
    sizeState.left = left;
    sizeState.top = top;
  });
  header.addEventListener("pointerup", () => { if (dragging) { dragging = null; saveSize(); } });

  function closePanel() {
    panel.classList.add("hidden");
    fab.focus();
  }

  // -- scrolling -------------------------------------------------------------
  // "Preserve scroll while generating": a new message only yanks the view
  // down if the admin was already at (or near) the bottom — if they've
  // scrolled up to read earlier context, it stays put.
  function isNearBottom() { return log.scrollHeight - log.scrollTop - log.clientHeight < 60; }
  function scrollToBottom(force) {
    if (!force && !isNearBottom()) return;
    log.scrollTo({ top: log.scrollHeight, behavior: "smooth" });
  }

  // -- markdown rendering (dependency-free, DOM-only — never innerHTML) ----
  // OneBox is a self-hosted, potentially-offline single binary, so this
  // deliberately doesn't pull a markdown/highlighting library from a CDN —
  // everything below builds real DOM nodes via el()/textContent, which also
  // means LLM-generated (or RAG-influenced) text can never inject HTML.
  function copyToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
    } else {
      fallbackCopy(text);
    }
  }
  function fallbackCopy(text) {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (e) {}
    ta.remove();
  }

  function renderInline(container, text) {
    const pattern = /`([^`]+)`|\*\*([^*]+)\*\*|\*([^*]+)\*|\[([^\]]+)\]\(([^)]+)\)/;
    let rest = text;
    while (rest) {
      const m = pattern.exec(rest);
      if (!m) { container.appendChild(document.createTextNode(rest)); break; }
      if (m.index > 0) container.appendChild(document.createTextNode(rest.slice(0, m.index)));
      if (m[1] !== undefined) container.appendChild(el("code", { class: "chatbot-inline-code", text: m[1] }));
      else if (m[2] !== undefined) container.appendChild(el("strong", { text: m[2] }));
      else if (m[3] !== undefined) container.appendChild(el("em", { text: m[3] }));
      else if (m[4] !== undefined) container.appendChild(el("a", { href: m[5], target: "_blank", rel: "noopener noreferrer", text: m[4] }));
      rest = rest.slice(m.index + m[0].length);
    }
  }

  function renderCodeBlock(code, lang) {
    const copyLabel = el("span", { text: "Copy" });
    const copyBtn = el("button", { type: "button", class: "code-copy-btn" }, [icon("copy", { size: 12 }), copyLabel]);
    copyBtn.addEventListener("click", () => {
      copyToClipboard(code);
      copyLabel.textContent = "Copied";
      setTimeout(() => { copyLabel.textContent = "Copy"; }, 1400);
    });
    const head = el("div", { class: "code-block-head" }, [el("span", { class: "code-lang", text: lang || "text" }), copyBtn]);
    return el("div", { class: "code-block" }, [head, el("pre", {}, [el("code", { text: code })])]);
  }

  function renderMarkdown(container, text) {
    const lines = String(text || "").replace(/\r\n/g, "\n").split("\n");
    let i = 0;
    let list = null; // { node, ordered }
    function closeList() { list = null; }
    while (i < lines.length) {
      const line = lines[i];
      const fence = line.match(/^```(\w*)\s*$/);
      if (fence) {
        closeList();
        const lang = fence[1];
        const buf = [];
        i++;
        while (i < lines.length && !/^```\s*$/.test(lines[i])) { buf.push(lines[i]); i++; }
        i++; // skip closing fence
        container.appendChild(renderCodeBlock(buf.join("\n"), lang));
        continue;
      }
      if (!line.trim()) { closeList(); i++; continue; }
      const heading = line.match(/^(#{1,3})\s+(.*)$/);
      if (heading) {
        closeList();
        const h = el("h" + Math.min(4, heading[1].length + 1), {});
        renderInline(h, heading[2]);
        container.appendChild(h);
        i++;
        continue;
      }
      const bullet = line.match(/^\s*[-*]\s+(.*)$/);
      const ordered = line.match(/^\s*\d+\.\s+(.*)$/);
      if (bullet || ordered) {
        const tag = ordered ? "ol" : "ul";
        if (!list || list.ordered !== !!ordered) {
          list = { node: el(tag, {}), ordered: !!ordered };
          container.appendChild(list.node);
        }
        const li = el("li", {});
        renderInline(li, (bullet || ordered)[1]);
        list.node.appendChild(li);
        i++;
        continue;
      }
      closeList();
      const p = el("p", {});
      renderInline(p, line);
      container.appendChild(p);
      i++;
    }
  }

  // -- Proposal Cards ---------------------------------------------------------
  // RC4: renders a proposedAction the model called but autoExecutable
  // (chatbot_tool_execution.go) did NOT run automatically — either
  // destructive (needs confirmation) or with no execution primitive on the
  // backend at all (see notExecutedResult). Mirrors the React AI
  // Workspace's ProposalCard (web/src/components/ai/proposal-card.tsx):
  // delete_collection/delete_field genuinely execute here, by calling the
  // exact same DELETE/PATCH endpoints a hand-typed request would hit —
  // never a separate "approve" endpoint, since none exists (see
  // chatbot_context.go's proposedAction doc comment for why). Every other
  // type stays honest about not being executable from chat yet. Previously
  // a permanently-disabled stub ("Not called by anything today") — the
  // classic dashboard's chat panel silently dropped every proposal it got
  // back from /api/chat, so a destructive action the model proposed had no
  // way to actually happen from here at all, unlike the React dashboard.
  function renderActionCard(action) {
    const payload = action.payload || {};
    let done = false;

    const buttons = el("div", { class: "chatbot-action-buttons" });
    const errorLine = el("div", { class: "chatbot-action-error hidden" });

    function markDone(successMessage) {
      done = true;
      clear(buttons);
      buttons.appendChild(el("span", { class: "chatbot-action-done", text: "✓ Done" }));
      toastSuccess(successMessage);
    }
    function markFailed(err) {
      errorLine.textContent = err instanceof Error ? err.message : String(err);
      errorLine.classList.remove("hidden");
    }

    if (action.type === "delete_collection" && payload.name) {
      const btn = el("button", { type: "button", class: "btn btn-sm btn-danger", text: "Confirm delete" });
      btn.addEventListener("click", async () => {
        const ok = await confirmDialog(`Delete collection "${payload.name}"? This permanently deletes it and every record in it.`, "Delete");
        if (!ok) return;
        btn.disabled = true;
        try {
          await api("/api/collections/" + encodeURIComponent(payload.name), { method: "DELETE" });
          markDone(`Deleted collection "${payload.name}"`);
        } catch (err) {
          btn.disabled = false;
          markFailed(err);
        }
      });
      buttons.appendChild(btn);
    } else if (action.type === "delete_field" && payload.collection && payload.field) {
      const btn = el("button", { type: "button", class: "btn btn-sm btn-danger", text: "Confirm delete field" });
      btn.addEventListener("click", async () => {
        const ok = await confirmDialog(`Delete field "${payload.field}" from "${payload.collection}"? This deletes that field's data for every record.`, "Delete");
        if (!ok) return;
        btn.disabled = true;
        try {
          const col = await api("/api/collections/" + encodeURIComponent(payload.collection));
          const fields = (col.schema.fields || []).filter((f) => f.name !== payload.field);
          await api("/api/collections/" + encodeURIComponent(payload.collection), { method: "PATCH", body: JSON.stringify({ fields }) });
          markDone(`Removed field "${payload.field}" from "${payload.collection}"`);
        } catch (err) {
          btn.disabled = false;
          markFailed(err);
        }
      });
      buttons.appendChild(btn);
    } else {
      buttons.appendChild(el("span", {
        class: "chatbot-action-nohandler",
        text: action.destructive
          ? "Needs confirmation, but OneBox doesn't execute this type from chat yet — do it from the collection page."
          : "Not executed — do it from the collection page.",
      }));
    }

    const collectionLink = typeof payload.collection === "string" ? payload.collection : typeof payload.name === "string" ? payload.name : null;
    if (collectionLink) {
      buttons.appendChild(el("a", { href: "#/records/" + encodeURIComponent(collectionLink), class: "btn btn-sm btn-secondary", text: "Open " + collectionLink }));
    }

    return el("div", { class: "chatbot-action-card" + (action.destructive ? " destructive" : "") }, [
      el("div", { class: "chatbot-proposal-eyebrow", text: "Proposal" }),
      el("div", { class: "chatbot-action-header" }, [
        el("span", { class: "chatbot-action-title", text: action.title }),
        action.destructive ? el("span", { class: "chatbot-action-destructive-badge", text: "Destructive" }) : null,
      ].filter(Boolean)),
      el("div", { class: "chatbot-action-desc", text: action.description }),
      buttons,
      errorLine,
    ]);
  }

  // actionsNode/appendActionsIfAny split the same way attachments do: one
  // path renders inline during buildMessageNode (history replay, or a
  // reply that never streamed at all — actions are already known by the
  // time the node is first built), the other appends post-hoc once a
  // streaming reply's node already exists on screen before its actions
  // arrive on the final SSE event (see runTurn).
  function actionsNode(msg) {
    if (!msg.actions || !msg.actions.length) return null;
    const wrap = el("div", { class: "chatbot-actions-container" });
    msg.actions.forEach((a) => wrap.appendChild(renderActionCard(a)));
    return wrap;
  }
  function appendActionsIfAny(node, msg) {
    const wrap = actionsNode(msg);
    if (!wrap) return;
    const timeEl = node.querySelector(".chatbot-msg-time");
    if (timeEl) node.insertBefore(wrap, timeEl); else node.appendChild(wrap);
  }

  // -- message rendering: keyed, append-only — no full-log rerender per msg -
  const messageNodes = new Map();
  function formatTime(ts) {
    try { return new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }); } catch (e) { return ""; }
  }
  // renderBubbleContent is shared by buildMessageNode (first render) and
  // updateMessageContent (streaming re-render as deltas arrive) so both
  // stay in sync with exactly one rendering rule.
  function renderBubbleContent(bubbleContent, msg) {
    clear(bubbleContent);
    if (msg.role === "assistant") renderMarkdown(bubbleContent, msg.content);
    else bubbleContent.textContent = msg.content;
  }
  function buildMessageNode(msg) {
    const bubbleContent = el("div", { class: "chatbot-bubble-content" });
    renderBubbleContent(bubbleContent, msg);
    const children = [];
    // Attachment chips render as a fixed sibling row, never touched by
    // updateMessageContent's streaming re-renders (only bubbleContent is —
    // attachments belong to the admin's own messages, which never stream).
    if (msg.attachments && msg.attachments.length) {
      const row = el("div", { class: "chatbot-msg-attachments" });
      msg.attachments.forEach((att) => row.appendChild(attachmentChipNode(att, false)));
      children.push(row);
    }
    children.push(bubbleContent);
    const actions = actionsNode(msg);
    if (actions) children.push(actions);
    children.push(el("div", { class: "chatbot-msg-time", text: formatTime(msg.ts) }));
    return el("div", { class: "chatbot-msg " + msg.role }, children);
  }
  // updateMessageContent re-renders an already-appended message's bubble in
  // place (mutates the existing DOM node instead of rebuilding it) — used
  // while a streaming reply's content is still growing, see runTurn below.
  function updateMessageContent(node, msg) {
    const bubbleContent = node.querySelector(".chatbot-bubble-content");
    if (bubbleContent) renderBubbleContent(bubbleContent, msg);
  }
  // typingRow is kept permanently mounted as log's last child (inserted
  // once below, in renderActiveConversation, and never removed again — only
  // hidden/shown via its "hidden" class) so it can act as a stable anchor
  // for insertBefore. appendMessage relies on that invariant; it must never
  // run before typingRow has been (re-)attached, which is why
  // renderActiveConversation attaches it first, ahead of replaying messages.
  function appendMessage(msg) {
    if (emptyHint.parentNode) emptyHint.remove();
    const node = buildMessageNode(msg);
    messageNodes.set(msg.id, node);
    log.insertBefore(node, typingRow);
    scrollToBottom(isNearBottom());
    return node;
  }
  function renderActiveConversation() {
    clear(log);
    messageNodes.clear();
    log.appendChild(typingRow);
    typingRow.classList.add("hidden");
    const conv = activeConversation();
    if (!conv || conv.messages.length === 0) {
      log.insertBefore(emptyHint, typingRow);
    } else {
      conv.messages.forEach((m) => appendMessage(m));
    }
    scrollToBottom(true);
    headerTitle.textContent = conv ? conv.title : "Ask about your OneBox";
  }
  function setTyping(on) {
    typingRow.classList.toggle("hidden", !on);
    if (on) { if (emptyHint.parentNode) emptyHint.remove(); scrollToBottom(true); }
  }

  // -- history panel ---------------------------------------------------------
  function renderHistoryList() {
    clear(historyList);
    conversations
      .slice()
      .sort((a, b) => b.updatedAt - a.updatedAt)
      .forEach((conv) => {
        const item = el("button", { type: "button", class: "chatbot-history-item" + (conv.id === activeId ? " active" : "") }, [
          el("div", { class: "chatbot-history-item-title", text: conv.title }),
          el("div", { class: "chatbot-history-item-time", text: new Date(conv.updatedAt).toLocaleString() }),
        ]);
        item.addEventListener("click", () => {
          activeId = conv.id;
          persist();
          renderActiveConversation();
          renderHistoryList();
          toggleHistory(false);
        });
        historyList.appendChild(item);
      });
  }
  function toggleHistory(force) {
    const show = force !== undefined ? force : historyPanel.classList.contains("hidden");
    historyPanel.classList.toggle("hidden", !show);
    if (show) renderHistoryList();
  }
  newChatBtn.addEventListener("click", () => {
    const conv = newConversation();
    conversations.push(conv);
    activeId = conv.id;
    persist();
    renderActiveConversation();
    renderHistoryList();
    textarea.focus();
  });

  // -- composer: auto-grow textarea (≤8 lines), Enter to send, Shift+Enter --
  const LINE_HEIGHT_PX = 20, MAX_LINES = 8;
  function autoGrow() {
    textarea.style.height = "auto";
    const maxHeight = LINE_HEIGHT_PX * MAX_LINES + 16;
    textarea.style.height = Math.min(textarea.scrollHeight, maxHeight) + "px";
    textarea.style.overflowY = textarea.scrollHeight > maxHeight ? "auto" : "hidden";
  }
  textarea.addEventListener("input", autoGrow);
  textarea.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      form.requestSubmit();
    }
  });

  // -- attachments: drag-and-drop, paste, file picker, upload progress ------
  // pendingAttachments holds the CURRENT (not-yet-sent) message's
  // attachments — each: { localId, id, filename, mime, size, kind,
  // status: "uploading"|"done"|"error", progress (0-1), xhr, previewUrl }.
  // id/mime/size/kind get overwritten from the server's response once the
  // upload finishes (see uploadAttachment) — filename/kind start as a
  // client-side guess so the chip renders instantly, before the round trip
  // completes.
  let pendingAttachments = [];

  function renderAttachmentsRow() {
    clear(attachmentsRow);
    attachmentsRow.classList.toggle("hidden", pendingAttachments.length === 0);
    pendingAttachments.forEach((att) => {
      attachmentsRow.appendChild(attachmentChipNode(att, true));
    });
  }

  // attachmentChipNode is shared by the live composer row above and by
  // buildMessageNode below (for chips on an already-sent message) — one
  // rendering rule for "what an attachment looks like," same discipline as
  // renderBubbleContent being shared by buildMessageNode/updateMessageContent.
  function attachmentChipNode(att, removable) {
    const thumb = el("span", { class: "attachment-chip-icon" });
    if (att.kind === "image" && att.previewUrl) {
      // Still-composing attachment: the local blob preview from the File
      // object picked/dropped/pasted, no round trip needed.
      clear(thumb);
      thumb.appendChild(el("img", { class: "attachment-chip-thumb", src: att.previewUrl, alt: "" }));
    } else if (att.kind === "image" && att.id) {
      // An already-sent message's image chip (this session's blob preview
      // didn't survive — conversations persist to localStorage as plain
      // JSON, and a blob: URL doesn't survive that round trip) — resolve
      // the real thumbnail the same authenticated-fetch-then-swap way
      // fillAvatarNode does for avatars (see resolveAvatarURL).
      thumb.appendChild(icon("sparkle", { size: 14 }));
      resolveAvatarURL(att.id).then((url) => {
        if (!url) return;
        clear(thumb);
        thumb.appendChild(el("img", { class: "attachment-chip-thumb", src: url, alt: "" }));
      });
    } else {
      thumb.appendChild(icon("files", { size: 14 }));
    }
    let statusText;
    if (att.status === "uploading") statusText = `Uploading… ${Math.round((att.progress || 0) * 100)}%`;
    else if (att.status === "error") statusText = "Upload failed";
    else statusText = formatBytes(att.size);
    const meta = el("div", { class: "attachment-chip-meta" }, [
      el("div", { class: "attachment-chip-name", text: att.filename }),
      el("div", { class: "attachment-chip-status", text: statusText }),
    ]);
    const children = [thumb, meta];
    if (att.status === "uploading") {
      children.push(el("div", { class: "attachment-chip-progress" }, [
        el("div", { class: "attachment-chip-progress-bar", style: `width:${Math.round((att.progress || 0) * 100)}%` }),
      ]));
    }
    if (removable) {
      children.push(el("button", {
        type: "button", class: "attachment-chip-remove", title: "Remove", "aria-label": "Remove attachment",
        onclick: () => removeAttachment(att),
      }, [icon("close", { size: 10 })]));
    }
    return el("div", { class: "attachment-chip" + (att.status === "error" ? " error" : "") }, children);
  }

  function removeAttachment(att) {
    pendingAttachments = pendingAttachments.filter((a) => a.localId !== att.localId);
    if (att.status === "uploading" && att.xhr) att.xhr.abort();
    else if (att.status === "done" && att.id) api("/api/chat-attachments/" + att.id, { method: "DELETE" }).catch(() => {});
    if (att.previewUrl) URL.revokeObjectURL(att.previewUrl);
    renderAttachmentsRow();
  }

  function uploadAttachment(file) {
    const att = {
      localId: uid(), id: null, filename: file.name, mime: file.type,
      size: file.size, kind: ATTACHMENT_IMAGE_EXT_RE.test(file.name) ? "image" : "document",
      status: "uploading", progress: 0, xhr: null,
      previewUrl: ATTACHMENT_IMAGE_EXT_RE.test(file.name) ? URL.createObjectURL(file) : null,
    };
    pendingAttachments.push(att);
    renderAttachmentsRow();

    uploadFileWithProgress(
      file,
      (frac) => { att.progress = frac; renderAttachmentsRow(); },
      "/api/chat-attachments",
      (xhr) => { att.xhr = xhr; }
    )
      .then((rec) => {
        att.id = rec.id;
        att.filename = rec.filename;
        att.mime = rec.mime;
        att.size = rec.size;
        att.kind = rec.kind;
        att.status = "done";
        renderAttachmentsRow();
      })
      .catch((err) => {
        // Already-removed (aborted) attachments shouldn't resurrect an
        // error toast — only report a failure for one still pending.
        if (!pendingAttachments.some((a) => a.localId === att.localId)) return;
        att.status = "error";
        renderAttachmentsRow();
        toastError(`"${file.name}" failed to upload: ` + err.message);
      });
  }

  function handleFiles(fileList) {
    const files = Array.from(fileList || []);
    if (files.length === 0) return;
    for (const file of files) {
      if (pendingAttachments.length >= MAX_ATTACHMENTS_PER_MESSAGE) {
        toastError(`You can attach up to ${MAX_ATTACHMENTS_PER_MESSAGE} files per message.`);
        break;
      }
      if (!ATTACHMENT_EXT_RE.test(file.name)) {
        toastError(`"${file.name}" isn't a supported attachment type.`);
        continue;
      }
      uploadAttachment(file);
    }
  }

  attachInput.addEventListener("change", () => {
    handleFiles(attachInput.files);
    attachInput.value = "";
  });

  // Drag-and-drop over the whole panel (not just the composer) — dragenter/
  // dragleave fire on every child boundary crossed, so a counter (rather
  // than a plain boolean) is needed to know when the pointer has actually
  // left the panel entirely vs. just crossed from one child into another.
  let dragDepth = 0;
  panel.addEventListener("dragenter", (e) => {
    if (!e.dataTransfer || !Array.from(e.dataTransfer.types || []).includes("Files")) return;
    e.preventDefault();
    dragDepth++;
    dropOverlay.classList.remove("hidden");
  });
  panel.addEventListener("dragover", (e) => {
    if (!e.dataTransfer || !Array.from(e.dataTransfer.types || []).includes("Files")) return;
    e.preventDefault();
  });
  panel.addEventListener("dragleave", () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) dropOverlay.classList.add("hidden");
  });
  panel.addEventListener("drop", (e) => {
    e.preventDefault();
    dragDepth = 0;
    dropOverlay.classList.add("hidden");
    if (e.dataTransfer && e.dataTransfer.files) handleFiles(e.dataTransfer.files);
  });

  // Paste: Ctrl+V with an image on the clipboard attaches it the same way
  // a drag-drop or file-picker upload would, instead of doing nothing (the
  // textarea's default paste behavior only handles text).
  textarea.addEventListener("paste", (e) => {
    const items = (e.clipboardData && e.clipboardData.items) || [];
    const files = [];
    for (const item of items) {
      if (item.kind === "file") {
        const file = item.getAsFile();
        if (file) files.push(file);
      }
    }
    if (files.length > 0) {
      e.preventDefault();
      handleFiles(files);
    }
  });

  // -- send + streaming + automatic retry ------------------------------------
  // Retryable: no HTTP status at all (network drop, DNS failure, offline),
  // or a 5xx from the server (Ollama busy, upstream timeout). A 4xx fails
  // fast — retrying a validation error just repeats the same rejection.
  function isRetryable(err) {
    if (!err) return false;
    if (err.status === undefined || err.status === null) return true;
    return err.status >= 500;
  }
  function sleep(ms) { return new Promise((resolve) => setTimeout(resolve, ms)); }

  // attachmentRefs strips a stored message's attachments down to
  // {id, filename, kind} — the shape chatHistoryTurn.Attachments and
  // chatbotRequest.AttachmentIDs expect — dropping local-only fields like
  // mime/size/previewUrl/status that the backend has no use for.
  function attachmentRefs(msg) {
    return (msg.attachments || []).map((a) => ({ id: a.id, filename: a.filename, kind: a.kind }));
  }

  // streamChatRequest reads POST /api/chat as Server-Sent Events (Accept:
  // text/event-stream — see streamChatReply in chatbot_handlers.go),
  // calling onDelta(text) as each chunk arrives and resolving with the full
  // reply once the server sends {done:true}. The endpoint and request body
  // are unchanged from the plain JSON path — only the Accept header opts
  // in — and error responses (still plain JSON from writeError) are parsed
  // the same way api() does, so callers get the same .status/.code
  // envelope either way. Falls back to a single non-streaming read if the
  // response isn't actually a stream (an error, or a browser that can't
  // read a fetch body incrementally) — same reasoning as the backend's own
  // non-flushable-writer fallback.
  //
  // userMsg is the full message object (not just its text) so its
  // attachments can be sent as attachment_ids — history is built from
  // every OTHER message in the conversation (userMsg itself is excluded by
  // id: it's already sent as the top-level message/attachment_ids fields,
  // and including it in history too would hand the model the same turn
  // twice, once in each shape).
  async function streamChatRequest(conv, userMsg, onDelta) {
    const headers = { "Content-Type": "application/json", Accept: "text/event-stream" };
    const token = getToken();
    if (token) headers["Authorization"] = "Bearer " + token;

    const res = await fetch("/api/chat", {
      method: "POST",
      headers,
      body: JSON.stringify({
        message: userMsg.content,
        context: dashboardContext,
        history: conv.messages
          .filter((m) => m.id !== userMsg.id)
          .slice(-maxChatHistoryTurns)
          .map((m) => ({ role: m.role, content: m.content, attachments: attachmentRefs(m) })),
        attachment_ids: attachmentRefs(userMsg).map((a) => a.id),
      }),
    });

    if (res.status === 401) {
      clearToken();
      clearRole();
      accountCache = null;
      location.hash = "#/login";
      throw new Error("Your session has expired — please log in again.");
    }

    const contentType = res.headers.get("content-type") || "";
    if (!res.ok || !contentType.includes("text/event-stream") || !res.body || !res.body.getReader) {
      const isJSON = contentType.includes("application/json");
      const body = isJSON ? await res.json() : await res.text();
      if (!res.ok) {
        const msg = isJSON && body && body.message ? body.message : String(body);
        const err = new Error(msg);
        if (isJSON && body && body.code) err.code = body.code;
        err.status = res.status;
        throw err;
      }
      const reply = isJSON && body && body.reply ? body.reply : "";
      if (reply) onDelta(reply);
      return { text: reply, actions: (isJSON && body && body.actions) || [], executedActions: (isJSON && body && body.executed_actions) || [] };
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    let full = "";
    let sawError = false;
    let actions = [];
    let executedActions = [];

    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let idx;
      while ((idx = buffer.indexOf("\n\n")) !== -1) {
        const rawEvent = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        const line = rawEvent.split("\n").find((l) => l.startsWith("data: "));
        if (!line) continue;
        let payload;
        try { payload = JSON.parse(line.slice(6)); } catch (e) { continue; }
        if (payload.delta) {
          full += payload.delta;
          onDelta(payload.delta);
        } else if (payload.error) {
          sawError = true;
        } else if (payload.done) {
          // Proposal Cards (payload.actions) and the Activity Panel feed
          // (payload.executed_actions) both ride on this same final event —
          // see streamDoneEvent's doc comment (chatbot_handlers.go). Kept
          // as plain locals rather than pushed straight onto the message
          // here since the caller (runTurn) is the one that knows whether
          // this turn's message node already exists (mid-stream) or still
          // needs to be created.
          actions = payload.actions || [];
          executedActions = payload.executed_actions || [];
        }
      }
    }

    if (sawError && !full) throw new Error("stream ended with no content");
    return { text: full, actions, executedActions };
  }

  // Never lets a raw backend/network error reach the UI: logs it to the
  // console (and it's already in the server's own logs), retries once
  // after ~1s if the failure looks transient AND nothing has streamed in
  // yet, and only rethrows — for the caller to turn into the single
  // friendly toast message — if both attempts fail. Once any delta has
  // arrived, retrying would duplicate/garble what's already shown, so a
  // failure past that point is never retried automatically here (matches
  // streamChatReply's own "never retry mid-stream" rule server-side).
  async function sendWithRetry(conv, userMsg, onDelta) {
    let gotAny = false;
    const wrappedDelta = (chunk) => { gotAny = true; onDelta(chunk); };
    try {
      return await streamChatRequest(conv, userMsg, wrappedDelta);
    } catch (err) {
      console.error("onebox: chat request failed (attempt 1 of 2):", err);
      if (gotAny || !isRetryable(err)) throw err;
      await sleep(1000);
      try {
        return await streamChatRequest(conv, userMsg, wrappedDelta);
      } catch (err2) {
        console.error("onebox: chat request failed (attempt 2 of 2):", err2);
        throw err2;
      }
    }
  }

  const FRIENDLY_ERROR = "I'm having trouble responding right now. Please try again in a moment.";

  async function runTurn(conv, userMsg) {
    if (sending) return;
    sending = true;
    textarea.disabled = true;
    sendBtn.disabled = true;
    setTyping(true);

    const assistantMsg = { id: uid(), role: "assistant", content: "", ts: Date.now() };
    let node = null; // created lazily on the first delta, so an instant reply doesn't flash an empty bubble before content exists

    function handleDelta(chunk) {
      if (!node) {
        setTyping(false);
        node = appendMessage(assistantMsg);
      }
      assistantMsg.content += chunk;
      updateMessageContent(node, assistantMsg);
      scrollToBottom(isNearBottom());
    }

    try {
      const result = await sendWithRetry(conv, userMsg, handleDelta);
      setTyping(false);
      assistantMsg.actions = result.actions || [];
      if (!node) {
        // Nothing streamed in (non-streaming fallback path, or a reply
        // that arrived as a single chunk) — render it now, same as the
        // old non-streaming flow always did. assistantMsg.actions is
        // already set, so buildMessageNode renders any Proposal Cards on
        // this same first pass.
        assistantMsg.content = result.text;
        node = appendMessage(assistantMsg);
      } else {
        // The node was already on screen from streaming deltas, before
        // actions were known (they only ride on the final SSE event) — add
        // them now rather than rebuilding the whole node.
        if (result.text && result.text !== assistantMsg.content) {
          assistantMsg.content = result.text;
          updateMessageContent(node, assistantMsg);
        }
        appendActionsIfAny(node, assistantMsg);
      }
      conv.messages.push(assistantMsg);
      conv.updatedAt = Date.now();
      persist();
    } catch (err) {
      setTyping(false);
      if (node && assistantMsg.content) {
        // Partial content already reached the screen — keep it visible and
        // save it rather than erase real progress; the rest of the reply
        // is just gone, logged here rather than shown as a raw error.
        console.error("onebox: stream ended early:", err);
        conv.messages.push(assistantMsg);
        conv.updatedAt = Date.now();
        persist();
      } else {
        // Errors never become a chat bubble — only a dismissible toast
        // with a one-click Retry, so the conversation transcript itself
        // always stays clean of backend/network noise.
        toastError(FRIENDLY_ERROR, { action: { label: "Retry", onClick: () => runTurn(conv, userMsg) } });
      }
    }

    sending = false;
    textarea.disabled = false;
    sendBtn.disabled = false;
    textarea.focus();
  }

  let sending = false;
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const message = textarea.value.trim();
    const readyAttachments = pendingAttachments.filter((a) => a.status === "done");
    // A message needs typed text OR at least one attachment, not always
    // both — dropping an image with nothing else to say is normal
    // ChatGPT-style behavior (see handleChatbot's matching relaxed check
    // server-side).
    if ((!message && readyAttachments.length === 0) || sending) return;
    if (pendingAttachments.some((a) => a.status === "uploading")) {
      toastError("Please wait for attachments to finish uploading.");
      return;
    }

    const conv = activeConversation();
    if (conv.messages.length === 0) {
      conv.title = titleFromFirstMessage(message || (readyAttachments[0] && readyAttachments[0].filename) || "");
    }
    const userMsg = {
      id: uid(), role: "user", content: message, ts: Date.now(),
      attachments: readyAttachments.map((a) => ({ id: a.id, filename: a.filename, kind: a.kind, mime: a.mime, size: a.size })),
    };
    conv.messages.push(userMsg);
    conv.updatedAt = Date.now();
    persist();
    appendMessage(userMsg);
    renderHistoryList();

    textarea.value = "";
    autoGrow();
    // The sent message's chip re-resolves its thumbnail via the
    // authenticated /api/files/:id fetch (see attachmentChipNode) rather
    // than these local blob previews, so it's safe — and necessary, to
    // avoid leaking them — to revoke every one now.
    pendingAttachments.forEach((a) => { if (a.previewUrl) URL.revokeObjectURL(a.previewUrl); });
    pendingAttachments = [];
    renderAttachmentsRow();
    runTurn(conv, userMsg);
  });

  fab.addEventListener("click", () => {
    panel.classList.toggle("hidden");
    if (!panel.classList.contains("hidden")) textarea.focus();
  });
  panel.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closePanel();
  });

  renderActiveConversation();

  resetChatbotForLogout = function () {
    conversations = [newConversation()];
    activeId = conversations[0].id;
    persist();
    renderActiveConversation();
    renderHistoryList();
    sizeState = Object.assign({}, DEFAULT_SIZE);
    saveSize();
    applySizeState();
    panel.classList.add("hidden");
  };
})();

// performLogout is shared by the sidebar's logout button (admin sessions)
// and renderUnauthorizedPage's logout button (user sessions blocked from
// the dashboard) — the same clear-and-redirect either way.
function performLogout() {
  clearToken();
  clearRole();
  accountCache = null;
  resetChatbotForLogout(); // don't leak this session's chat transcripts into the next login
  // Setting location.hash (when it actually changes) fires the
  // "hashchange" listener below, which calls navigate() itself — an
  // explicit extra call here would race it: both invocations are async
  // and can interleave, appending duplicate content to #app. See the
  // same reasoning at the other location.hash assignments in this file.
  location.hash = "#/login";
}

document.getElementById("logoutBtn").addEventListener("click", performLogout);

function currentRoute() {
  const hash = location.hash.replace(/^#\/?/, "");
  const parts = hash.split("/").filter(Boolean);
  return parts;
}

function updateActiveNav(routeName) {
  document.querySelectorAll("#sidebar a[data-route]").forEach((a) => {
    a.classList.toggle("active", a.dataset.route === routeName);
  });
}

// Sidebar nav links (and the whole shell) only ever render for an admin
// session — see navigate() below, which routes a non-admin session to
// renderUnauthorizedPage instead of here — so every link is always live;
// the only behavior left to bind is: clicking the link for the route
// you're already on doesn't change location.hash, so the "hashchange"
// listener never fires and the page would otherwise sit there stale
// (reported: Home's stat cards not refreshing) — force a re-render.
document.querySelectorAll('#sidebar a[href^="#/"]').forEach((a) => {
  a.addEventListener("click", (e) => {
    const targetHash = a.getAttribute("href");
    if (targetHash === (location.hash || "#/home")) {
      e.preventDefault();
      navigate();
    }
  });
});

// navGeneration guards against the login/signup race: both pages do an
// async GET /api/setup-status before rendering, so two hash changes in
// quick succession (e.g. logging out, then landing on #/signup) can have
// two navigate() calls in flight together — whichever's fetch resolves
// last would otherwise overwrite the correct, newer page with a stale
// one. Each render checks it's still the current navigation before
// touching the DOM; see renderLoginPage/renderSignupPage.
let navGeneration = 0;

async function navigate() {
  const myGen = ++navGeneration;
  const parts = currentRoute();

  // #/setup is checked before anything else, regardless of the current
  // visitor's own auth state: it always re-derives admin_exists itself
  // and redirects away the instant one exists (see renderSetupPage), so
  // it's truly permanently disabled after bootstrap rather than just
  // hidden from logged-out visitors — an edge case that matters because a
  // regular _user account can exist even before any admin does (nothing
  // requires bootstrapping the superuser first at the API level).
  if (parts[0] === "setup") {
    shell.classList.add("hidden");
    loginRoot.classList.remove("hidden");
    clear(loginRoot);
    await renderSetupPage(loginRoot, myGen);
    return;
  }

  const authed = !!getToken();
  // The dashboard shell is a Superuser surface, full stop — a signed-in
  // User account is a valid login, just not an authorized one here, so it
  // renders into loginRoot too (as renderUnauthorizedPage), never shell.
  const admin = authed && isAdminRole();
  shell.classList.toggle("hidden", !admin);
  loginRoot.classList.toggle("hidden", admin);

  if (!authed) {
    clear(loginRoot);
    if (parts[0] === "signup") await renderSignupPage(loginRoot, myGen);
    else if (parts[0] === "forgot-password") renderForgotPasswordPage(loginRoot);
    else await renderLoginPage(loginRoot, myGen);
    return;
  }
  if (!admin) {
    clear(loginRoot);
    renderUnauthorizedPage(loginRoot);
    return;
  }

  document.getElementById("chatbotRoot").classList.remove("hidden");
  refreshAccountSummary();

  clear(app);
  app.classList.remove("page-transition");
  void app.offsetWidth; // restart the entrance animation on every route change
  app.classList.add("page-transition");

  if (parts.length === 0) {
    location.hash = "#/home";
    return;
  }

  // Keep dashboardContext in sync with the route so the admin chatbot's
  // Workspace Awareness always reflects what's actually on screen —
  // record_id is set separately by showRecordFormModal, and reset here
  // since a route change means whatever modal was open no longer applies.
  dashboardContext.page = parts[0];
  dashboardContext.collection = parts[0] === "records" && parts.length === 2 ? decodeURIComponent(parts[1]) : "";
  dashboardContext.record_id = "";

  try {
    if (parts[0] === "home") {
      updateActiveNav("home");
      await renderHome(app);
    } else if (parts[0] === "account") {
      updateActiveNav("account");
      await renderAccount(app);
    } else if (parts[0] === "collections" && parts.length === 1) {
      updateActiveNav("collections");
      await renderCollections(app);
    } else if (parts[0] === "records" && parts.length === 2) {
      updateActiveNav("collections");
      await renderRecords(app, decodeURIComponent(parts[1]));
    } else if (parts[0] === "files") {
      updateActiveNav("files");
      await renderFiles(app);
    } else if (parts[0] === "rag") {
      updateActiveNav("rag");
      await renderRAGSources(app);
    } else if (parts[0] === "usage") {
      updateActiveNav("usage");
      await renderUsage(app);
    } else if (parts[0] === "logs") {
      updateActiveNav("logs");
      await renderLogs(app);
    } else if (parts[0] === "backups") {
      updateActiveNav("backups");
      await renderBackups(app);
    } else if (parts[0] === "settings") {
      updateActiveNav("settings");
      await renderSettings(app);
    } else {
      location.hash = "#/home";
    }
  } catch (e) {
    app.appendChild(el("div", { class: "card error-text", text: e.message }));
    toastError(e.message);
  }
}

applyStaticIcons();
window.addEventListener("hashchange", navigate);
window.addEventListener("DOMContentLoaded", navigate);

// -- auth pages: login / signup / forgot-password ------------------------
// Two distinct pages (not one form with a signup toggle), each supporting
// both a regular _users session and an _admins session via a secondary
// "log in/sign up as admin instead" link — the dashboard now serves both
// audiences, so the primary flow is the _users one (the common case for
// an app's end users) with the admin path one click away.

function passwordField(placeholder, autocomplete) {
  const input = el("input", { type: "password", placeholder, autocomplete });
  const toggle = el("button", { type: "button", class: "password-toggle", text: "Show" });
  toggle.setAttribute("aria-label", "Show password");
  toggle.addEventListener("click", () => {
    const showing = input.type === "text";
    input.type = showing ? "password" : "text";
    toggle.textContent = showing ? "Show" : "Hide";
    toggle.setAttribute("aria-label", showing ? "Show password" : "Hide password");
  });
  return { input, wrap: el("div", { class: "password-field" }, [input, toggle]) };
}

// passwordStrength is a purely client-side heuristic (length + character
// variety) — it never leaves the browser, so it's just a UX nudge, not a
// policy the server enforces (the server's own floor is the 8-char check
// already applied at submit time).
function passwordStrength(pw) {
  if (!pw) return { score: 0, label: "" };
  let score = 0;
  if (pw.length >= 8) score++;
  if (pw.length >= 12) score++;
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) score++;
  if (/\d/.test(pw)) score++;
  if (/[^A-Za-z0-9]/.test(pw)) score++;
  score = Math.min(score, 4);
  const labels = ["Very weak", "Weak", "Fair", "Good", "Strong"];
  return { score, label: pw.length < 8 ? "Too short" : labels[score] };
}

// passwordStrengthMeter attaches a live strength bar under a password
// input created by passwordField() — used on every "set a new password"
// form (signup, reset password) so users get feedback before submitting.
function passwordStrengthMeter(input) {
  const segments = [0, 1, 2, 3].map(() => el("span", { class: "strength-seg" }));
  const bar = el("div", { class: "strength-bar" }, segments);
  const label = el("span", { class: "strength-label" });
  function update() {
    const { score, label: text } = passwordStrength(input.value);
    segments.forEach((seg, i) => {
      seg.className = i < score ? "strength-seg strength-" + score : "strength-seg";
    });
    label.textContent = input.value ? text : "";
  }
  input.addEventListener("input", update);
  update();
  return el("div", { class: "password-strength" }, [bar, label]);
}

// authBackground is a decorative, hand-rolled SVG gradient — no external
// image assets, so it stays tiny and embeds cleanly via go:embed. Colors
// are CSS custom properties (set via inline `style`, since SVG
// presentation *attributes* don't reliably resolve var(), but inline
// styles do) so it automatically matches the current theme and accent.
function authBackground() {
  const wrap = el("div", { class: "auth-bg" });
  wrap.setAttribute("aria-hidden", "true");
  wrap.innerHTML =
    '<svg viewBox="0 0 800 600" preserveAspectRatio="xMidYMid slice">' +
    '<defs>' +
    '<linearGradient id="obg1" x1="0" y1="0" x2="1" y2="1">' +
    '<stop offset="0%" style="stop-color:var(--accent);stop-opacity:0.32"/>' +
    '<stop offset="100%" style="stop-color:var(--accent-2);stop-opacity:0.04"/>' +
    '</linearGradient>' +
    '<linearGradient id="obg2" x1="1" y1="0" x2="0" y2="1">' +
    '<stop offset="0%" style="stop-color:var(--accent-2);stop-opacity:0.28"/>' +
    '<stop offset="100%" style="stop-color:var(--accent);stop-opacity:0"/>' +
    '</linearGradient>' +
    '</defs>' +
    '<circle cx="620" cy="60" r="280" fill="url(#obg1)"/>' +
    '<circle cx="90" cy="540" r="320" fill="url(#obg2)"/>' +
    '<circle cx="700" cy="580" r="150" fill="url(#obg1)"/>' +
    '</svg>';
  return wrap;
}

function authBrandBlock() {
  return [
    el("div", { class: "auth-brand" }, [el("span", { class: "brand-mark" }, "◆"), "onebox"]),
    el("div", { class: "auth-tagline", text: "Your entire AI backend in one box" }),
  ];
}

function mountAuthPage(container, card) {
  container.appendChild(authBackground());
  container.appendChild(el("div", { class: "auth-shell page-transition" }, [card]));
}

// emergencyKitCard is the "1Password-style" reveal-once recovery phrase
// component, shared by the post-signup flow and the Account page's
// "regenerate recovery phrase" flow. It's plain markup (no PDF library —
// see the docs/api-reference.md note): "Download Emergency Kit" prints
// via the browser's native print-to-PDF, using the @media print rules in
// style.css to show only the phrase card.
function emergencyKitCard(opts) {
  const words = opts.phrase.trim().split(/\s+/);
  const grid = el(
    "div",
    { class: "recovery-grid" },
    words.map((w, i) => el("div", { class: "recovery-word" }, [el("span", { class: "recovery-word-index", text: String(i + 1) }), w]))
  );

  const printBtn = el("button", {
    type: "button",
    class: "btn-secondary",
    text: "🖨️ Download Emergency Kit (PDF)",
    onclick: () => window.print(),
  });

  const ack = el("input", { type: "checkbox" });
  const continueBtn = actionButton(opts.continueLabel || "Continue", {}, async () => {
    if (opts.onContinue) await opts.onContinue();
  });
  continueBtn.disabled = true;
  ack.addEventListener("change", () => {
    continueBtn.disabled = !ack.checked;
  });

  return el("div", { class: "auth-card emergency-kit-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: "Save your recovery phrase" }),
    el("p", { class: "muted", style: "font-size:0.86rem" }, [
      "This is the only time this phrase will be shown. Anyone who has it can reset ",
      opts.email,
      "'s password — save it somewhere safe, like a password manager, or print it and lock it away.",
    ]),
    el("div", { class: "recovery-kit-print" }, [
      el("div", { class: "recovery-kit-meta" }, [
        el("div", {}, [el("strong", {}, "Account: "), opts.email]),
        el("div", {}, [el("strong", {}, "Server: "), location.origin]),
        el("div", { class: "muted" }, "Generated " + new Date().toLocaleString()),
      ]),
      grid,
      el("p", { class: "muted", style: "font-size:0.8rem" }, [
        "To reset this password later, go to ",
        location.origin + "/_/#/forgot-password",
        " and enter this phrase along with the account email.",
      ]),
    ]),
    el("div", { class: "row no-print" }, [printBtn]),
    el("label", { class: "row no-print", style: "align-items:center;font-weight:400" }, [ack, "I've saved this phrase somewhere safe"]),
    el("div", { class: "no-print" }, [continueBtn]),
  ]);
}

// showEmergencyKitModal is emergencyKitCard's modal presentation, used
// when regenerating a phrase from inside the already-authenticated
// dashboard (Account page) rather than during signup. Unlike
// confirmDialog, there's deliberately no click-outside-to-close — losing
// track of an unsaved phrase is the one outcome this screen exists to
// prevent.
function showEmergencyKitModal(opts) {
  return new Promise((resolve) => {
    clear(modalRoot);
    const overlay = el("div", { class: "modal-overlay" }, [emergencyKitCard({ ...opts, onContinue: () => { clear(modalRoot); resolve(); } })]);
    modalRoot.appendChild(overlay);
    modalA11y(overlay);
  });
}

async function fetchSetupStatus() {
  try {
    return await api("/api/setup-status");
  } catch (e) {
    // safest defaults if the check itself fails: don't offer to bootstrap
    // an admin, and don't offer signup either.
    return { admin_exists: true, registration_enabled: false };
  }
}

async function renderLoginPage(container, gen) {
  const status = await fetchSetupStatus();
  if (gen !== undefined && gen !== navGeneration) return; // superseded by a newer navigation
  // No superuser yet — this instance can't do anything else until one
  // exists, so every unauthenticated route funnels here instead of
  // showing a login form with nothing to log into.
  if (!status.admin_exists) {
    location.hash = "#/setup";
    return;
  }
  renderLoginForm(container);
}

// renderLoginForm is the one login form for the dashboard — no
// admin/user choice. It calls POST /api/login, which checks both the
// _admins and _users tables and reports back which one matched; the
// caller never picks a role up front. What that role is allowed to see
// is then decided by navigate() (a "user" role never renders the
// dashboard shell at all — see renderUnauthorizedPage) not by anything
// chosen on this form.
function renderLoginForm(container) {
  const email = el("input", { type: "email", placeholder: "you@example.com", autocomplete: "username" });
  const { input: passwordInput, wrap: passwordWrap } = passwordField("Password", "current-password");
  const rememberMe = el("input", { type: "checkbox" });
  rememberMe.checked = true;
  const status = el("div", { class: "field-error" });

  const submitBtn = actionButton(
    "Log in",
    { style: "width:100%;justify-content:center" },
    async () => {
      clear(status);
      if (!email.value.trim() || !passwordInput.value) {
        status.textContent = "Enter your email and password.";
        return;
      }
      try {
        const resp = await api("/api/login", {
          method: "POST",
          body: JSON.stringify({ email: email.value.trim(), password: passwordInput.value }),
        });
        setToken(resp.token, rememberMe.checked);
        setRole(resp.role, rememberMe.checked);
        accountCache = null;
        toastSuccess("Logged in");
        location.hash = "#/home"; // triggers navigate() via the hashchange listener
      } catch (e) {
        renderAuthError(status, e, { onSignupLink: () => renderSignupPage(container) });
      }
    }
  );

  const card = el("div", { class: "auth-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: "Log in" }),
    el("div", { class: "col" }, [
      el("label", {}, ["Email", email]),
      el("label", {}, ["Password", passwordWrap]),
      el("div", { class: "auth-row-between" }, [
        el("label", { class: "remember-me", style: "align-items:center;font-weight:400" }, [rememberMe, "Remember me"]),
        el("div", { class: "auth-links", style: "margin-top:0" }, [el("a", { href: "#/forgot-password", text: "Forgot password?" })]),
      ]),
      submitBtn,
      status,
    ]),
    el("div", { class: "auth-switch" }, ["Don't have an account? ", el("a", { href: "#/signup", text: "Sign up" })]),
  ]);

  mountAuthPage(container, card);
}

// renderUnauthorizedPage is what a successfully-authenticated regular
// user sees at the backend dashboard URL — never the shell, never a
// grayed-out/locked nav. The dashboard is a Superuser tool; a User
// account authenticating here is a valid login that simply isn't
// authorized for this surface, so it gets a clear 403-style explanation
// and a way out, not a broken or half-populated admin UI.
function renderUnauthorizedPage(container) {
  const logoutBtn = el("button", { class: "btn-secondary", style: "width:100%;justify-content:center", text: "Log out", onclick: performLogout });
  const card = el("div", { class: "auth-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: "Access restricted" }),
    el("p", { class: "muted", style: "font-size:0.9rem" }, "The OneBox dashboard is for administrators only. This account is a regular user, not a Superuser, so it can't access it."),
    logoutBtn,
  ]);
  mountAuthPage(container, card);
}

// renderAuthError renders a login/signup failure into a status element,
// adding a contextual link for the two failure codes that have an
// obvious next step (per hand-tested feedback: "no account with this
// email" should point at signup, "email already exists" should point at
// login — a plain error message with no way forward is a dead end).
function renderAuthError(statusEl, err, opts = {}) {
  clear(statusEl);
  if (err.code === "no_account" && opts.onSignupLink) {
    statusEl.appendChild(document.createTextNode(err.message + " "));
    const link = el("a", { href: "#/signup", text: "Sign up" });
    link.addEventListener("click", (e) => {
      e.preventDefault();
      opts.onSignupLink();
    });
    statusEl.appendChild(link);
    return;
  }
  if (err.code === "email_taken" && opts.onLoginLink) {
    statusEl.appendChild(document.createTextNode(err.message + " "));
    const link = el("a", { href: "#/login", text: "Log in" });
    link.addEventListener("click", (e) => {
      e.preventDefault();
      opts.onLoginLink();
    });
    statusEl.appendChild(link);
    return;
  }
  statusEl.textContent = err.message;
}

// renderSignupPage is regular _users signup only — creating the
// admin/owner account is a separate, dedicated flow (renderSetupPage)
// that a fresh instance is funneled into instead. A onebox instance
// always has a superuser before it has anything else, so signup redirects
// to #/setup rather than rendering here until one exists.
async function renderSignupPage(container, gen) {
  const status = await fetchSetupStatus();
  if (gen !== undefined && gen !== navGeneration) return; // superseded by a newer navigation
  if (!status.admin_exists) {
    location.hash = "#/setup";
    return;
  }
  if (!status.registration_enabled) {
    renderRegistrationDisabledPage(container);
    return;
  }
  renderSignupForm(container);
}

// renderRegistrationDisabledPage is shown instead of the signup form when
// an admin has turned off self-service registration from Settings (see
// registration_enabled) — a clear explanation instead of a form that
// would just 403 on submit.
function renderRegistrationDisabledPage(container) {
  const card = el("div", { class: "auth-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: "Sign up" }),
    el("p", { class: "muted", style: "font-size:0.9rem" }, "New account registration is currently disabled for this onebox instance. If you already have an account, log in instead."),
    el("div", { class: "auth-switch" }, [el("a", { href: "#/login", text: "← Back to log in" })]),
  ]);
  mountAuthPage(container, card);
}

// renderSetupPage is the dedicated "create the superuser" screen — a
// fresh instance's very first account, distinct from regular _users
// signup (see renderSignupPage) the way PocketBase/Supabase/Appwrite all
// keep platform-owner bootstrap separate from application user signup.
// Once an admin exists this route immediately redirects to #/login and
// never renders the form again — POST /api/admins/signup itself already
// rejects a second admin, but redirecting here means the page can't even
// be reached to try.
async function renderSetupPage(container, gen) {
  const status = await fetchSetupStatus();
  if (gen !== undefined && gen !== navGeneration) return; // superseded by a newer navigation
  if (status.admin_exists) {
    // Permanently disabled once a superuser exists — reachable or not,
    // #/setup never renders the form again. An already-authenticated
    // admin (e.g. one who just re-typed the URL) goes straight back to
    // the dashboard instead of a login form they don't need.
    location.hash = isAdminRole() && getToken() ? "#/home" : "#/login";
    return;
  }

  const firstName = el("input", { type: "text", placeholder: "First name", autocomplete: "given-name" });
  const lastName = el("input", { type: "text", placeholder: "Last name", autocomplete: "family-name" });
  const email = el("input", { type: "email", placeholder: "you@example.com", autocomplete: "username" });
  const { input: passwordInput, wrap: passwordWrap } = passwordField("Password", "new-password");
  const { input: confirmInput, wrap: confirmWrap } = passwordField("Confirm password", "new-password");
  const formStatus = el("div", { class: "field-error" });

  const submitBtn = actionButton(
    "Create superuser account",
    { style: "width:100%;justify-content:center" },
    async () => {
      clear(formStatus);
      if (!email.value.trim() || !passwordInput.value) {
        formStatus.textContent = "Enter your email and a password.";
        return;
      }
      if (passwordInput.value.length < 8) {
        formStatus.textContent = "Password must be at least 8 characters.";
        return;
      }
      if (passwordInput.value !== confirmInput.value) {
        formStatus.textContent = "Passwords don't match.";
        return;
      }
      try {
        const resp = await api("/api/admins/signup", {
          method: "POST",
          body: JSON.stringify({
            email: email.value.trim(),
            password: passwordInput.value,
            first_name: firstName.value.trim(),
            last_name: lastName.value.trim(),
          }),
        });
        setToken(resp.token);
        setRole("admin");
        accountCache = null;
        toastSuccess("Superuser account created");

        clear(container);
        container.appendChild(authBackground());
        container.appendChild(
          el("div", { class: "auth-shell page-transition" }, [
            emergencyKitCard({
              email: email.value.trim(),
              phrase: resp.recovery_phrase,
              continueLabel: "Continue to onebox",
              onContinue: () => {
                location.hash = "#/home"; // triggers navigate() via the hashchange listener
              },
            }),
          ])
        );
      } catch (e) {
        // setup_complete means another request won this race and already
        // bootstrapped the admin — send this visitor to log in instead of
        // showing them a dead-end error on a form they can't submit.
        if (e.code === "setup_complete") {
          location.hash = "#/login";
          return;
        }
        formStatus.textContent = e.message;
      }
    }
  );

  const fields = [
    el("div", { class: "row" }, [
      el("label", { style: "flex:1" }, ["First name", firstName]),
      el("label", { style: "flex:1" }, ["Last name", lastName]),
    ]),
    el("label", {}, ["Email", email]),
    el("label", {}, ["Password", passwordWrap]),
    passwordStrengthMeter(passwordInput),
    el("label", {}, ["Confirm password", confirmWrap]),
  ];

  const card = el("div", { class: "auth-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: "Create your superuser account" }),
    el("p", { class: "muted", style: "font-size:0.86rem" }, "This is the first account on this onebox instance. It has full platform access — manage collections, users, settings, and every other admin account — separate from the regular user accounts your app's own users sign up for."),
    el("div", { class: "col" }, fields.concat([submitBtn, formStatus])),
  ]);

  mountAuthPage(container, card);
}

// renderSignupForm creates a regular _users account — the platform's
// admin/owner account is created exclusively through renderSetupPage
// (#/setup), never here, so this form has no role branching left.
function renderSignupForm(container) {
  const firstName = el("input", { type: "text", placeholder: "First name", autocomplete: "given-name" });
  const lastName = el("input", { type: "text", placeholder: "Last name", autocomplete: "family-name" });
  const email = el("input", { type: "email", placeholder: "you@example.com", autocomplete: "username" });
  const { input: passwordInput, wrap: passwordWrap } = passwordField("Password", "new-password");
  const { input: confirmInput, wrap: confirmWrap } = passwordField("Confirm password", "new-password");
  const status = el("div", { class: "field-error" });

  const submitBtn = actionButton(
    "Sign up",
    { style: "width:100%;justify-content:center" },
    async () => {
      clear(status);
      if (!email.value.trim() || !passwordInput.value) {
        status.textContent = "Enter your email and a password.";
        return;
      }
      if (passwordInput.value.length < 8) {
        status.textContent = "Password must be at least 8 characters.";
        return;
      }
      if (passwordInput.value !== confirmInput.value) {
        status.textContent = "Passwords don't match.";
        return;
      }
      try {
        const resp = await api("/api/auth/signup", {
          method: "POST",
          body: JSON.stringify({
            email: email.value.trim(),
            password: passwordInput.value,
            first_name: firstName.value.trim(),
            last_name: lastName.value.trim(),
          }),
        });
        setRole("user");
        setToken(resp.token);
        accountCache = null;
        toastSuccess("Account created");

        clear(container);
        container.appendChild(authBackground());
        container.appendChild(
          el("div", { class: "auth-shell page-transition" }, [
            emergencyKitCard({
              email: email.value.trim(),
              phrase: resp.recovery_phrase,
              continueLabel: "Continue to onebox",
              onContinue: () => {
                location.hash = "#/home"; // triggers navigate() via the hashchange listener
              },
            }),
          ])
        );
      } catch (e) {
        renderAuthError(status, e, { onLoginLink: () => renderLoginPage(container) });
      }
    }
  );

  const fields = [
    el("div", { class: "row" }, [
      el("label", { style: "flex:1" }, ["First name", firstName]),
      el("label", { style: "flex:1" }, ["Last name", lastName]),
    ]),
    el("label", {}, ["Email", email]),
    el("label", {}, ["Password", passwordWrap]),
    passwordStrengthMeter(passwordInput),
    el("label", {}, ["Confirm password", confirmWrap]),
  ];

  const card = el("div", { class: "auth-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: "Sign up" }),
    el("div", { class: "col" }, fields.concat([submitBtn, status])),
    el("div", { class: "auth-switch" }, ["Already have an account? ", el("a", { href: "#/login", text: "Log in" })]),
  ]);

  mountAuthPage(container, card);
}

function renderForgotPasswordPage(container, mode = "phrase", roleMode = "user") {
  const status = el("div", { class: "field-error" });
  let fields, submitBtn;

  if (mode === "phrase") {
    const email = el("input", { type: "email", placeholder: "you@example.com", autocomplete: "username" });
    const phrase = el("textarea", { rows: "2", placeholder: "twelve words, separated by spaces" });
    const { input: newPw, wrap: newPwWrap } = passwordField("New password", "new-password");
    const { input: confirmPw, wrap: confirmPwWrap } = passwordField("Confirm new password", "new-password");

    submitBtn = actionButton("Reset password", { style: "width:100%;justify-content:center" }, async () => {
      clear(status);
      if (!email.value.trim() || !phrase.value.trim()) {
        status.textContent = "Enter your email and your 12-word recovery phrase.";
        return;
      }
      if (newPw.value.length < 8) {
        status.textContent = "Password must be at least 8 characters.";
        return;
      }
      if (newPw.value !== confirmPw.value) {
        status.textContent = "Passwords don't match.";
        return;
      }
      try {
        await api("/api/auth/recover-password", {
          method: "POST",
          body: JSON.stringify({ email: email.value.trim(), recovery_phrase: phrase.value, new_password: newPw.value, role: roleMode }),
        });
        toastSuccess("Password reset — log in with your new password");
        location.hash = "#/login"; // triggers navigate() via the hashchange listener
      } catch (e) {
        status.textContent = e.message;
      }
    });

    fields = [
      el("label", {}, ["Email", email]),
      el("label", {}, ["12-word recovery phrase", phrase]),
      el("label", {}, ["New password", newPwWrap]),
      passwordStrengthMeter(newPw),
      el("label", {}, ["Confirm new password", confirmPwWrap]),
    ];
  } else {
    const token = el("input", { type: "text", placeholder: "Reset code from your admin" });
    const { input: newPw, wrap: newPwWrap } = passwordField("New password", "new-password");
    const { input: confirmPw, wrap: confirmPwWrap } = passwordField("Confirm new password", "new-password");

    submitBtn = actionButton("Reset password", { style: "width:100%;justify-content:center" }, async () => {
      clear(status);
      if (!token.value.trim()) {
        status.textContent = "Paste the reset code your admin gave you.";
        return;
      }
      if (newPw.value.length < 8) {
        status.textContent = "Password must be at least 8 characters.";
        return;
      }
      if (newPw.value !== confirmPw.value) {
        status.textContent = "Passwords don't match.";
        return;
      }
      try {
        await api("/api/auth/reset-password", {
          method: "POST",
          body: JSON.stringify({ token: token.value.trim(), new_password: newPw.value }),
        });
        toastSuccess("Password reset — log in with your new password");
        location.hash = "#/login"; // triggers navigate() via the hashchange listener
      } catch (e) {
        status.textContent = e.message;
      }
    });

    fields = [
      el("label", {}, ["Reset code", token]),
      el("label", {}, ["New password", newPwWrap]),
      passwordStrengthMeter(newPw),
      el("label", {}, ["Confirm new password", confirmPwWrap]),
    ];
  }

  const modeSwitch = el("button", {
    type: "button",
    class: "link-btn",
    text: mode === "phrase" ? "I have a reset code from my admin instead" : "I have my recovery phrase instead",
  });
  modeSwitch.addEventListener("click", () => {
    clear(container);
    renderForgotPasswordPage(container, mode === "phrase" ? "token" : "phrase", roleMode);
  });

  const roleSwitch = el("button", {
    type: "button",
    class: "link-btn",
    text: roleMode === "admin" ? "Recovering a regular user account instead?" : "Recovering an admin account instead?",
  });
  roleSwitch.addEventListener("click", () => {
    clear(container);
    renderForgotPasswordPage(container, mode, roleMode === "admin" ? "user" : "admin");
  });

  const explainer =
    mode === "phrase"
      ? "Enter the email and the 12-word recovery phrase you saved when you created your account."
      : "onebox doesn't send reset emails yet — ask whoever administers your onebox instance to generate a one-time reset code from Settings → Admins, then paste it below.";

  const card = el("div", { class: "auth-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: "Reset your password" }),
    el("p", { class: "muted", style: "font-size:0.86rem", text: explainer }),
    el("div", { class: "col" }, fields.concat([submitBtn, status])),
    el("div", { class: "auth-role-switch" }, [modeSwitch]),
    mode === "phrase" ? el("div", { class: "auth-role-switch" }, [roleSwitch]) : null,
    el("div", { class: "auth-switch" }, [el("a", { href: "#/login", text: "← Back to log in" })]),
  ]);

  mountAuthPage(container, card);
}

// -- home ------------------------------------------------------------

function statCard(opts) {
  const ic = icon(opts.icon, { size: 22 });
  ic.classList.add("stat-card-icon");
  const card = el("a", { href: opts.href, class: "stat-card" }, [
    ic,
    el("div", { class: "stat-card-value", text: opts.value }),
    el("div", { class: "stat-card-label", text: opts.label }),
    opts.sub ? el("div", { class: "stat-card-sub", text: opts.sub }) : null,
  ]);
  return card;
}

function monthStartISO() {
  return new Date().toISOString().slice(0, 7) + "-01T00:00:00.000Z";
}

function timeAgo(iso) {
  if (!iso) return "";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso;
  const diffMs = Date.now() - then;
  const mins = Math.round(diffMs / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return mins + "m ago";
  const hours = Math.round(mins / 60);
  if (hours < 24) return hours + "h ago";
  const days = Math.round(hours / 24);
  if (days < 30) return days + "d ago";
  return iso.slice(0, 10);
}

async function renderHome(container) {
  const account = await loadAccount();
  const admin = isAdminRole();
  const name = displayName(account);
  const heroGreeting = name ? (new Date().getHours() < 12 ? "Hi" : "Welcome back") + ", " + name : "Hi there";

  container.appendChild(
    el("div", { class: "hero" }, [
      el("div", { class: "hero-greeting", text: heroGreeting }),
      el("div", { class: "hero-tagline", text: "Your entire AI backend in one box" }),
    ])
  );

  const statGrid = el("div", { class: "stat-grid" });
  container.appendChild(statGrid);

  const activityCard = el("div", { class: "card" }, [el("h3", { text: "Recent activity" }), el("p", { class: "muted", text: "Loading…" })]);
  container.appendChild(activityCard);

  const [filesResp, ragResp, usageResp, collectionsResp] = await Promise.all([
    api("/api/files?limit=5").catch(() => ({ items: [], total: 0 })),
    api("/api/rag/sources?limit=5").catch(() => ({ items: [], total: 0, status_counts: {} })),
    api("/api/usage?from=" + monthStartISO()).catch(() => ({ items: [], total_cost_estimate: 0 })),
    admin ? api("/api/collections").catch(() => ({ items: [] })) : Promise.resolve(null),
  ]);

  const ready = ragResp.status_counts && ragResp.status_counts.done ? ragResp.status_counts.done : 0;
  const processing =
    ((ragResp.status_counts && ragResp.status_counts.pending) || 0) + ((ragResp.status_counts && ragResp.status_counts.processing) || 0);

  if (admin && collectionsResp) {
    const items = collectionsResp.items || [];
    const totalRecords = items.reduce((sum, c) => sum + (c.record_count || 0), 0);
    statGrid.appendChild(statCard({ href: "#/collections", icon: "collections", value: String(items.length), label: "Collections" }));
    statGrid.appendChild(
      statCard({ href: "#/collections", icon: "record", value: String(totalRecords), label: "Total records", sub: "across all collections" })
    );
  }
  statGrid.appendChild(
    statCard({
      href: "#/rag",
      icon: "rag",
      value: String(ragResp.total || 0),
      label: "Documents",
      sub: ragResp.total ? ready + " ready · " + processing + " processing" : "none yet",
    })
  );
  statGrid.appendChild(statCard({ href: "#/files", icon: "files", value: String(filesResp.total || 0), label: "Files stored" }));
  statGrid.appendChild(
    statCard({
      href: "#/usage",
      icon: "sparkle",
      value: String((usageResp.items || []).length),
      label: "AI calls this month",
      sub: "$" + (usageResp.total_cost_estimate || 0).toFixed(4) + " est. spend",
    })
  );

  clear(activityCard);
  activityCard.appendChild(el("h3", { text: "Recent activity" }));
  const events = []
    .concat((filesResp.items || []).map((f) => ({ icon: "files", text: '"' + f.filename + '" uploaded', created: f.created })))
    .concat(
      (ragResp.items || []).map((s) => ({
        icon: "rag",
        text: '"' + s.filename + '" ' + (s.status === "done" ? "ingested" : s.status),
        created: s.created,
      }))
    )
    .sort((a, b) => (a.created < b.created ? 1 : -1))
    .slice(0, 6);

  const brandNew = (ragResp.total || 0) === 0 && (filesResp.total || 0) === 0 && (admin ? (collectionsResp.items || []).length === 0 : true);

  if (events.length === 0 && brandNew) {
    // One friendly empty state for a fresh account, not two saying the
    // same thing — this replaces the activity card's own generic empty
    // state rather than stacking alongside it.
    activityCard.appendChild(
      emptyState(
        "rocket",
        "Let's get you started",
        admin
          ? "Create a collection, upload a file, or ingest a document to see activity show up here."
          : "Upload a file on the Files page, or ingest a document on RAG sources to enable grounded Q&A."
      )
    );
  } else if (events.length === 0) {
    activityCard.appendChild(emptyState("inbox", "Nothing here yet", "Upload a file or a document to see activity show up here."));
  } else {
    const list = el("div", { class: "activity-list" });
    for (const ev of events) {
      const evIcon = icon(ev.icon, { size: 15 });
      evIcon.classList.add("activity-icon");
      list.appendChild(
        el("div", { class: "activity-row" }, [
          evIcon,
          el("span", { class: "activity-text", text: ev.text }),
          el("span", { class: "activity-time", text: timeAgo(ev.created) }),
        ])
      );
    }
    activityCard.appendChild(list);
  }
}

// -- account -----------------------------------------------------------

async function renderAccount(container) {
  container.appendChild(pageHeader("account", "Account"));
  const admin = isAdminRole();
  const account = await loadAccount(true);

  const avatarSlot = avatarNode(account, "avatar-lg");
  const avatarInput = el("input", { type: "file", accept: "image/*", class: "hidden" });
  const avatarStatus = el("div", { class: "error-text" });
  const avatarEndpoint = admin ? "/api/admins/me/avatar" : "/api/auth/me/avatar";
  const avatarBtn = actionButton("Upload photo", { class: "btn-secondary" }, () => avatarInput.click());
  avatarInput.addEventListener("change", async () => {
    clear(avatarStatus);
    if (!avatarInput.files[0]) return;
    const form = new FormData();
    form.append("file", avatarInput.files[0]);
    try {
      const updated = await api(avatarEndpoint, { method: "POST", body: form });
      accountCache = updated;
      fillAvatarNode(avatarSlot, updated);
      avatarInput.value = "";
      toastSuccess("Profile photo updated");
      refreshAccountSummary();
    } catch (e) {
      avatarStatus.textContent = e.message;
    }
  });
  const removeAvatarBtn = actionButton("Remove photo", { class: "btn-secondary", loadingLabel: "Removing..." }, async () => {
    clear(avatarStatus);
    try {
      const updated = await api(avatarEndpoint, { method: "DELETE" });
      accountCache = updated;
      fillAvatarNode(avatarSlot, updated);
      toastSuccess("Profile photo removed");
      refreshAccountSummary();
    } catch (e) {
      avatarStatus.textContent = e.message;
      throw e;
    }
  });

  container.appendChild(
    el("div", { class: "card" }, [
      el("div", { class: "avatar-upload-row" }, [
        avatarSlot,
        el("div", { class: "col", style: "gap:6px" }, [
          el("div", { class: "row" }, [avatarInput, avatarBtn, removeAvatarBtn]),
          el("div", { class: "muted", style: "font-size:0.8rem", text: "PNG or JPG. Shows your initials until you upload one." }),
          avatarStatus,
        ]),
      ]),
    ])
  );

  const displayNameInput = el("input", { type: "text", value: account.display_name || "", placeholder: "optional — overrides first/last name in greetings" });
  const firstName = el("input", { type: "text", value: account.first_name || "", autocomplete: "given-name" });
  const lastName = el("input", { type: "text", value: account.last_name || "", autocomplete: "family-name" });
  const phone = el("input", { type: "tel", value: account.phone || "", autocomplete: "tel", placeholder: "optional" });
  const profileStatus = el("div", { class: "error-text" });

  const profileFields = [
    el("div", { class: "row" }, [
      el("label", { style: "flex:1" }, ["First name", firstName]),
      el("label", { style: "flex:1" }, ["Last name", lastName]),
    ]),
    el("label", {}, ["Display name (optional)", displayNameInput]),
  ];
  let email;
  if (admin) {
    profileFields.push(el("label", {}, ["Email", el("input", { type: "email", value: account.email || "", disabled: "disabled" })]));
  } else {
    email = el("input", { type: "email", value: account.email || "", autocomplete: "username" });
    profileFields.push(el("label", {}, ["Email", email]));
  }
  profileFields.push(el("label", {}, ["Phone (optional)", phone]));

  const saveProfileBtn = actionButton("Save changes", { loadingLabel: "Saving..." }, async () => {
    clear(profileStatus);
    try {
      const body = { first_name: firstName.value.trim(), last_name: lastName.value.trim(), display_name: displayNameInput.value.trim(), phone: phone.value.trim() };
      if (!admin) body.email = email.value.trim();
      const updated = await api(admin ? "/api/admins/me" : "/api/auth/me", { method: "PATCH", body: JSON.stringify(body) });
      accountCache = updated;
      toastSuccess("Profile updated");
      refreshAccountSummary();
    } catch (e) {
      profileStatus.textContent = e.message;
      toastError("Couldn't save profile: " + e.message);
      throw e;
    }
  });

  container.appendChild(
    el("div", { class: "card" }, [el("h3", { text: "Profile" }), el("div", { class: "col" }, profileFields.concat([saveProfileBtn, profileStatus]))])
  );

  if (admin) {
    container.appendChild(
      el("div", { class: "card" }, [
        cardTitle("lock", "Password"),
        el("p", { class: "muted", text: "Admin accounts don't have self-service password change yet — use \"Regenerate recovery phrase\" below (it needs your current password too), or ask another admin to help." }),
      ])
    );
  } else {
    container.appendChild(renderChangePasswordCard());
  }

  container.appendChild(renderRecoveryPhraseCard());
}

function renderRecoveryPhraseCard() {
  const { input: currentPw, wrap: currentPwWrap } = passwordField("Current password", "current-password");
  const status = el("div", { class: "error-text" });

  const submitBtn = actionButton("Regenerate recovery phrase", { class: "btn-secondary", loadingLabel: "Generating..." }, async () => {
    clear(status);
    if (!currentPw.value) {
      status.textContent = "Enter your current password.";
      return;
    }
    try {
      const resp = await api("/api/auth/regenerate-recovery-phrase", {
        method: "POST",
        body: JSON.stringify({ current_password: currentPw.value }),
      });
      currentPw.value = "";
      const account = await loadAccount(true);
      await showEmergencyKitModal({ email: account.email, phrase: resp.recovery_phrase, continueLabel: "Done" });
      toastSuccess("New recovery phrase generated — the old one no longer works");
    } catch (e) {
      status.textContent = e.message;
      throw e;
    }
  });

  return el("div", { class: "card" }, [
    cardTitle("key", "Recovery phrase"),
    el("p", { class: "muted", text: "Your recovery phrase resets your password from the \"forgot password\" page without needing an admin. Regenerating it invalidates the old one immediately — save the new one." }),
    el("div", { class: "col" }, [el("label", {}, ["Current password", currentPwWrap]), submitBtn, status]),
  ]);
}

function renderChangePasswordCard() {
  const { input: currentPw, wrap: currentPwWrap } = passwordField("Current password", "current-password");
  const { input: newPw, wrap: newPwWrap } = passwordField("New password", "new-password");
  const { input: confirmPw, wrap: confirmPwWrap } = passwordField("Confirm new password", "new-password");
  const status = el("div", { class: "error-text" });

  const submitBtn = actionButton("Change password", { loadingLabel: "Changing..." }, async () => {
    clear(status);
    if (newPw.value.length < 8) {
      status.textContent = "New password must be at least 8 characters.";
      return;
    }
    if (newPw.value !== confirmPw.value) {
      status.textContent = "New passwords don't match.";
      return;
    }
    try {
      await api("/api/auth/change-password", {
        method: "POST",
        body: JSON.stringify({ current_password: currentPw.value, new_password: newPw.value }),
      });
      toastSuccess("Password changed");
      currentPw.value = "";
      newPw.value = "";
      confirmPw.value = "";
    } catch (e) {
      status.textContent = e.message;
      throw e;
    }
  });

  return el("div", { class: "card" }, [
    cardTitle("lock", "Change password"),
    el("div", { class: "col" }, [
      el("label", {}, ["Current password", currentPwWrap]),
      el("label", {}, ["New password", newPwWrap]),
      passwordStrengthMeter(newPw),
      el("label", {}, ["Confirm new password", confirmPwWrap]),
      submitBtn,
      status,
    ]),
  ]);
}

// -- collections -------------------------------------------------------

const FIELD_TYPES = ["text", "number", "bool", "date", "json"];

async function renderCollections(container) {
  const newCollectionBtn = actionButton("+ New collection", {}, async () => {
    const created = await showCreateCollectionModal();
    if (created) navigate();
  });
  container.appendChild(pageHeader("collections", "Collections", [newCollectionBtn]));
  const list = el("div", { class: "card" }, [el("p", { class: "muted", text: "Loading…" })]);
  container.appendChild(list);

  const resp = await api("/api/collections");
  clear(list);
  const items = resp.items || [];
  if (items.length === 0) {
    list.appendChild(emptyState("collections", "No collections yet", 'Click "+ New collection" above to start storing data with a REST API and realtime updates for free.'));
    return;
  }
  const table = el("table", {}, [
    el("thead", {}, el("tr", {}, [el("th", { text: "Name" }), el("th", { text: "Fields" }), el("th", { text: "" })])),
  ]);
  const tbody = el("tbody");
  for (const c of items) {
    const fieldNames = (c.schema.fields || []).map((f) => f.name + ":" + f.type).join(", ");
    const openLink = el("a", { href: "#/records/" + encodeURIComponent(c.name), text: c.name });
    const editBtn = el("button", {
      class: "btn-secondary",
      text: "Edit schema",
      onclick: async () => {
        const updated = await showEditCollectionModal(c);
        if (updated) navigate();
      },
    });
    const delBtn = deleteButton("Delete", 'Delete collection "' + c.name + '" and all its records? This cannot be undone.', async () => {
      await api("/api/collections/" + encodeURIComponent(c.name), { method: "DELETE" });
      toastSuccess('Collection "' + c.name + '" deleted');
      navigate();
    });
    tbody.appendChild(
      el("tr", {}, [
        el("td", {}, openLink),
        el("td", { class: "muted", text: fieldNames }),
        el("td", { class: "row", style: "flex-wrap:nowrap" }, [editBtn, delBtn]),
      ])
    );
  }
  table.appendChild(tbody);
  list.appendChild(table);
}

// showCreateCollectionModal opens the New Collection dialog (PocketBase's
// "+ New collection" opens a similar modal). Resolves true if a
// collection was created, false if cancelled.
function showCreateCollectionModal() {
  return new Promise((resolve) => {
    clear(modalRoot);
    function close(result) {
      clear(modalRoot);
      resolve(result);
    }

    const nameInput = el("input", { placeholder: "collection_name" });
    const fieldsWrap = el("div");
    const status = el("div", { class: "error-text" });
    const fields = [];

    function addFieldRow(name = "", type = "text", required = false) {
      const nameEl = el("input", { placeholder: "field name", value: name });
      const typeEl = el("select", {}, FIELD_TYPES.map((t) => el("option", { value: t, text: t })));
      typeEl.value = type;
      const reqEl = el("input", { type: "checkbox" });
      reqEl.checked = required;
      const reqLabel = el("label", { style: "flex-direction:row;align-items:center;gap:4px;font-weight:400" }, [reqEl, "required"]);
      const removeBtn = el("button", { class: "btn-secondary", text: "×", type: "button", "aria-label": "Remove field" });
      const row = el("div", { class: "field-row" }, [nameEl, typeEl, reqLabel, removeBtn]);
      removeBtn.addEventListener("click", () => {
        fieldsWrap.removeChild(row);
        const idx = fields.indexOf(entry);
        if (idx >= 0) fields.splice(idx, 1);
      });
      const entry = { nameEl, typeEl, reqEl };
      fields.push(entry);
      fieldsWrap.appendChild(row);
    }
    addFieldRow("title", "text", true);

    const addFieldBtn = el("button", { class: "btn-secondary", text: "+ field", type: "button", onclick: () => addFieldRow() });
    const cancelBtn = el("button", { class: "btn-secondary", text: "Cancel", onclick: () => close(false) });
    const submitBtn = actionButton("Create collection", {}, async () => {
      clear(status);
      const schema = {
        fields: fields
          .filter((f) => f.nameEl.value.trim())
          .map((f) => ({ name: f.nameEl.value.trim(), type: f.typeEl.value, required: f.reqEl.checked })),
      };
      const name = nameInput.value.trim();
      try {
        await api("/api/collections", { method: "POST", body: JSON.stringify({ name, schema }) });
        toastSuccess('Collection "' + name + '" created');
        close(true);
      } catch (e) {
        status.textContent = e.message;
        throw e;
      }
    });

    const overlay = el("div", { class: "modal-overlay", onclick: (e) => { if (e.target === overlay) close(false); } }, [
      el("div", { class: "modal-card modal-card-wide" }, [
        el("h3", { text: "New collection" }),
        el("div", { class: "col" }, [
          el("label", {}, ["Name", nameInput]),
          el("div", { class: "muted", text: "Fields" }),
          fieldsWrap,
          addFieldBtn,
          status,
        ]),
        el("div", { class: "modal-actions" }, [cancelBtn, submitBtn]),
      ]),
    ]);
    modalRoot.appendChild(overlay);
    modalA11y(overlay, () => close(false));
  });
}

// showEditCollectionModal lets an admin add/remove/rename/retype/reorder
// fields on an existing collection, backed by PATCH /api/collections/:name
// (a full-table rebuild server-side — see updateCollectionSchema in Go).
// Removing a field or changing its type can lose or alter data, so those
// are flagged in a confirmation dialog before the request is sent; the API
// itself stays unguarded (a script calling PATCH directly shouldn't be
// blocked by a UI-only confirmation). Resolves the updated collection, or
// null if cancelled.
function showEditCollectionModal(coll) {
  return new Promise((resolve) => {
    clear(modalRoot);
    function close(result) {
      clear(modalRoot);
      resolve(result);
    }

    const fieldsWrap = el("div");
    const status = el("div", { class: "error-text" });
    const fields = [];

    function reorderDOM() {
      fields.forEach((f) => fieldsWrap.appendChild(f.row));
    }

    function addFieldRow(origName, name, type, required) {
      const nameEl = el("input", { placeholder: "field name", value: name });
      const typeEl = el("select", {}, FIELD_TYPES.map((t) => el("option", { value: t, text: t })));
      typeEl.value = type;
      const reqEl = el("input", { type: "checkbox" });
      reqEl.checked = required;
      const reqLabel = el("label", { style: "flex-direction:row;align-items:center;gap:4px;font-weight:400" }, [reqEl, "required"]);
      const upBtn = el("button", { class: "btn-secondary", text: "↑", type: "button", title: "Move up", "aria-label": "Move field up" });
      const downBtn = el("button", { class: "btn-secondary", text: "↓", type: "button", title: "Move down", "aria-label": "Move field down" });
      const removeBtn = el("button", { class: "btn-secondary", text: "×", type: "button", title: "Remove field", "aria-label": "Remove field" });
      const row = el("div", { class: "field-row field-row-edit" }, [nameEl, typeEl, reqLabel, upBtn, downBtn, removeBtn]);
      const entry = { nameEl, typeEl, reqEl, origName, origType: type, row };

      upBtn.addEventListener("click", () => {
        const idx = fields.indexOf(entry);
        if (idx <= 0) return;
        fields.splice(idx, 1);
        fields.splice(idx - 1, 0, entry);
        reorderDOM();
      });
      downBtn.addEventListener("click", () => {
        const idx = fields.indexOf(entry);
        if (idx < 0 || idx >= fields.length - 1) return;
        fields.splice(idx, 1);
        fields.splice(idx + 1, 0, entry);
        reorderDOM();
      });
      removeBtn.addEventListener("click", () => {
        fieldsWrap.removeChild(row);
        fields.splice(fields.indexOf(entry), 1);
      });

      fields.push(entry);
      fieldsWrap.appendChild(row);
    }

    for (const f of coll.schema.fields) addFieldRow(f.name, f.name, f.type, f.required);

    const addFieldBtn = el("button", {
      class: "btn-secondary", text: "+ field", type: "button",
      onclick: () => addFieldRow(null, "", "text", false),
    });
    const cancelBtn = el("button", { class: "btn-secondary", text: "Cancel", onclick: () => close(null) });

    const submitBtn = actionButton("Save schema", { loadingLabel: "Saving..." }, async () => {
      clear(status);
      const newFields = fields
        .filter((f) => f.nameEl.value.trim())
        .map((f) => {
          const name = f.nameEl.value.trim();
          const type = f.typeEl.value;
          const field = { name, type, required: f.reqEl.checked };
          if (f.origName && f.origName !== name) field.rename_from = f.origName;
          return field;
        });
      if (newFields.length === 0) {
        status.textContent = "A collection needs at least one field.";
        return;
      }

      const survivingOldNames = new Set(newFields.map((f) => f.rename_from || f.name));
      const removed = coll.schema.fields.filter((f) => !survivingOldNames.has(f.name));
      const retyped = newFields.filter((f) => {
        const orig = coll.schema.fields.find((of) => of.name === (f.rename_from || f.name));
        return orig && orig.type !== f.type;
      });

      if (removed.length || retyped.length) {
        const parts = [];
        if (removed.length) parts.push(removed.length + " field(s) will be permanently deleted, losing that data: " + removed.map((f) => f.name).join(", ") + ".");
        if (retyped.length) parts.push(retyped.length + " field(s) are changing type, which can lose or alter existing data: " + retyped.map((f) => f.rename_from || f.name).join(", ") + ".");
        const ok = await confirmDialog(parts.join(" ") + " This cannot be undone.", "Save anyway");
        if (!ok) return;
      }

      try {
        await api("/api/collections/" + encodeURIComponent(coll.name), { method: "PATCH", body: JSON.stringify({ fields: newFields }) });
        toastSuccess('Schema for "' + coll.name + '" updated');
        close(true);
      } catch (e) {
        status.textContent = e.message;
        throw e;
      }
    });

    const overlay = el("div", { class: "modal-overlay", onclick: (e) => { if (e.target === overlay) close(null); } }, [
      el("div", { class: "modal-card modal-card-wide" }, [
        el("h3", { text: 'Edit "' + coll.name + '" schema' }),
        el("p", { class: "muted", style: "font-size:0.85rem" }, "Renaming a field keeps its data. Removing a field or changing its type can lose or alter data — you'll be asked to confirm before saving."),
        el("div", { class: "col" }, [fieldsWrap, addFieldBtn, status]),
        el("div", { class: "modal-actions" }, [cancelBtn, submitBtn]),
      ]),
    ]);
    modalRoot.appendChild(overlay);
    modalA11y(overlay, () => close(null));
  });
}

// -- records -----------------------------------------------------------

async function renderRecords(container, name) {
  container.appendChild(el("a", { href: "#/collections", text: "← Collections", class: "back-link" }));

  const collection = await api("/api/collections/" + encodeURIComponent(name));
  const fields = collection.schema.fields || [];

  const newRecordBtn = actionButton("+ New record", {}, async () => {
    const created = await showRecordFormModal(name, fields, null);
    if (created) upsertRow(created);
  });
  container.appendChild(pageHeader("record", name, [newRecordBtn]));

  const status = el("div", { class: "error-text" });
  const selected = new Set();
  const bulkBar = el("div", { class: "row bulk-bar hidden" });
  const table = el("table");
  const tbody = el("tbody");
  const thead = el(
    "thead",
    {},
    el(
      "tr",
      {},
      [el("th", {}, el("input", { type: "checkbox", "aria-label": "Select all records", onclick: (e) => toggleAll(e.target.checked) }))].concat(
        ["id", "owner_id"]
          .concat(fields.map((f) => f.name))
          .concat(["created", ""])
          .map((h) => el("th", { text: h }))
      )
    )
  );
  table.appendChild(thead);
  table.appendChild(tbody);

  const loadMoreBtn = actionButton("Load more", { class: "btn-secondary" }, () => loadPage(false));
  let nextCursor = "";
  let loadedAny = false;
  const tableCard = el("div", { class: "card hidden" }, [table, loadMoreBtn]);
  const emptyCard = emptyState("record", "No records yet", 'Click "+ New record" above, or POST to /api/collections/' + name + '/records.');
  emptyCard.classList.add("hidden");
  const loadingMsg = el("p", { class: "muted", text: "Loading…" });

  function updateBulkBar() {
    clear(bulkBar);
    bulkBar.classList.toggle("hidden", selected.size === 0);
    if (selected.size === 0) return;
    bulkBar.appendChild(el("span", { class: "muted", text: selected.size + " selected" }));

    if (fields.length > 0) {
      const bulkFieldSel = el("select", {}, fields.map((f) => el("option", { value: f.name, text: f.name })));
      const bulkValueSlot = el("span");
      let bulkInputEntry;
      function rebuildBulkValueInput() {
        const f = fields.find((x) => x.name === bulkFieldSel.value) || fields[0];
        bulkInputEntry = buildFieldInputs([f])[0];
        clear(bulkValueSlot);
        bulkValueSlot.appendChild(bulkInputEntry.input);
      }
      bulkFieldSel.addEventListener("change", rebuildBulkValueInput);
      rebuildBulkValueInput();

      const bulkApplyBtn = actionButton("Set for selected", { class: "btn-secondary", loadingLabel: "Applying..." }, async () => {
        const ids = Array.from(selected);
        const ok = await confirmDialog('Set "' + bulkFieldSel.value + '" on ' + ids.length + " selected record(s)? This cannot be undone.", "Apply");
        if (!ok) return;
        let body;
        try {
          body = fieldInputsToBody([bulkInputEntry]);
        } catch (e) {
          toastError(e.message);
          return;
        }
        let failed = 0;
        for (const id of ids) {
          try {
            const updated = await api("/api/collections/" + encodeURIComponent(name) + "/records/" + id, { method: "PATCH", body: JSON.stringify(body) });
            upsertRow(updated);
          } catch (e) {
            failed++;
          }
        }
        selected.clear();
        updateBulkBar();
        toastSuccess(failed ? "Updated " + (ids.length - failed) + " record(s), " + failed + " failed" : "Updated " + ids.length + " record(s)");
      });
      bulkBar.appendChild(bulkFieldSel);
      bulkBar.appendChild(bulkValueSlot);
      bulkBar.appendChild(bulkApplyBtn);
    }

    bulkBar.appendChild(
      deleteButton("Delete selected", "Delete " + selected.size + " record(s)? This cannot be undone.", async () => {
        for (const id of Array.from(selected)) {
          await api("/api/collections/" + encodeURIComponent(name) + "/records/" + id, { method: "DELETE" });
          const row = tbody.querySelector('tr[data-id="' + id + '"]');
          if (row) row.remove();
        }
        toastSuccess("Deleted " + selected.size + " record(s)");
        selected.clear();
        updateBulkBar();
        if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
      })
    );
  }
  function toggleAll(checked) {
    tbody.querySelectorAll("input[type=checkbox]").forEach((cb) => { cb.checked = checked; });
    selected.clear();
    if (checked) tbody.querySelectorAll("tr").forEach((row) => selected.add(row.dataset.id));
    updateBulkBar();
  }

  function renderRow(rec) {
    const rowCheckbox = el("input", {
      type: "checkbox",
      "aria-label": "Select record " + rec.id,
      onclick: (e) => {
        if (e.target.checked) selected.add(rec.id);
        else selected.delete(rec.id);
        updateBulkBar();
      },
    });
    const cells = ["id", "owner_id"].concat(fields.map((f) => f.name)).map((key, i) => {
      let val = rec[key];
      if (val === null || val === undefined) val = "";
      else if (typeof val === "object") val = JSON.stringify(val);
      else val = String(val);
      return el("td", { class: i < 2 ? "id-cell" : "", text: val });
    });
    const editBtn = el("button", {
      class: "btn-secondary",
      text: "Edit",
      onclick: async () => {
        const updated = await showRecordFormModal(name, fields, rec);
        if (updated) upsertRow(updated);
      },
    });
    const delBtn = deleteButton("Delete", "Delete this record? This cannot be undone.", async () => {
      await api("/api/collections/" + encodeURIComponent(name) + "/records/" + rec.id, { method: "DELETE" });
      row.remove();
      selected.delete(rec.id);
      updateBulkBar();
      toastSuccess("Record deleted");
      if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
    });
    const created = el("td", { class: "muted", text: rec.created || "" });
    const row = el("tr", { "data-id": rec.id }, [el("td", {}, rowCheckbox)].concat(cells, [created, el("td", { class: "row", style: "flex-wrap:nowrap" }, [editBtn, delBtn])]));
    return row;
  }

  // upsertRow is the single insert/update path for a record row, used by
  // both the New Record modal's own success callback and the realtime SSE
  // handler below. Both can observe the same create (the SSE message and
  // the modal's fetch response race independently), so insertion has to be
  // idempotent on rec.id rather than each caller inserting unconditionally.
  function upsertRow(rec) {
    const existing = tbody.querySelector('tr[data-id="' + rec.id + '"]');
    if (existing) existing.replaceWith(renderRow(rec));
    else tbody.insertBefore(renderRow(rec), tbody.firstChild);
    tableCard.classList.remove("hidden");
    emptyCard.classList.add("hidden");
  }

  // Filter/sort toolbar — exposes the list API's existing ?filter= (which
  // already ANDs any number of comma-separated field=value pairs) and
  // ?sort= query params, as any number of filter rows rather than just one.
  const filterRowsWrap = el("div", { class: "col", style: "gap:6px" });
  const filterRows = [];
  function addFilterRow() {
    const fieldSel = el("select", {}, [el("option", { value: "", text: "Filter by…" })].concat(fields.map((f) => el("option", { value: f.name, text: f.name }))));
    const valueInput = el("input", { type: "text", placeholder: "value", style: "max-width:180px" });
    const removeBtn = el("button", {
      class: "btn-ghost", type: "button", text: "×", "aria-label": "Remove filter",
      onclick: () => {
        filterRowsWrap.removeChild(entryRow);
        filterRows.splice(filterRows.indexOf(entry), 1);
      },
    });
    const entryRow = el("div", { class: "row", style: "flex-wrap:nowrap" }, [fieldSel, valueInput, removeBtn]);
    const entry = { fieldSel, valueInput };
    filterRows.push(entry);
    filterRowsWrap.appendChild(entryRow);
    return entry;
  }
  const firstFilterRow = addFilterRow();
  const addFilterRowBtn = el("button", { class: "btn-ghost", type: "button", text: "+ filter", onclick: () => addFilterRow() });

  const sortBtn = el("button", { class: "btn-secondary", text: "Newest first" });
  let descending = true;
  sortBtn.addEventListener("click", () => {
    descending = !descending;
    sortBtn.textContent = descending ? "Newest first" : "Oldest first";
    loadPage(true);
  });
  const applyFilterBtn = actionButton("Apply", { class: "btn-secondary" }, () => loadPage(true));
  const clearFilterBtn = el("button", {
    class: "btn-ghost",
    text: "Clear",
    onclick: () => {
      clear(filterRowsWrap);
      filterRows.length = 0;
      addFilterRow();
      loadPage(true);
    },
  });

  async function loadPage(reset) {
    if (reset) { nextCursor = ""; loadedAny = false; clear(tbody); selected.clear(); updateBulkBar(); }
    const qs = new URLSearchParams({ limit: "30", sort: descending ? "-created" : "created" });
    if (nextCursor) qs.set("cursor", nextCursor);
    const filterParts = filterRows.filter((r) => r.fieldSel.value && r.valueInput.value).map((r) => r.fieldSel.value + "=" + r.valueInput.value);
    if (filterParts.length) qs.set("filter", filterParts.join(","));
    const resp = await api("/api/collections/" + encodeURIComponent(name) + "/records?" + qs.toString());
    for (const rec of resp.items || []) { tbody.appendChild(renderRow(rec)); loadedAny = true; }
    nextCursor = resp.nextCursor || "";
    loadMoreBtn.style.display = nextCursor ? "" : "none";
    tableCard.classList.toggle("hidden", !loadedAny);
    emptyCard.classList.toggle("hidden", loadedAny);
  }

  const toolbar = el("div", { class: "card col", style: "margin-bottom:12px" }, [
    filterRowsWrap,
    el("div", { class: "row" }, [addFilterRowBtn, applyFilterBtn, clearFilterBtn, sortBtn]),
  ]);

  const recordsPane = el("div", { role: "tabpanel", id: "recordsPane", "aria-labelledby": "recordsTabBtn" }, [toolbar, bulkBar, loadingMsg, tableCard, emptyCard, status]);
  const apiPane = el("div", { class: "hidden", role: "tabpanel", id: "apiPane", "aria-labelledby": "apiTabBtn" }, [renderAPISnippets(name, fields)]);

  const recordsTabBtn = el("button", {
    type: "button", id: "recordsTabBtn", class: "btn-secondary active",
    role: "tab", "aria-selected": "true", "aria-controls": "recordsPane", text: "Records",
  });
  const apiTabBtn = el("button", {
    type: "button", id: "apiTabBtn", class: "btn-secondary",
    role: "tab", "aria-selected": "false", "aria-controls": "apiPane", text: "API",
  });
  recordsTabBtn.addEventListener("click", () => {
    recordsTabBtn.classList.add("active");
    apiTabBtn.classList.remove("active");
    recordsTabBtn.setAttribute("aria-selected", "true");
    apiTabBtn.setAttribute("aria-selected", "false");
    recordsPane.classList.remove("hidden");
    apiPane.classList.add("hidden");
  });
  apiTabBtn.addEventListener("click", () => {
    apiTabBtn.classList.add("active");
    recordsTabBtn.classList.remove("active");
    apiTabBtn.setAttribute("aria-selected", "true");
    recordsTabBtn.setAttribute("aria-selected", "false");
    apiPane.classList.remove("hidden");
    recordsPane.classList.add("hidden");
  });

  container.appendChild(el("div", { class: "row", role: "tablist", style: "margin-bottom:12px" }, [recordsTabBtn, apiTabBtn]));
  container.appendChild(recordsPane);
  container.appendChild(apiPane);

  await loadPage();
  loadingMsg.remove();

  // Keyboard shortcuts, page-scoped (removed on navigation below): "/"
  // focuses the first filter's value field, "n" opens the New Record
  // modal, both skipped while already typing somewhere so they don't
  // hijack normal text entry.
  function onKeydown(e) {
    const typing = ["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement.tagName);
    if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "/") {
      e.preventDefault();
      firstFilterRow.valueInput.focus();
    } else if (e.key === "n") {
      e.preventDefault();
      newRecordBtn.click();
    }
  }
  document.addEventListener("keydown", onKeydown);
  window.addEventListener("hashchange", () => document.removeEventListener("keydown", onKeydown), { once: true });

  // Live-update the table as records change in this collection, via the
  // realtime endpoint built in Week 6. Actions this client itself just
  // performed (e.g. via the New Record form or a Delete button) already
  // updated the DOM directly, so every handler here is a no-op if that
  // row's already in the state the event describes — this also picks up
  // changes made by other admins/users.
  const sse = new EventSource("/api/realtime?token=" + encodeURIComponent(getToken()));
  sse.addEventListener("record_change", (e) => {
    const evt = JSON.parse(e.data);
    if (evt.collection !== name) return;
    if (evt.action === "delete") {
      const existingRow = tbody.querySelector('tr[data-id="' + evt.record.id + '"]');
      if (existingRow) existingRow.remove();
      selected.delete(evt.record.id);
      updateBulkBar();
      return;
    }
    upsertRow(evt.record);
  });
  window.addEventListener("hashchange", () => sse.close(), { once: true });
}

// renderAPISnippets builds ready-to-copy curl + JS-SDK snippets for the
// four record operations on this collection (PocketBase-style "API
// preview"), using an example payload derived from the collection's own
// schema so the snippets are directly runnable, not just a generic shape.
function renderAPISnippets(name, fields) {
  const exampleObj = {};
  for (const f of fields) {
    if (f.type === "number") exampleObj[f.name] = 1;
    else if (f.type === "bool") exampleObj[f.name] = true;
    else if (f.type === "json") exampleObj[f.name] = {};
    else if (f.type === "date") exampleObj[f.name] = new Date().toISOString();
    else exampleObj[f.name] = "example";
  }
  const exampleJSON = JSON.stringify(exampleObj);
  const base = location.origin;
  const enc = encodeURIComponent(name);

  // exampleRecord mirrors what the server actually sends back — the
  // system columns every record has, plus the example field values above
  // — so "response" isn't left to guesswork alongside the request shape.
  const exampleRecord = Object.assign({ id: "rec_abc123", owner_id: null, created: "2025-01-01T00:00:00.000Z", updated: "2025-01-01T00:00:00.000Z" }, exampleObj);
  const exampleRecordJSON = JSON.stringify(exampleRecord, null, 2);
  const exampleListJSON = JSON.stringify({ items: [exampleRecord], nextCursor: "" }, null, 2);

  const snippets = [
    {
      title: "List records",
      curl: `curl "${base}/api/collections/${enc}/records" \\\n  -H "Authorization: Bearer $TOKEN"`,
      js: `const { items } = await client.records("${name}").list();`,
      response: exampleListJSON,
    },
    {
      title: "Create a record",
      curl: `curl -X POST "${base}/api/collections/${enc}/records" \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '${exampleJSON}'`,
      js: `const record = await client.records("${name}").create(${exampleJSON});`,
      response: exampleRecordJSON,
    },
    {
      title: "Update a record",
      curl: `curl -X PATCH "${base}/api/collections/${enc}/records/RECORD_ID" \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '${exampleJSON}'`,
      js: `const record = await client.records("${name}").update("RECORD_ID", ${exampleJSON});`,
      response: exampleRecordJSON,
    },
    {
      title: "Delete a record",
      curl: `curl -X DELETE "${base}/api/collections/${enc}/records/RECORD_ID" \\\n  -H "Authorization: Bearer $TOKEN"`,
      js: `await client.records("${name}").delete("RECORD_ID");`,
      response: "204 No Content (empty body)",
    },
  ];

  const cards = snippets.map((s) => {
    function codeBlock(label, code) {
      const pre = el("pre", { class: "api-snippet", text: code });
      const copyBtn = el("button", {
        type: "button",
        class: "link-btn",
        text: "Copy",
        onclick: () => {
          navigator.clipboard.writeText(code).then(() => toastSuccess(label + " copied"), () => toastError("Couldn't copy — clipboard access was denied"));
        },
      });
      return el("div", {}, [el("div", { class: "row", style: "justify-content:space-between" }, [el("span", { class: "muted", text: label }), copyBtn]), pre]);
    }
    return el("div", { class: "card" }, [
      el("h3", { text: s.title }),
      codeBlock("curl", s.curl),
      codeBlock("JS SDK", s.js),
      codeBlock("Example response", s.response),
    ]);
  });

  return el("div", {}, [
    el("p", { class: "muted", text: "$TOKEN is a session token from /api/auth/login or /api/admins/login. client is a onebox-js OneboxClient instance." }),
    ...cards,
  ]);
}

// buildFieldInputs creates one input per schema field, pre-filled from
// initialValues if given — shared by the create form and the edit modal
// so the two don't drift out of sync on field-type handling.
function buildFieldInputs(fields, initialValues) {
  return fields.map((f) => {
    const current = initialValues ? initialValues[f.name] : undefined;
    let input;
    if (f.type === "bool") {
      input = el("input", { type: "checkbox" });
      input.checked = !!current;
    } else if (f.type === "number") {
      input = el("input", { type: "number", step: "any", value: current === undefined || current === null ? "" : String(current) });
    } else if (f.type === "json") {
      input = el("textarea", { rows: "2", placeholder: "{}", text: current === undefined || current === null ? "" : JSON.stringify(current) });
    } else {
      input = el("input", { type: "text", value: current === undefined || current === null ? "" : String(current) });
    }
    return { field: f, input };
  });
}

// fieldInputsToBody reads buildFieldInputs' inputs back into a JSON body,
// throwing (with the offending field named) on invalid JSON input.
function fieldInputsToBody(inputs) {
  const body = {};
  for (const { field, input } of inputs) {
    if (field.type === "bool") {
      body[field.name] = input.checked;
    } else if (field.type === "number") {
      if (input.value !== "") body[field.name] = Number(input.value);
    } else if (field.type === "json") {
      if (input.value.trim()) {
        try {
          body[field.name] = JSON.parse(input.value);
        } catch (e) {
          throw new Error('"' + field.name + '" is not valid JSON');
        }
      }
    } else if (input.value !== "") {
      body[field.name] = input.value;
    }
  }
  return body;
}

// showRecordFormModal is the Record Editor for both create and edit
// (PocketBase uses a side drawer for this — a centered modal reuses this
// dashboard's existing dialog pattern instead of introducing a second
// one). Pass record=null to create; pass a record to edit it pre-filled.
// Resolves with the created/updated record, or null if cancelled.
function showRecordFormModal(collectionName, fields, record) {
  const isEdit = !!record;
  // Tell the admin chatbot which record is open so "why does this field
  // look wrong" doesn't require pasting the record — cleared on close so
  // stale context doesn't linger after the modal goes away.
  dashboardContext.record_id = isEdit ? record.id : "";
  return new Promise((resolve) => {
    clear(modalRoot);
    const status = el("div", { class: "error-text" });
    const inputs = buildFieldInputs(fields, record || undefined);

    function close(result) {
      dashboardContext.record_id = "";
      clear(modalRoot);
      resolve(result);
    }

    const saveBtn = actionButton(isEdit ? "Save changes" : "Create record", { loadingLabel: "Saving..." }, async () => {
      clear(status);
      try {
        const body = fieldInputsToBody(inputs);
        const url = "/api/collections/" + encodeURIComponent(collectionName) + "/records" + (isEdit ? "/" + record.id : "");
        const result = await api(url, { method: isEdit ? "PATCH" : "POST", body: JSON.stringify(body) });
        toastSuccess(isEdit ? "Record updated" : "Record created");
        close(result);
      } catch (e) {
        status.textContent = e.message;
        throw e;
      }
    });
    const cancelBtn = el("button", { class: "btn-secondary", text: "Cancel", onclick: () => close(null) });

    const overlay = el("div", { class: "modal-overlay", onclick: (e) => { if (e.target === overlay) close(null); } }, [
      el("div", { class: "modal-card modal-card-wide" }, [
        el("h3", { text: isEdit ? "Edit record" : "New record" }),
        isEdit ? el("p", { class: "muted id-cell", style: "font-size:0.8rem", text: record.id }) : null,
        el(
          "div",
          { class: "col" },
          inputs.map(({ field, input }) => el("label", {}, [field.name + (field.required ? " *" : ""), input])).concat([status])
        ),
        el("div", { class: "modal-actions" }, [cancelBtn, saveBtn]),
      ]),
    ]);
    modalRoot.appendChild(overlay);
    modalA11y(overlay, () => close(null));
  });
}

// -- files ---------------------------------------------------------------

// formatBytes renders a byte count the way a person reads file sizes,
// instead of a raw integer that takes a moment to parse ("2.4 MB" vs
// "2415392").
function formatBytes(n) {
  if (n === null || n === undefined) return "";
  const units = ["B", "KB", "MB", "GB"];
  let val = n;
  let i = 0;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return (i === 0 ? String(val) : val.toFixed(1)) + " " + units[i];
}

// uploadFileWithProgress POSTs one file via XMLHttpRequest rather than
// fetch() — fetch has no upload-progress event, so a real progress bar
// (vs. an indeterminate spinner) needs the older XHR API instead. endpoint
// defaults to the Files-browser upload route; the chatbot's attachment
// pipeline (see initChatbot) reuses this same helper against
// /api/chat-attachments instead of duplicating the XHR plumbing. onXHR, if
// given, is called synchronously with the XMLHttpRequest the moment it's
// created — the only way a caller can later call .abort() on it, since the
// XHR itself isn't otherwise reachable from the returned Promise.
function uploadFileWithProgress(file, onProgress, endpoint = "/api/files", onXHR) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    if (onXHR) onXHR(xhr);
    xhr.open("POST", endpoint);
    xhr.setRequestHeader("Authorization", "Bearer " + getToken());
    xhr.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total);
    });
    xhr.addEventListener("load", () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText));
        } catch (e) {
          reject(new Error("Server returned an invalid response"));
        }
      } else {
        let msg = "Upload failed (status " + xhr.status + ")";
        try {
          const body = JSON.parse(xhr.responseText);
          if (body && body.message) msg = body.message;
        } catch (e) {
          // non-JSON error body — fall back to the generic message above
        }
        reject(new Error(msg));
      }
    });
    xhr.addEventListener("error", () => reject(new Error("Network error during upload")));
    xhr.addEventListener("abort", () => reject(new Error("Upload cancelled")));
    const form = new FormData();
    form.append("file", file);
    xhr.send(form);
  });
}

async function downloadFile(id, filename) {
  const res = await fetch("/api/files/" + id, { headers: { Authorization: "Bearer " + getToken() } });
  if (!res.ok) { toastError("Download failed"); return; }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = el("a", { href: url, download: filename });
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

// showFileDetailsModal is the "file details panel": full metadata plus,
// for images, an on-demand authenticated preview (fetched as a blob,
// since a plain <img src> can't carry the auth header a file with an
// owner/authenticated access rule requires).
function showFileDetailsModal(f, onDeleted) {
  return new Promise((resolve) => {
    clear(modalRoot);
    function close() {
      clear(modalRoot);
      resolve();
    }

    const previewSlot = el("div", { class: "file-preview-slot" });
    if ((f.mime || "").startsWith("image/")) {
      previewSlot.appendChild(el("p", { class: "muted", text: "Loading preview…" }));
      fetch("/api/files/" + f.id, { headers: { Authorization: "Bearer " + getToken() } })
        .then((res) => (res.ok ? res.blob() : Promise.reject(new Error("Preview failed to load"))))
        .then((blob) => {
          clear(previewSlot);
          const url = URL.createObjectURL(blob);
          previewSlot.appendChild(el("img", { src: url, alt: f.filename, class: "file-preview-lg" }));
        })
        .catch(() => {
          clear(previewSlot);
          previewSlot.appendChild(el("p", { class: "muted", text: "Preview unavailable." }));
        });
    }

    const dlBtn = actionButton("Download", { class: "btn-secondary" }, () => downloadFile(f.id, f.filename));
    const delBtn = deleteButton("Delete", 'Delete "' + f.filename + '"? This cannot be undone.', async () => {
      await api("/api/files/" + f.id, { method: "DELETE" });
      toastSuccess("File deleted");
      if (onDeleted) onDeleted(f.id);
      close();
    });
    const cancelBtn = el("button", { class: "btn-secondary", text: "Close", onclick: close });

    const overlay = el("div", { class: "modal-overlay", onclick: (e) => { if (e.target === overlay) close(); } }, [
      el("div", { class: "modal-card modal-card-wide" }, [
        el("h3", { text: f.filename }),
        previewSlot,
        el("div", { class: "col", style: "gap:4px;font-size:0.88rem" }, [
          el("div", {}, [el("strong", {}, "Size: "), formatBytes(f.size)]),
          el("div", {}, [el("strong", {}, "Type: "), f.mime || "unknown"]),
          el("div", {}, [el("strong", {}, "Uploaded: "), f.created || ""]),
          el("div", { class: "id-cell" }, [el("strong", {}, "ID: "), f.id]),
        ]),
        el("div", { class: "modal-actions" }, [cancelBtn, dlBtn, delBtn]),
      ]),
    ]);
    modalRoot.appendChild(overlay);
    modalA11y(overlay, close);
  });
}

async function renderFiles(container) {
  container.appendChild(pageHeader("files", "File Storage"));

  const fileInput = el("input", { type: "file", multiple: "multiple", class: "hidden" });
  const uploadStatus = el("div", { class: "error-text" });
  const progressBar = el("div", { class: "upload-progress-bar" });
  const progressWrap = el("div", { class: "upload-progress hidden" }, [progressBar]);
  const progressLabel = el("div", { class: "muted hidden", style: "font-size:0.85rem" });

  const selected = new Set();
  const bulkBar = el("div", { class: "row bulk-bar hidden" });
  const table = el("table", {}, [
    el(
      "thead",
      {},
      el("tr", {}, [
        el("th", {}, el("input", { type: "checkbox", "aria-label": "Select all files", onclick: (e) => toggleAll(e.target.checked) })),
        el("th", { text: "Filename" }),
        el("th", { text: "Size" }),
        el("th", { text: "Mime" }),
        el("th", { text: "Created" }),
        el("th", { text: "" }),
      ])
    ),
  ]);
  const tbody = el("tbody");
  table.appendChild(tbody);
  const tableCard = el("div", { class: "card hidden" }, [table]);
  const emptyCard = emptyState("files", "No files yet", "Drag a file into the box above, or click it to browse.");
  emptyCard.classList.add("hidden");
  const loadingMsg = el("p", { class: "muted", text: "Loading…" });

  function updateBulkBar() {
    clear(bulkBar);
    bulkBar.classList.toggle("hidden", selected.size === 0);
    if (selected.size === 0) return;
    bulkBar.appendChild(el("span", { class: "muted", text: selected.size + " selected" }));
    bulkBar.appendChild(
      deleteButton("Delete selected", "Delete " + selected.size + " file(s)? This cannot be undone.", async () => {
        for (const id of Array.from(selected)) {
          await api("/api/files/" + id, { method: "DELETE" });
          const row = tbody.querySelector('tr[data-id="' + id + '"]');
          if (row) row.remove();
        }
        toastSuccess("Deleted " + selected.size + " file(s)");
        selected.clear();
        updateBulkBar();
        if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
      })
    );
  }
  function toggleAll(checked) {
    tbody.querySelectorAll('input[type=checkbox]').forEach((cb) => { cb.checked = checked; });
    selected.clear();
    if (checked) tbody.querySelectorAll("tr").forEach((row) => selected.add(row.dataset.id));
    updateBulkBar();
  }

  function renderRow(f) {
    const rowCheckbox = el("input", {
      type: "checkbox",
      "aria-label": "Select " + f.filename,
      onclick: (e) => {
        if (e.target.checked) selected.add(f.id);
        else selected.delete(f.id);
        updateBulkBar();
      },
    });
    const nameBtn = el("button", { class: "link-btn", type: "button", text: f.filename, onclick: () => showFileDetailsModal(f, removeRow) });
    const dlBtn = el("button", { class: "btn-secondary", text: "Download", onclick: () => downloadFile(f.id, f.filename) });
    const delBtn = deleteButton("Delete", 'Delete "' + f.filename + '"? This cannot be undone.', async () => {
      await api("/api/files/" + f.id, { method: "DELETE" });
      row.remove();
      selected.delete(f.id);
      updateBulkBar();
      toastSuccess("File deleted");
      if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
    });
    const row = el("tr", { "data-id": f.id }, [
      el("td", {}, rowCheckbox),
      el("td", {}, nameBtn),
      el("td", { text: formatBytes(f.size) }),
      el("td", { class: "muted", text: f.mime }),
      el("td", { class: "muted", text: f.created }),
      el("td", { class: "row" }, [dlBtn, delBtn]),
    ]);
    return row;
  }

  function removeRow(id) {
    const row = tbody.querySelector('tr[data-id="' + id + '"]');
    if (row) row.remove();
    selected.delete(id);
    updateBulkBar();
    if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
  }

  const loadMoreBtn = actionButton("Load more", { class: "btn-secondary" }, loadPage);
  let nextCursor = "";
  async function loadPage() {
    const qs = new URLSearchParams({ limit: "30" });
    if (nextCursor) qs.set("cursor", nextCursor);
    const resp = await api("/api/files?" + qs.toString());
    const items = resp.items || [];
    for (const f of items) tbody.appendChild(renderRow(f));
    nextCursor = resp.nextCursor || "";
    loadMoreBtn.style.display = nextCursor ? "" : "none";
    const hasAny = !!tbody.firstChild;
    tableCard.classList.toggle("hidden", !hasAny);
    emptyCard.classList.toggle("hidden", hasAny);
  }
  tableCard.appendChild(loadMoreBtn);

  // uploadFiles handles any number of files (a multi-select via the
  // browse button, or a multi-file drag-and-drop), one at a time so the
  // progress bar/label always reflect a single, real upload in flight
  // rather than an averaged or fake aggregate.
  async function uploadFiles(fileList) {
    const files = Array.from(fileList);
    if (files.length === 0) return;
    clear(uploadStatus);
    progressWrap.classList.remove("hidden");
    progressLabel.classList.remove("hidden");
    let failed = 0;
    for (let i = 0; i < files.length; i++) {
      const file = files[i];
      progressLabel.textContent = "Uploading " + file.name + " (" + (i + 1) + "/" + files.length + ")…";
      progressBar.style.width = "0%";
      try {
        const rec = await uploadFileWithProgress(file, (frac) => { progressBar.style.width = Math.round(frac * 100) + "%"; });
        tbody.insertBefore(renderRow(rec), tbody.firstChild);
        tableCard.classList.remove("hidden");
        emptyCard.classList.add("hidden");
      } catch (e) {
        failed++;
        uploadStatus.textContent = file.name + ": " + e.message;
        toastError(file.name + ': "' + e.message + '"');
      }
    }
    progressWrap.classList.add("hidden");
    progressLabel.classList.add("hidden");
    fileInput.value = "";
    if (files.length - failed > 0) {
      toastSuccess(files.length - failed === 1 ? "File uploaded" : files.length - failed + " files uploaded");
    }
  }

  fileInput.addEventListener("change", () => uploadFiles(fileInput.files));

  const browseBtn = actionButton("Browse files", { class: "btn-secondary" }, () => fileInput.click());
  const dropzone = el(
    "div",
    { class: "dropzone" },
    [
      el("div", { class: "muted", text: "Drag & drop files here, or" }),
      browseBtn,
      fileInput,
      progressLabel,
      progressWrap,
      uploadStatus,
    ]
  );
  let dragDepth = 0;
  dropzone.addEventListener("dragenter", (e) => {
    e.preventDefault();
    dragDepth++;
    dropzone.classList.add("dropzone-active");
  });
  dropzone.addEventListener("dragover", (e) => e.preventDefault());
  dropzone.addEventListener("dragleave", () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) dropzone.classList.remove("dropzone-active");
  });
  dropzone.addEventListener("drop", (e) => {
    e.preventDefault();
    dragDepth = 0;
    dropzone.classList.remove("dropzone-active");
    if (e.dataTransfer && e.dataTransfer.files.length) uploadFiles(e.dataTransfer.files);
  });

  container.appendChild(el("div", { class: "card" }, [dropzone]));
  container.appendChild(bulkBar);
  container.appendChild(loadingMsg);
  container.appendChild(tableCard);
  container.appendChild(emptyCard);

  await loadPage();
  loadingMsg.remove();
}

// -- rag sources -----------------------------------------------------------

function statusBadge(status, error) {
  const labelMap = { pending: "Pending", processing: "Processing", done: "Ready", error: "Error" };
  const badge = el("span", { class: "badge badge-" + status });
  if (status === "processing") badge.appendChild(el("span", { class: "spinner" }));
  badge.appendChild(document.createTextNode(labelMap[status] || status));
  if (status === "error" && error) badge.title = error;
  return badge;
}

async function renderRAGSources(container) {
  const fileInput = el("input", { type: "file", accept: ".pdf,.txt,.md,.docx", class: "hidden" });
  const uploadBtn = actionButton("+ Upload document", {}, () => fileInput.click());
  container.appendChild(pageHeader("rag", "RAG sources", [uploadBtn, fileInput]));
  const uploadStatus = el("div", { class: "error-text" });
  const table = el("table", {}, [
    el(
      "thead",
      {},
      el("tr", {}, [
        el("th", { text: "Filename" }),
        el("th", { text: "Status" }),
        el("th", { text: "Chunks" }),
        el("th", { text: "Created" }),
        el("th", { text: "" }),
      ])
    ),
  ]);
  const tbody = el("tbody");
  table.appendChild(tbody);
  const tableCard = el("div", { class: "card hidden" }, [table]);
  const emptyCard = emptyState("rag", "No documents ingested yet", "Upload a PDF, TXT, MD, or DOCX above to enable grounded Q&A.");
  emptyCard.classList.add("hidden");
  const loadingMsg = el("p", { class: "muted", text: "Loading…" });

  function renderRow(src) {
    const delBtn = deleteButton("Delete", 'Delete "' + src.filename + '"? This cannot be undone.', async () => {
      await api("/api/rag/sources/" + src.id, { method: "DELETE" });
      row.remove();
      toastSuccess("Source deleted");
      if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
    });
    const row = el("tr", { "data-id": src.id }, [
      el("td", { text: src.filename }),
      el("td", {}, statusBadge(src.status, src.error)),
      el("td", { text: String(src.chunk_count) }),
      el("td", { class: "muted", text: src.created }),
      el("td", {}, delBtn),
    ]);
    return row;
  }

  const loadMoreBtn = actionButton("Load more", { class: "btn-secondary" }, loadPage);
  let nextCursor = "";
  async function loadPage() {
    const qs = new URLSearchParams({ limit: "30" });
    if (nextCursor) qs.set("cursor", nextCursor);
    const resp = await api("/api/rag/sources?" + qs.toString());
    const items = resp.items || [];
    for (const s of items) tbody.appendChild(renderRow(s));
    nextCursor = resp.nextCursor || "";
    loadMoreBtn.style.display = nextCursor ? "" : "none";
    const hasAny = !!tbody.firstChild;
    tableCard.classList.toggle("hidden", !hasAny);
    emptyCard.classList.toggle("hidden", hasAny);
  }
  tableCard.appendChild(loadMoreBtn);

  // Ingestion (extract/chunk/embed) runs in the background server-side;
  // poll the source's status live until it leaves pending/processing so
  // the badge updates from "Processing" to "Ready"/"Error" without a
  // manual refresh.
  async function pollUntilDone(id) {
    for (let i = 0; i < 30; i++) {
      await new Promise((r) => setTimeout(r, 1000));
      let src;
      try {
        src = await api("/api/rag/sources/" + id);
      } catch (e) {
        return;
      }
      const row = tbody.querySelector('tr[data-id="' + id + '"]');
      if (row) row.replaceWith(renderRow(src));
      if (src.status === "done") {
        toastSuccess('"' + src.filename + '" ready (' + src.chunk_count + ' chunk' + (src.chunk_count === 1 ? "" : "s") + ')');
        return;
      }
      if (src.status === "error") {
        toastError('"' + src.filename + '" failed: ' + src.error);
        return;
      }
    }
  }

  fileInput.addEventListener("change", async () => {
    clear(uploadStatus);
    if (!fileInput.files[0]) return;
    const form = new FormData();
    form.append("file", fileInput.files[0]);
    try {
      const src = await api("/api/rag/sources", { method: "POST", body: form });
      tbody.insertBefore(renderRow(src), tbody.firstChild);
      tableCard.classList.remove("hidden");
      emptyCard.classList.add("hidden");
      toastSuccess('"' + src.filename + '" uploaded, ingesting…');
      fileInput.value = "";
      pollUntilDone(src.id);
    } catch (e) {
      uploadStatus.textContent = e.message;
    }
  });

  container.appendChild(uploadStatus);
  container.appendChild(loadingMsg);
  container.appendChild(tableCard);
  container.appendChild(emptyCard);

  await loadPage();
  loadingMsg.remove();
}

// -- usage -----------------------------------------------------------------

// barChart renders a minimal, dependency-free SVG bar chart following the
// dataviz skill's mark spec: thin bars, 4px rounded data-ends anchored to
// the baseline, recessive gridlines, a single sequential hue, direct
// value labels via <title> hover tooltips (no legend needed — one series).
function barChart(data, opts = {}) {
  const width = opts.width || 640;
  const height = opts.height || 200;
  const padL = 36, padB = 24, padT = 12, padR = 8;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;
  const max = Math.max(1, ...data.map((d) => d.value));

  const gapFrac = 0.35;
  const n = Math.max(1, data.length);
  const slot = plotW / n;
  const barW = Math.max(2, slot * (1 - gapFrac));

  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  svg.setAttribute("width", "100%");
  svg.setAttribute("height", height);
  svg.classList.add("viz-root");

  function line(x1, y1, x2, y2, cls) {
    const l = document.createElementNS(svg.namespaceURI, "line");
    l.setAttribute("x1", x1); l.setAttribute("y1", y1);
    l.setAttribute("x2", x2); l.setAttribute("y2", y2);
    l.setAttribute("class", cls);
    svg.appendChild(l);
  }
  // gridlines (recessive) at 0/50/100%
  for (const frac of [0, 0.5, 1]) {
    const y = padT + plotH * (1 - frac);
    line(padL, y, width - padR, y, "chart-grid-line");
  }
  line(padL, padT + plotH, width - padR, padT + plotH, "chart-axis-line");

  data.forEach((d, i) => {
    const barH = max === 0 ? 0 : (d.value / max) * plotH;
    const x = padL + i * slot + (slot - barW) / 2;
    const y = padT + plotH - barH;
    const rect = document.createElementNS(svg.namespaceURI, "rect");
    rect.setAttribute("x", x);
    rect.setAttribute("y", y);
    rect.setAttribute("width", barW);
    rect.setAttribute("height", Math.max(barH, 1));
    rect.setAttribute("rx", 4);
    rect.setAttribute("class", "chart-bar");
    const title = document.createElementNS(svg.namespaceURI, "title");
    title.textContent = d.label + ": " + (opts.formatValue ? opts.formatValue(d.value) : d.value);
    rect.appendChild(title);
    svg.appendChild(rect);

    const label = document.createElementNS(svg.namespaceURI, "text");
    label.setAttribute("x", x + barW / 2);
    label.setAttribute("y", height - 6);
    label.setAttribute("text-anchor", "middle");
    label.setAttribute("class", "chart-bar-label");
    label.textContent = d.label;
    svg.appendChild(label);
  });

  return svg;
}

async function renderUsage(container) {
  container.appendChild(pageHeader("usage", "Usage"));

  const resp = await api("/api/usage");
  const items = resp.items || [];

  container.appendChild(
    el("div", { class: "card" }, [
      el("h3", { text: "Estimated spend (shown range)" }),
      el("p", { style: "font-size:1.6rem;font-weight:700;margin:0", text: "$" + resp.total_cost_estimate.toFixed(4) }),
    ])
  );

  if (items.length === 0) {
    container.appendChild(el("div", { class: "card" }, [emptyState("usage", "No usage recorded yet", "Usage from /api/llm/chat and /api/rag/answer calls will show up here.")]));
    return;
  }

  // Bucket request counts by day (last 14 days present in the data) —
  // magnitude over time, so a bar chart; single series, so no legend
  // needed (the chart title names it).
  const byDay = new Map();
  for (const u of items) {
    const day = (u.created || "").slice(0, 10);
    byDay.set(day, (byDay.get(day) || 0) + 1);
  }
  const days = Array.from(byDay.keys()).sort();
  const chartData = days.map((d) => ({ label: d.slice(5), value: byDay.get(d) }));

  container.appendChild(
    el("div", { class: "card" }, [
      el("h3", { text: "Requests per day" }),
      barChart(chartData, { formatValue: (v) => v + " request" + (v === 1 ? "" : "s") }),
    ])
  );

  const table = el("table", {}, [
    el(
      "thead",
      {},
      el("tr", {}, [
        el("th", { text: "When" }),
        el("th", { text: "User" }),
        el("th", { text: "Provider" }),
        el("th", { text: "Model" }),
        el("th", { text: "Tokens in" }),
        el("th", { text: "Tokens out" }),
        el("th", { text: "Cost" }),
        el("th", { text: "Cached" }),
      ])
    ),
  ]);
  const tbody = el("tbody");
  for (const u of items) {
    tbody.appendChild(
      el("tr", {}, [
        el("td", { class: "muted", text: u.created }),
        el("td", { class: "id-cell", text: u.user_id || "" }),
        el("td", { text: u.provider }),
        el("td", { text: u.model }),
        el("td", { text: String(u.tokens_in) }),
        el("td", { text: String(u.tokens_out) }),
        el("td", { text: "$" + u.cost_estimate.toFixed(6) }),
        el("td", { text: u.cached ? "yes" : "no" }),
      ])
    );
  }
  table.appendChild(tbody);
  container.appendChild(el("div", { class: "card" }, [table]));
}

// renderChatSharePanel lets an admin publish a public, unauthenticated
// chat page (GET /chat/:token — a standalone page, not part of this SPA)
// that answers questions about this onebox instance. Disabling or
// regenerating immediately revokes whichever link was out there before.
function renderChatSharePanel() {
  const body = el("div", { class: "col" }, [el("p", { class: "muted", text: "Loading…" })]);

  async function load() {
    const status = await api("/api/chat-share");
    clear(body);
    if (!status.enabled) {
      const enableBtn = actionButton("Enable public chat link", { class: "btn-secondary" }, async () => {
        await api("/api/chat-share/enable", { method: "POST" });
        toastSuccess("Public chat link enabled");
        await load();
      });
      body.appendChild(enableBtn);
      return;
    }
    const urlInput = el("input", { readonly: "readonly", value: status.url, onclick: (e) => e.target.select() });
    const disableBtn = deleteButton("Disable", "Disable the public chat link? The current link stops working immediately.", async () => {
      await api("/api/chat-share/disable", { method: "POST" });
      toastSuccess("Public chat link disabled");
      await load();
    });
    const regenBtn = actionButton("Regenerate link", { class: "btn-secondary" }, async () => {
      await api("/api/chat-share/regenerate", { method: "POST" });
      toastSuccess("Link regenerated — the old one no longer works");
      await load();
    });
    body.appendChild(el("label", {}, ["Shareable link", urlInput]));
    body.appendChild(el("div", { class: "row" }, [regenBtn, disableBtn]));
  }

  load();

  return el("div", { class: "card" }, [
    el("h3", { text: "Public chat link" }),
    el("p", { class: "muted", text: "A standalone page anyone with the link can use to ask questions about this onebox instance — no login, no frontend code needed. Grounded in your collection names/counts, not raw record data." }),
    body,
  ]);
}

// renderPasswordResetPanel is the admin-side half of the password-reset
// flow: since onebox doesn't send email yet, an admin looks up a user by
// email here and gets a one-time token + expiry back to hand them out of
// band. The user then pastes it into the dashboard's
// #/forgot-password page, which calls POST /api/auth/reset-password.
// Clean seam for later: once SMTP settings exist, an unauthenticated
// "forgot password" endpoint can call the same token-issuing path and
// email it automatically — no change needed here beyond adding that caller.
function renderPasswordResetPanel() {
  const email = el("input", { type: "email", placeholder: "user@example.com" });
  const status = el("div", { class: "error-text" });
  const result = el("div", { class: "hidden" });

  const submitBtn = actionButton("Generate reset token", { class: "btn-secondary" }, async () => {
    clear(status);
    result.classList.add("hidden");
    if (!email.value.trim()) return;
    try {
      const resp = await api("/api/admins/password-resets", {
        method: "POST",
        body: JSON.stringify({ email: email.value.trim() }),
      });
      clear(result);
      result.classList.remove("hidden");
      result.appendChild(el("p", { class: "muted", text: "Give this token to " + resp.email + " — it expires " + new Date(resp.expires_at).toLocaleString() + " and can only be used once." }));
      result.appendChild(el("input", { readonly: "readonly", value: resp.token, onclick: (e) => e.target.select() }));
      toastSuccess("Reset token generated");
    } catch (e) {
      status.textContent = e.message;
      throw e;
    }
  });

  return el("div", { class: "card" }, [
    el("h3", { text: "Reset a user's password" }),
    el("p", { class: "muted", text: "onebox has no SMTP integration yet, so users can't request a reset email themselves. Generate a one-time token here and share it with them directly — they redeem it on the dashboard's \"forgot password\" page." }),
    el("div", { class: "col" }, [el("label", {}, ["User email", email]), submitBtn, status, result]),
  ]);
}

// -- logs ------------------------------------------------------------------

async function renderLogs(container) {
  container.appendChild(pageHeader("logs", "Logs"));

  const statusFilter = el("input", { type: "text", placeholder: "status (e.g. 404)", style: "max-width:160px" });
  const pathFilter = el("input", { type: "text", placeholder: "path contains…", style: "max-width:220px" });
  const table = el("table", {}, [
    el(
      "thead",
      {},
      el("tr", {}, [
        el("th", { text: "Time" }),
        el("th", { text: "Method" }),
        el("th", { text: "Path" }),
        el("th", { text: "Status" }),
        el("th", { text: "User" }),
        el("th", { text: "Duration" }),
      ])
    ),
  ]);
  const tbody = el("tbody");
  table.appendChild(tbody);
  const tableCard = el("div", { class: "card hidden" }, [table]);
  const emptyCard = emptyState("logs", "No requests logged yet", "API requests will show up here as they happen.");
  emptyCard.classList.add("hidden");
  const loadingMsg = el("p", { class: "muted", text: "Loading…" });

  function statusBadgeClass(status) {
    if (status >= 500) return "badge-error";
    if (status >= 400) return "badge-pending";
    return "badge-done";
  }

  async function load() {
    clear(tbody);
    const qs = new URLSearchParams();
    if (statusFilter.value.trim()) qs.set("status", statusFilter.value.trim());
    if (pathFilter.value.trim()) qs.set("path", pathFilter.value.trim());
    const resp = await api("/api/logs" + (qs.toString() ? "?" + qs.toString() : ""));
    const items = resp.items || [];
    for (const e of items) {
      tbody.appendChild(
        el("tr", {}, [
          el("td", { class: "muted", text: e.time }),
          el("td", { text: e.method }),
          el("td", { class: "id-cell", text: e.path }),
          el("td", {}, el("span", { class: "badge " + statusBadgeClass(e.status), text: String(e.status) })),
          el("td", { class: "id-cell", text: e.user_id || "" }),
          el("td", { class: "muted", text: e.duration_ms + "ms" }),
        ])
      );
    }
    tableCard.classList.toggle("hidden", items.length === 0);
    emptyCard.classList.toggle("hidden", items.length !== 0);
  }

  const filterBtn = actionButton("Filter", { class: "btn-secondary" }, load);

  container.appendChild(
    el("div", { class: "card" }, [el("div", { class: "row" }, [statusFilter, pathFilter, filterBtn])])
  );
  container.appendChild(loadingMsg);
  container.appendChild(tableCard);
  container.appendChild(emptyCard);

  await load();
  loadingMsg.remove();
}

// -- backups -----------------------------------------------------------

async function downloadAuthed(path, filename) {
  const res = await fetch(path, { headers: { Authorization: "Bearer " + getToken() } });
  if (!res.ok) {
    toastError("Download failed");
    return;
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = el("a", { href: url, download: filename });
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

async function renderBackups(container) {
  const exportBtn = actionButton("Download full backup (.zip)", {}, async () => {
    await downloadAuthed("/api/backups/export", "onebox-backup-" + new Date().toISOString().slice(0, 10) + ".zip");
    toastSuccess("Backup downloaded");
  });
  container.appendChild(pageHeader("backups", "Backups", [exportBtn]));

  const restoreInput = el("input", { type: "file", accept: ".zip" });
  const restoreStatus = el("div", { class: "error-text" });
  const restoreBtn = actionButton("Restore from backup", { class: "btn-danger", loadingLabel: "Restoring..." }, async () => {
    clear(restoreStatus);
    if (!restoreInput.files[0]) return;
    const ok = await confirmDialog("Restore will overwrite existing data in every collection present in the backup. This cannot be undone. Continue?", "Restore");
    if (!ok) return;
    const form = new FormData();
    form.append("file", restoreInput.files[0]);
    try {
      const resp = await api("/api/backups/import", { method: "POST", body: form });
      toastSuccess(`Restored ${resp.tables_restored ? resp.tables_restored.length : 0} table(s), ${resp.files_restored || 0} file(s)`);
      restoreInput.value = "";
    } catch (e) {
      restoreStatus.textContent = e.message;
      throw e;
    }
  });

  container.appendChild(
    el("div", { class: "card" }, [
      cardTitle("backups", "Full backup"),
      el("p", { class: "muted", text: "A single .zip with the database and every stored file. Restoring merges the backup's data into this instance's matching tables — collections only present in the backup are skipped and reported." }),
      el("div", { class: "row" }, [restoreInput, restoreBtn]),
      restoreStatus,
    ])
  );

  container.appendChild(await renderBackupHistoryCard());

  const collectionsCard = el("div", { class: "col" }, [el("p", { class: "muted", text: "Loading collections…" })]);
  container.appendChild(el("div", { class: "card" }, [cardTitle("collections", "Per-collection export / import"), collectionsCard]));

  const resp = await api("/api/collections");
  clear(collectionsCard);
  const items = resp.items || [];
  if (items.length === 0) {
    collectionsCard.appendChild(emptyState("collections", "No collections yet", "Create one on the Collections page first."));
    return;
  }
  for (const c of items) {
    collectionsCard.appendChild(renderCollectionBackupRow(c));
  }
}

// renderBackupHistoryCard (RC3): the persisted backup history — create,
// list, download, restore-preview, restore, delete — around the same
// snapshot/restore primitives the "Full backup" card above already uses.
// Rebuilds itself in place after any action rather than a full page
// re-render, same lightweight pattern renderCollectionBackupRow's mapping
// area already uses.
async function renderBackupHistoryCard() {
  const listArea = el("div", { class: "col" }, [el("p", { class: "muted", text: "Loading backup history…" })]);

  const createBtn = actionButton("Create backup now", {}, async () => {
    await api("/api/backups", { method: "POST" });
    toastSuccess("Backup created");
    await refreshList();
  });

  const cardEl = el("div", { class: "card" }, [
    el("div", { class: "row", style: "justify-content:space-between" }, [cardTitle("backups", "Backup history"), createBtn]),
    el("p", { class: "muted", text: "Every backup created here (manually, or automatically if scheduled) stays listed below until you delete it." }),
    listArea,
  ]);

  async function refreshList() {
    clear(listArea);
    let resp;
    try {
      resp = await api("/api/backups");
    } catch (e) {
      listArea.appendChild(el("p", { class: "error-text", text: e.message }));
      return;
    }
    const items = resp.items || [];
    if (items.length === 0) {
      listArea.appendChild(el("p", { class: "muted", text: "No backups yet." }));
      return;
    }
    for (const b of items) {
      listArea.appendChild(renderBackupRow(b, refreshList));
    }
  }

  await refreshList();
  return cardEl;
}

function renderBackupRow(b, refreshList) {
  const status = el("div", { class: "error-text" });
  const previewArea = el("div", { class: "hidden col" });

  const badge = b.status === "complete" ? "" : ` (${b.status}${b.error ? ": " + b.error : ""})`;
  const label = el("span", { text: `${b.filename}${badge} — ${formatBytes(b.size_bytes)} · ${b.trigger} · ${new Date(b.created).toLocaleString()}` });

  const downloadBtn = actionButton("Download", { class: "btn-secondary" }, () => downloadAuthed("/api/backups/" + b.id + "/download", b.filename));

  const previewBtn = actionButton("Preview restore…", { class: "btn-secondary" }, async () => {
    clear(status);
    clear(previewArea);
    previewArea.classList.remove("hidden");
    previewArea.appendChild(el("p", { class: "muted", text: "Loading preview…" }));
    let preview;
    try {
      preview = await api("/api/backups/" + b.id + "/preview");
    } catch (e) {
      clear(previewArea);
      previewArea.appendChild(el("p", { class: "error-text", text: e.message }));
      return;
    }
    clear(previewArea);
    const rows = (preview.tables_to_restore || []).map((t) =>
      el("div", { class: "row", style: "justify-content:space-between;max-width:420px" }, [
        el("span", { text: t.table }),
        el("span", { class: "muted", text: `${t.live_row_count} row(s) now -> ${t.backup_row_count} row(s) in backup` }),
      ])
    );
    const skipped = preview.tables_skipped || [];
    previewArea.appendChild(
      el("div", { class: "col", style: "margin-top:8px" }, [
        el("div", { class: "muted", text: "Restoring will replace these tables' data:" }),
        ...rows,
        skipped.length
          ? el("div", { class: "muted", text: "Skipped (not in this instance's current schema): " + skipped.join(", ") })
          : null,
        el("div", { class: "muted", text: (preview.files_in_backup || 0) + " file(s) in this backup." }),
        actionButton("Restore this backup", { class: "btn-danger", loadingLabel: "Restoring..." }, async () => {
          const ok = await confirmDialog(
            "Restore will overwrite existing data in every collection listed above. This cannot be undone. Continue?",
            "Restore"
          );
          if (!ok) return;
          try {
            const summary = await api("/api/backups/" + b.id + "/restore", { method: "POST" });
            toastSuccess(`Restored ${summary.tables_restored ? summary.tables_restored.length : 0} table(s), ${summary.files_restored || 0} file(s)`);
            previewArea.classList.add("hidden");
          } catch (e) {
            status.textContent = e.message;
            throw e;
          }
        }),
      ].filter(Boolean))
    );
  });

  const deleteBtn = actionButton("Delete", { class: "btn-danger" }, async () => {
    const ok = await confirmDialog(`Delete backup "${b.filename}"? This can't be undone.`, "Delete");
    if (!ok) return;
    try {
      await api("/api/backups/" + b.id, { method: "DELETE" });
      toastSuccess("Backup deleted");
      await refreshList();
    } catch (e) {
      status.textContent = e.message;
      throw e;
    }
  });

  const actions = [downloadBtn];
  if (b.status === "complete") actions.push(previewBtn);
  actions.push(deleteBtn);

  return el("div", { class: "col", style: "padding:8px 0;border-top:1px solid var(--border)" }, [
    el("div", { class: "row", style: "justify-content:space-between;flex-wrap:wrap" }, [label, el("div", { class: "row" }, actions)]),
    status,
    previewArea,
  ]);
}

function renderCollectionBackupRow(c) {
  const status = el("div", { class: "error-text" });
  const mappingArea = el("div", { class: "hidden" });
  const fileInput = el("input", { type: "file", accept: ".json,.csv" });

  const exportJSONBtn = actionButton("Export JSON", { class: "btn-secondary" }, () =>
    downloadAuthed("/api/collections/" + encodeURIComponent(c.name) + "/export?format=json", c.name + ".json")
  );
  const exportCSVBtn = actionButton("Export CSV", { class: "btn-secondary" }, () =>
    downloadAuthed("/api/collections/" + encodeURIComponent(c.name) + "/export?format=csv", c.name + ".csv")
  );

  const previewBtn = actionButton("Preview import…", { class: "btn-secondary" }, async () => {
    clear(status);
    clear(mappingArea);
    mappingArea.classList.add("hidden");
    if (!fileInput.files[0]) {
      status.textContent = "Choose a .json or .csv file first.";
      return;
    }
    const form = new FormData();
    form.append("file", fileInput.files[0]);
    try {
      const preview = await api("/api/collections/" + encodeURIComponent(c.name) + "/import/preview", { method: "POST", body: form });
      mappingArea.classList.remove("hidden");
      const selects = {};
      const rows = preview.columns.map((col) => {
        const select = el(
          "select",
          {},
          [el("option", { value: "", text: "(skip)" })].concat(preview.schema_fields.map((f) => el("option", { value: f, text: f })))
        );
        select.value = preview.suggested_map[col] || "";
        selects[col] = select;
        return el("div", { class: "row", style: "justify-content:space-between;max-width:420px" }, [el("span", { text: col }), select]);
      });

      const confirmBtn = actionButton("Confirm import", {}, async () => {
        clear(status);
        const mapping = {};
        for (const col of preview.columns) mapping[col] = selects[col].value;
        const importForm = new FormData();
        importForm.append("file", fileInput.files[0]);
        importForm.append("mapping", JSON.stringify(mapping));
        try {
          const result = await api("/api/collections/" + encodeURIComponent(c.name) + "/import", { method: "POST", body: importForm });
          toastSuccess(`Imported ${result.imported} record(s)` + (result.failed ? `, ${result.failed} failed` : ""));
          mappingArea.classList.add("hidden");
          fileInput.value = "";
        } catch (e) {
          status.textContent = e.message;
          throw e;
        }
      });

      mappingArea.appendChild(
        el("div", { class: "col", style: "margin-top:8px" }, [
          el("div", { class: "muted", text: preview.total_rows + " row(s) detected. Map each source column to a field (or skip it):" }),
          ...rows,
          confirmBtn,
        ])
      );
    } catch (e) {
      status.textContent = e.message;
      throw e;
    }
  });

  return el("div", { class: "col", style: "padding:12px 0;border-bottom:1px solid var(--border)" }, [
    el("div", { class: "row", style: "justify-content:space-between;align-items:center" }, [
      el("strong", {}, c.name),
      el("div", { class: "row" }, [exportJSONBtn, exportCSVBtn]),
    ]),
    el("div", { class: "row" }, [fileInput, previewBtn]),
    status,
    mappingArea,
  ]);
}

// -- settings ------------------------------------------------------------

async function renderSettings(container) {
  container.appendChild(pageHeader("settings", "Settings"));

  const current = await api("/api/settings");
  const status = el("div", { class: "error-text" });

  function secretField(key, label) {
    const isSet = current[key] && current[key].set;
    const input = el("input", {
      type: "password",
      placeholder: isSet ? "•••••••• (set — leave blank to keep)" : "not set",
    });
    return { key, input, secret: true, label };
  }
  function textField(key, label, defaultValue) {
    const input = el("input", { type: "text", value: current[key] || defaultValue || "", placeholder: defaultValue || "" });
    return { key, input, secret: false, label };
  }
  function selectField(key, label, options) {
    const input = el("select", {}, options.map((o) => el("option", { value: o, text: o })));
    input.value = current[key] || options[0];
    return { key, input, secret: false, label };
  }

  // testButton makes one real (non-generating, so no API cost) request to
  // the named provider using whatever's currently typed — falling back to
  // whatever's already saved for any field left blank — and reports
  // success/failure right there, before the user commits to Save.
  function testButton(kind, overridesFn) {
    const result = el("div", { class: "error-text" });
    const btn = actionButton("Test connection", { class: "btn-secondary", loadingLabel: "Testing..." }, async () => {
      clear(result);
      try {
        const resp = await api("/api/settings/test-connection", { method: "POST", body: JSON.stringify({ kind, ...overridesFn() }) });
        result.className = resp.ok ? "success-text" : "error-text";
        result.textContent = (resp.ok ? "✓ " : "✗ ") + resp.message;
      } catch (e) {
        result.className = "error-text";
        result.textContent = e.message;
        throw e;
      }
    });
    return { btn, result };
  }

  const anthropicKey = secretField("anthropic_api_key", "Anthropic API key");
  const anthropicModel = textField("anthropic_model", "Default model for direct API calls", "claude-sonnet-5");
  const anthropicTest = testButton("anthropic", () => ({ api_key: anthropicKey.input.value }));

  const openaiKey = secretField("openai_api_key", "OpenAI API key");
  const openaiBaseURL = textField("openai_base_url", "Base URL", "https://api.openai.com/v1");
  const openaiTest = testButton("openai", () => ({ api_key: openaiKey.input.value, base_url: openaiBaseURL.input.value }));

  const ollamaBaseURL = textField("ollama_base_url", "Base URL", "http://127.0.0.1:11434");
  const ollamaTest = testButton("ollama", () => ({ base_url: ollamaBaseURL.input.value }));

  const embeddingProvider = selectField("embedding_provider", "Provider", ["openai", "ollama", "voyage"]);
  const embeddingKey = secretField("embedding_api_key", "API key (if OpenAI-compatible)");
  const embeddingBaseURL = textField("embedding_base_url", "Base URL (if OpenAI-compatible)");
  const embeddingModel = textField("embedding_model", "Model", "text-embedding-3-small");
  const embeddingTest = testButton("embedding", () => ({
    embedding_provider: embeddingProvider.input.value,
    api_key: embeddingKey.input.value,
    base_url: embeddingProvider.input.value === "ollama" ? ollamaBaseURL.input.value : embeddingBaseURL.input.value,
  }));

  // -- Chat Provider ------------------------------------------------------
  // Independent of Embedding provider above: this picks which backend the
  // dashboard's own chat surfaces (the floating chatbot panel, and
  // /api/rag/answer) use, saved as chat_provider/chat_model. The API
  // key/base URL fields below mirror the same-named field in the
  // Anthropic/OpenAI-compatible/Ollama cards further down the page (there
  // is only one Anthropic key, one OpenAI base URL, one Ollama daemon,
  // server-side) — editing either copy keeps both in sync and saves the
  // same setting, so configuring chat here doesn't require re-entering a
  // key that's already set below, and vice versa.
  const chatProvider = selectField("chat_provider", "Chat provider", ["ollama", "anthropic", "openai"]);

  // modelByProvider isolates each provider's model choice in the UI so
  // switching the Chat Provider dropdown never carries a model name over
  // to a provider it wasn't chosen for. This is the fix for the reported
  // "Anthropic/OpenAI/Ollama all show nomic-embed-text" bug: with a single
  // shared value, whatever Ollama auto-selected (an embedding model, since
  // that's what was actually installed) bled into every other branch's
  // rendering the instant it was picked. Only the provider that's actually
  // persisted (current chat_provider) starts pre-filled from the saved
  // chat_model; every other provider starts blank and computes its own
  // default the first time it's shown — see renderChatFields.
  const persistedChatProvider = current["chat_provider"] || "ollama";
  const modelByProvider = { ollama: "", anthropic: "", openai: "" };
  modelByProvider[persistedChatProvider] = current["chat_model"] || "";

  // A synthetic field descriptor — chat_model's actual input widget
  // changes shape per provider (a dropdown for Ollama/Anthropic, a text
  // box for OpenAI-compatible), so there's no single <input> to point at;
  // this just exposes whichever provider is currently selected's model the
  // same way every other field does, for the shared save loop below.
  const chatModelField = { key: "chat_model", secret: false, input: { get value() { return modelByProvider[chatProvider.input.value] || ""; } } };

  // mirrorField makes a second input for a setting that already has a
  // canonical field elsewhere on the page (a base URL), two-way-synced by
  // value so typing in either one updates the other — but only the
  // canonical field is ever included in allFields, so there is exactly
  // one write path when Save runs (no last-one-wins ambiguity).
  function mirrorField(canonical) {
    const mirror = el("input", { type: "text", value: canonical.input.value, placeholder: canonical.input.placeholder });
    mirror.addEventListener("input", () => {
      canonical.input.value = mirror.value;
    });
    canonical.input.addEventListener("input", () => {
      if (document.activeElement !== mirror) mirror.value = canonical.input.value;
    });
    return mirror;
  }

  const chatFieldsContainer = el("div", { class: "col" });
  const chatTestResult = el("div", { class: "error-text" });
  const chatTestBtn = actionButton("Test connection", { class: "btn-secondary", loadingLabel: "Testing..." }, async () => {
    clear(chatTestResult);
    const provider = chatProvider.input.value;
    const body = { kind: provider, model: modelByProvider[provider] };
    if (provider === "anthropic") body.api_key = anthropicKeyChat.input.value;
    if (provider === "openai") {
      body.api_key = openaiKeyChat.input.value;
      body.base_url = openaiBaseURL.input.value;
    }
    if (provider === "ollama") body.base_url = ollamaBaseURL.input.value;
    try {
      const resp = await api("/api/settings/test-connection", { method: "POST", body: JSON.stringify(body) });
      chatTestResult.className = resp.ok ? "success-text" : "error-text";
      let msg = (resp.ok ? "✓ " : "✗ ") + resp.message;
      if (resp.version) msg += ` — Ollama v${resp.version}`;
      if (resp.models && resp.models.length) msg += `, ${resp.models.length} model(s) found`;
      chatTestResult.textContent = msg;
    } catch (e) {
      chatTestResult.className = "error-text";
      chatTestResult.textContent = e.message;
      throw e;
    }
  });

  const OLLAMA_EXAMPLE_MODELS = ["llama3.2:3b", "llama3.2:1b", "qwen", "mistral"];

  async function loadOllamaModels(baseURL) {
    try {
      return await api("/api/settings/ollama-models?base_url=" + encodeURIComponent(baseURL));
    } catch (e) {
      return { version: "", models: [], error: e.message };
    }
  }

  // Real, separate API key inputs for the Chat Provider panel (not just a
  // status readout) — safe to duplicate the same setting key because a
  // blank secret field is always skipped on save (see the save loop
  // below), so leaving either copy blank never overwrites a saved key
  // with emptiness.
  const anthropicKeyChat = secretField("anthropic_api_key", "Anthropic API key");
  const openaiKeyChat = secretField("openai_api_key", "OpenAI API key");
  const openaiBaseURLMirror = mirrorField(openaiBaseURL);

  // renderChatFields swaps in only the fields relevant to the selected
  // provider — Ollama never shows an API key field, Anthropic/OpenAI
  // never show a raw model dropdown pulled from a live daemon — and, for
  // Ollama, automatically loads what's actually installed instead of
  // asking the operator to type a model name from memory.
  async function renderChatFields() {
    clear(chatFieldsContainer);
    clear(chatTestResult);
    const provider = chatProvider.input.value;

    if (provider === "ollama") {
      const baseURLMirror = mirrorField(ollamaBaseURL);
      const modelSelect = el("select", {}, [el("option", { value: "", text: "Loading installed models…" })]);
      modelSelect.addEventListener("change", () => {
        modelByProvider.ollama = modelSelect.value;
      });
      const info = el("div", { class: "muted", style: "font-size:0.82rem" }, "Detecting Ollama…");

      // refreshModels only ever offers chat-capable models — the server
      // already filtered embedding-only ones out (see
      // handleOllamaModels/llm.IsEmbeddingModel) — and only ever writes to
      // modelByProvider.ollama when the value is either the user's own
      // click or a real installed model, never a guess. That's what keeps
      // an unreachable-daemon fallback from silently "selecting" a model
      // name that was never actually saved.
      async function refreshModels() {
        info.textContent = "Detecting Ollama…";
        const { version, models, embedding_models_excluded, error } = await loadOllamaModels(ollamaBaseURL.input.value);
        clear(modelSelect);

        if (error) {
          modelSelect.appendChild(el("option", { value: "", text: "— choose manually (Ollama unreachable) —" }));
          for (const m of OLLAMA_EXAMPLE_MODELS) modelSelect.appendChild(el("option", { value: m, text: m }));
          modelSelect.value = modelByProvider.ollama && OLLAMA_EXAMPLE_MODELS.includes(modelByProvider.ollama) ? modelByProvider.ollama : "";
          info.textContent =
            "Couldn't reach Ollama at " + ollamaBaseURL.input.value + " — showing example model names below (not verified as installed). Start Ollama and click Test connection to refresh, or pick one manually.";
          return;
        }

        const chatModels = models || [];
        const excludedNote = embedding_models_excluded ? " (" + embedding_models_excluded + " embedding-only model(s) excluded)" : "";
        if (chatModels.length === 0) {
          modelSelect.appendChild(el("option", { value: "", text: "No chat-capable models installed" }));
          modelSelect.value = "";
          modelByProvider.ollama = "";
          info.textContent =
            "Ollama" + (version ? " v" + version : "") + " detected, but no chat-capable models are installed" + excludedNote +
            " — install one, e.g. `ollama pull llama3.2:3b`.";
          return;
        }

        for (const m of chatModels) modelSelect.appendChild(el("option", { value: m, text: m }));
        if (modelByProvider.ollama && chatModels.includes(modelByProvider.ollama)) {
          modelSelect.value = modelByProvider.ollama; // preserve the user's existing selection
        } else {
          // Automatically select the first chat-capable model when none is
          // configured yet (or the previous choice is no longer
          // installed) — never an embedding model, since chatModels is
          // already filtered server-side.
          modelSelect.value = chatModels[0];
          modelByProvider.ollama = chatModels[0];
        }
        info.textContent = "Ollama" + (version ? " v" + version : "") + " detected — " + chatModels.length + " chat model(s) installed" + excludedNote + ".";
      }
      baseURLMirror.addEventListener("change", refreshModels);

      chatFieldsContainer.appendChild(
        el("div", { class: "col" }, [el("label", {}, ["Base URL", baseURLMirror]), el("label", {}, ["Model", modelSelect]), info])
      );
      await refreshModels();
    } else if (provider === "anthropic") {
      const knownModels = ["claude-sonnet-5", "claude-opus-4-8", "claude-haiku-4-5"];
      const modelSelect = el("select", {}, knownModels.map((m) => el("option", { value: m, text: m })));
      const savedModel = modelByProvider.anthropic;
      if (savedModel && !knownModels.includes(savedModel)) {
        modelSelect.appendChild(el("option", { value: savedModel, text: savedModel }));
      }
      modelSelect.value = savedModel || knownModels[0];
      modelByProvider.anthropic = modelSelect.value;
      modelSelect.addEventListener("change", () => {
        modelByProvider.anthropic = modelSelect.value;
      });

      chatFieldsContainer.appendChild(
        el("div", { class: "col" }, [el("label", {}, [anthropicKeyChat.label, anthropicKeyChat.input]), el("label", {}, ["Claude model", modelSelect])])
      );
    } else {
      const modelInput = el("input", { type: "text", value: modelByProvider.openai, placeholder: "gpt-4o-mini" });
      modelInput.addEventListener("input", () => {
        modelByProvider.openai = modelInput.value;
      });
      chatFieldsContainer.appendChild(
        el("div", { class: "col" }, [
          el("label", {}, ["Base URL", openaiBaseURLMirror]),
          el("label", {}, [openaiKeyChat.label, openaiKeyChat.input]),
          el("label", {}, ["Model", modelInput]),
        ])
      );
    }
  }
  chatProvider.input.addEventListener("change", renderChatFields);
  await renderChatFields();

  const allFields = [
    chatProvider, chatModelField, anthropicKeyChat, openaiKeyChat,
    anthropicKey, anthropicModel, openaiKey, openaiBaseURL, ollamaBaseURL,
    embeddingProvider, embeddingKey, embeddingBaseURL, embeddingModel,
  ];

  const saveBtn = actionButton("Save all provider settings", { loadingLabel: "Saving..." }, async () => {
    clear(status);
    const body = {};
    for (const f of allFields) {
      if (f.secret) {
        if (f.input.value !== "") body[f.key] = f.input.value;
      } else {
        body[f.key] = f.input.value;
      }
    }
    try {
      await api("/api/settings", { method: "PUT", body: JSON.stringify(body) });
      toastSuccess("Settings saved successfully");
      navigate(); // re-fetch so secret fields show the fresh masked state
    } catch (e) {
      status.textContent = e.message;
      toastError("Failed to save settings: " + e.message);
      throw e;
    }
  });

  container.appendChild(
    el("div", { class: "card provider-card" }, [
      cardTitle("chat", "Chat Provider"),
      el("p", { class: "muted", text: "Which backend the admin chatbot (bottom-right) and \"Ask about my documents\" (RAG answers) use. Independent of the Embedding provider below — switching this never touches how documents are ingested or searched." }),
      el("div", { class: "col" }, [
        el("label", {}, [chatProvider.label, chatProvider.input]),
        chatFieldsContainer,
        chatTestBtn,
        chatTestResult,
      ]),
    ])
  );

  container.appendChild(
    el("div", { class: "card provider-card" }, [
      cardTitle("cpu", "Anthropic"),
      el("p", { class: "muted", text: "Also powers /api/llm/chat for claude-* models called directly by an external app. The API key here is the same one used above when Chat Provider is set to Anthropic." }),
      el("div", { class: "col" }, [
        el("label", {}, [anthropicKey.label, anthropicKey.input]),
        el("label", {}, [anthropicModel.label, anthropicModel.input]),
        anthropicTest.btn,
        anthropicTest.result,
      ]),
    ])
  );

  container.appendChild(
    el("div", { class: "card provider-card" }, [
      cardTitle("globe", "OpenAI-compatible"),
      el("p", { class: "muted", text: "Also powers /api/llm/chat for gpt-*/o1-*/o3-* models called directly by an external app, and works with any OpenAI-compatible API by changing the base URL. Shared with the Chat Provider panel above when it's set to OpenAI-compatible." }),
      el("div", { class: "col" }, [
        el("label", {}, [openaiKey.label, openaiKey.input]),
        el("label", {}, [openaiBaseURL.label, openaiBaseURL.input]),
        openaiTest.btn,
        openaiTest.result,
      ]),
    ])
  );

  container.appendChild(
    el("div", { class: "card provider-card" }, [
      cardTitle("server", "Ollama (local)"),
      el("p", { class: "muted", text: "Runs any other model name against a local Ollama instance — no API key needed. Shared between the LLM gateway, the Chat Provider panel above, and embeddings (below) when Ollama is selected." }),
      el("div", { class: "col" }, [el("label", {}, [ollamaBaseURL.label, ollamaBaseURL.input]), ollamaTest.btn, ollamaTest.result]),
    ])
  );

  container.appendChild(
    el("div", { class: "card provider-card" }, [
      cardTitle("rag", "Embedding provider"),
      el("p", { class: "muted", text: "Used to ingest RAG sources and embed queries. Pick Ollama for a fully local setup, OpenAI-compatible, or Voyage AI for a hosted embedding API." }),
      el("p", { class: "muted", style: "font-size:0.82rem", text: "Anthropic doesn't provide embedding models (Claude is chat-only) — it can't be used here." }),
      el("div", { class: "col" }, [
        el("label", {}, [embeddingProvider.label, embeddingProvider.input]),
        el("label", {}, [embeddingKey.label, embeddingKey.input]),
        el("label", {}, [embeddingBaseURL.label, embeddingBaseURL.input]),
        el("label", {}, [embeddingModel.label, embeddingModel.input]),
        embeddingTest.btn,
        embeddingTest.result,
      ]),
    ])
  );

  container.appendChild(
    el("div", { class: "card" }, [
      el("p", { class: "muted", text: "Keys are encrypted at rest and never shown again once saved. Saving applies immediately, no restart needed." }),
      el("div", { class: "col" }, [saveBtn, status]),
    ])
  );

  const registrationEnabledInput = el("input", { type: "checkbox" });
  registrationEnabledInput.checked = current["registration_enabled"] !== "false"; // absent/unset = enabled (default)
  const registrationStatus = el("div", { class: "error-text" });
  const registrationSaveBtn = actionButton("Save", { class: "btn-secondary", loadingLabel: "Saving..." }, async () => {
    clear(registrationStatus);
    try {
      await api("/api/settings", { method: "PUT", body: JSON.stringify({ registration_enabled: String(registrationEnabledInput.checked) }) });
      toastSuccess("Registration setting saved");
    } catch (e) {
      registrationStatus.textContent = e.message;
      toastError("Failed to save: " + e.message);
      throw e;
    }
  });
  container.appendChild(
    el("div", { class: "card" }, [
      cardTitle("account", "User registration"),
      el("p", { class: "muted", text: "Allow new users to sign up for a regular (non-admin) account. Turning this off disables self-service signup — existing users can still log in." }),
      el("label", { class: "remember-me", style: "align-items:center;font-weight:400" }, [registrationEnabledInput, "Allow user registration"]),
      registrationSaveBtn,
      registrationStatus,
    ])
  );

  container.appendChild(renderAdminManagementPanel());
  container.appendChild(renderChatSharePanel());
  container.appendChild(renderPasswordResetPanel());

  container.appendChild(
    el("div", { class: "card" }, [
      el("h3", { text: "Rate limits" }),
      el("p", {
        class: "muted",
        text: "Configured via ONEBOX_RATE_LIMIT_PER_MINUTE and ONEBOX_MONTHLY_SPEND_CAP_USD environment variables.",
      }),
    ])
  );

  container.appendChild(el("div", { class: "card" }, [el("h3", { text: "About" }), el("p", {}, ["onebox admin dashboard."])]));
}

// renderAdminManagementPanel lets an admin see every admin account and
// remove one (refused server-side if it's the last), and promote a
// regular user to admin by email — which creates a *separate* admin
// login for them via a one-time reset code (see POST /api/admins/promote
// and its doc comment for why it's a separate account rather than
// converting the existing one).
function renderAdminManagementPanel() {
  const listContainer = el("div", { class: "col" }, [el("p", { class: "muted", text: "Loading…" })]);
  const promoteEmail = el("input", { type: "email", placeholder: "user@example.com" });
  const promoteStatus = el("div", { class: "error-text" });
  const promoteResult = el("div", { class: "hidden" });

  async function loadList() {
    const resp = await api("/api/admins");
    clear(listContainer);
    const items = resp.items || [];
    for (const a of items) {
      const name = [a.first_name, a.last_name].filter(Boolean).join(" ").trim();
      const label = name ? name + " — " + a.email : a.email;
      const demoteBtn = deleteButton("Demote", 'Remove admin access for "' + a.email + '"? This cannot be undone.', async () => {
        try {
          await api("/api/admins/demote", { method: "POST", body: JSON.stringify({ email: a.email }) });
          toastSuccess("Removed admin access for " + a.email);
          await loadList();
        } catch (e) {
          toastError(e.message);
          throw e;
        }
      });
      listContainer.appendChild(el("div", { class: "row", style: "justify-content:space-between;align-items:center" }, [el("span", { text: label }), demoteBtn]));
    }
  }

  const promoteBtn = actionButton("Promote to admin", {}, async () => {
    clear(promoteStatus);
    promoteResult.classList.add("hidden");
    if (!promoteEmail.value.trim()) return;
    try {
      const resp = await api("/api/admins/promote", { method: "POST", body: JSON.stringify({ email: promoteEmail.value.trim() }) });
      clear(promoteResult);
      promoteResult.classList.remove("hidden");
      promoteResult.appendChild(
        el("p", {
          class: "muted",
          text:
            "Give this reset code to " +
            resp.email +
            " — it expires " +
            new Date(resp.expires_at).toLocaleString() +
            " and can only be used once. They redeem it at Forgot password → \"I have a reset code from my admin\".",
        })
      );
      promoteResult.appendChild(el("input", { readonly: "readonly", value: resp.reset_token, onclick: (e) => e.target.select() }));
      promoteEmail.value = "";
      toastSuccess("Promoted — share the reset code to finish");
      await loadList();
    } catch (e) {
      promoteStatus.textContent = e.message;
      throw e;
    }
  });

  const card = el("div", { class: "card" }, [
    el("h3", { text: "Admins" }),
    el("p", {
      class: "muted",
      text: "Anyone with admin access can manage collections, settings, and every account's data. Promoting someone creates a separate admin login for them — their regular user account, if they have one, is untouched.",
    }),
    listContainer,
    el("div", { class: "col", style: "margin-top:12px" }, [
      el("label", {}, ["Promote a user to admin (by email)", promoteEmail]),
      promoteBtn,
      promoteStatus,
      promoteResult,
    ]),
  ]);

  loadList();
  return card;
}
