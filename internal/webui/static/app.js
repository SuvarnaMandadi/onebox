// onebox admin dashboard — plain JS, no build step, no framework.
// Hash-based routing; every view is a render(container) function.

const TOKEN_KEY = "onebox_admin_token";
const ROLE_KEY = "onebox_role"; // "admin" | "user" — which login/signup endpoint issued the current token
const THEME_KEY = "onebox_theme";

function getToken() { return localStorage.getItem(TOKEN_KEY) || ""; }
function setToken(t) { localStorage.setItem(TOKEN_KEY, t); }
function clearToken() { localStorage.removeItem(TOKEN_KEY); }

function getRole() { return localStorage.getItem(ROLE_KEY) || "user"; }
function setRole(r) { localStorage.setItem(ROLE_KEY, r); }
function clearRole() { localStorage.removeItem(ROLE_KEY); }
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

function emptyState(icon, title, hint) {
  return el("div", { class: "empty-state" }, [
    el("div", { class: "empty-icon", text: icon }),
    el("div", { text: title }),
    hint ? el("div", { class: "empty-hint", text: hint }) : null,
  ]);
}

// -- toasts --------------------------------------------------------------
// Every action in this dashboard reports its outcome via a toast — success
// or failure — instead of a silent refresh, per the "nothing may silently
// refresh" design requirement.

const toastRoot = document.getElementById("toasts");

function toast(message, type = "info") {
  const node = el("div", { class: "toast toast-" + type, text: message });
  toastRoot.appendChild(node);
  setTimeout(() => {
    node.style.transition = "opacity 0.2s ease";
    node.style.opacity = "0";
    setTimeout(() => node.remove(), 200);
  }, 3200);
}
const toastSuccess = (msg) => toast(msg, "success");
const toastError = (msg) => toast(msg, "error");

// -- confirm dialog --------------------------------------------------------
// Replaces native confirm() with a themed modal, returning a Promise<bool>.

const modalRoot = document.getElementById("modalRoot");

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
  const fab = el("button", { type: "button", class: "chatbot-fab", title: "Ask about this onebox instance" }, "💬");
  const log = el("div", { class: "chatbot-log" });
  const input = el("input", { type: "text", placeholder: "Ask a question…" });
  const sendBtn = el("button", { type: "submit", text: "Send" });
  const form = el("form", { class: "chatbot-form" }, [input, sendBtn]);
  const panel = el("div", { class: "chatbot-panel hidden" }, [
    el("div", { class: "chatbot-panel-header" }, [
      "Ask about your OneBox",
      el("button", { type: "button", class: "link-btn", text: "✕", onclick: () => panel.classList.add("hidden") }),
    ]),
    log,
    form,
  ]);

  function appendMsg(role, text) {
    log.appendChild(el("div", { class: "chatbot-msg " + role, text }));
    log.scrollTop = log.scrollHeight;
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const message = input.value.trim();
    if (!message) return;
    appendMsg("user", message);
    input.value = "";
    input.disabled = true;
    try {
      const resp = await api("/api/chat", { method: "POST", body: JSON.stringify({ message }) });
      appendMsg("assistant", resp.reply);
    } catch (err) {
      appendMsg("assistant", err.message);
    }
    input.disabled = false;
    input.focus();
  });

  fab.addEventListener("click", () => {
    panel.classList.toggle("hidden");
    if (!panel.classList.contains("hidden")) input.focus();
  });

  root.appendChild(panel);
  root.appendChild(fab);
})();

document.getElementById("logoutBtn").addEventListener("click", () => {
  clearToken();
  clearRole();
  accountCache = null;
  // Setting location.hash (when it actually changes) fires the
  // "hashchange" listener below, which calls navigate() itself — an
  // explicit extra call here would race it: both invocations are async
  // and can interleave, appending duplicate content to #app. See the
  // same reasoning at the other location.hash assignments in this file.
  location.hash = "#/login";
});

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

// applyRoleVisibility gives non-admin users a *hint* that admin-only
// sections exist (a grayed-out, lock-badged nav item) rather than hiding
// them outright — hand-tested feedback was that fully hiding Collections
// and Settings made it look like they didn't exist at all, rather than
// "ask an admin to promote you."
function applyRoleVisibility() {
  const admin = isAdminRole();
  document.querySelectorAll('[data-role="admin"]').forEach((node) => {
    node.classList.toggle("locked", !admin);
  });
  document.getElementById("chatbotRoot").classList.toggle("hidden", !admin);
}

// Sidebar nav links are only ever rendered once (in index.html), so their
// click handlers are bound once here rather than re-bound on every
// navigate() call. Two behaviors layered on top of the plain hash link:
//  - a "locked" (admin-only, non-admin viewer) item explains itself via a
//    toast instead of silently 404ing against an admin-only endpoint.
//  - clicking the link for the route you're already on doesn't change
//    location.hash, so the "hashchange" listener never fires and the
//    page would otherwise sit there stale (reported: Home's stat cards
//    not refreshing) — force a re-render in that case.
document.querySelectorAll('#sidebar a[href^="#/"]').forEach((a) => {
  a.addEventListener("click", (e) => {
    if (a.classList.contains("locked")) {
      e.preventDefault();
      toast('"' + a.textContent.trim() + '" is an admin feature — ask an existing admin to promote your account from Settings → Admins.', "info");
      return;
    }
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
  const authed = !!getToken();
  shell.classList.toggle("hidden", !authed);
  loginRoot.classList.toggle("hidden", authed);

  if (!authed) {
    clear(loginRoot);
    const parts = currentRoute();
    if (parts[0] === "signup") await renderSignupPage(loginRoot, myGen);
    else if (parts[0] === "forgot-password") renderForgotPasswordPage(loginRoot);
    else await renderLoginPage(loginRoot, myGen);
    return;
  }

  applyRoleVisibility();
  refreshAccountSummary();

  clear(app);
  app.classList.remove("page-transition");
  void app.offsetWidth; // restart the entrance animation on every route change
  app.classList.add("page-transition");

  const parts = currentRoute();
  if (parts.length === 0) {
    location.hash = "#/home";
    return;
  }
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
  });
}

async function fetchSetupStatus() {
  try {
    return await api("/api/setup-status");
  } catch (e) {
    return { admin_exists: true }; // safest default: don't offer to bootstrap an admin if the check fails
  }
}

async function renderLoginPage(container, roleMode = "user", gen) {
  const status = await fetchSetupStatus();
  if (gen !== undefined && gen !== navGeneration) return; // superseded by a newer navigation
  renderLoginForm(container, roleMode, status.admin_exists);
}

function renderLoginForm(container, roleMode, adminExists) {
  const email = el("input", { type: "email", placeholder: "you@example.com", autocomplete: "username" });
  const { input: passwordInput, wrap: passwordWrap } = passwordField("Password", "current-password");
  const status = el("div", { class: "field-error" });

  const submitBtn = actionButton(
    roleMode === "admin" ? "Log in as admin" : "Log in",
    { style: "width:100%;justify-content:center" },
    async () => {
      clear(status);
      if (!email.value.trim() || !passwordInput.value) {
        status.textContent = "Enter your email and password.";
        return;
      }
      try {
        const endpoint = roleMode === "admin" ? "/api/admins/login" : "/api/auth/login";
        const resp = await api(endpoint, {
          method: "POST",
          body: JSON.stringify({ email: email.value.trim(), password: passwordInput.value }),
        });
        setToken(resp.token);
        setRole(roleMode);
        accountCache = null;
        toastSuccess("Logged in");
        location.hash = "#/home"; // triggers navigate() via the hashchange listener
      } catch (e) {
        renderAuthError(status, e, { onSignupLink: () => renderSignupPage(container) });
      }
    }
  );

  const roleSwitchRow =
    adminExists || roleMode === "admin"
      ? el("div", { class: "auth-role-switch" }, [
          (() => {
            const btn = el("button", {
              type: "button",
              class: "link-btn",
              text: roleMode === "admin" ? "Log in as a user instead" : "Are you the admin? Log in as admin",
            });
            btn.addEventListener("click", () => {
              clear(container);
              renderLoginForm(container, roleMode === "admin" ? "user" : "admin", adminExists);
            });
            return btn;
          })(),
        ])
      : null;

  const card = el("div", { class: "auth-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: roleMode === "admin" ? "Admin log in" : "Log in" }),
    el("div", { class: "col" }, [
      el("label", {}, ["Email", email]),
      el("label", {}, ["Password", passwordWrap]),
      el("div", { class: "auth-links" }, [el("a", { href: "#/forgot-password", text: "Forgot password?" })]),
      submitBtn,
      status,
    ]),
    roleSwitchRow,
    el("div", { class: "auth-switch" }, ["Don't have an account? ", el("a", { href: "#/signup", text: "Sign up" })]),
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

// renderSignupPage decides the mode itself (rather than offering a
// toggle): a fresh instance's very first account is always the
// admin/owner, so signup only ever asks "create the admin account" until
// one exists, then only ever regular _users signup after that — see
// GET /api/setup-status.
async function renderSignupPage(container, gen) {
  const status = await fetchSetupStatus();
  if (gen !== undefined && gen !== navGeneration) return; // superseded by a newer navigation
  renderSignupForm(container, status.admin_exists ? "user" : "admin");
}

function renderSignupForm(container, roleMode) {
  const firstName = el("input", { type: "text", placeholder: "First name", autocomplete: "given-name" });
  const lastName = el("input", { type: "text", placeholder: "Last name", autocomplete: "family-name" });
  const email = el("input", { type: "email", placeholder: "you@example.com", autocomplete: "username" });
  const { input: passwordInput, wrap: passwordWrap } = passwordField("Password", "new-password");
  const { input: confirmInput, wrap: confirmWrap } = passwordField("Confirm password", "new-password");
  const status = el("div", { class: "field-error" });

  const submitBtn = actionButton(
    roleMode === "admin" ? "Create admin account" : "Sign up",
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
        let resp;
        if (roleMode === "admin") {
          resp = await api("/api/admins/signup", {
            method: "POST",
            body: JSON.stringify({
              email: email.value.trim(),
              password: passwordInput.value,
              first_name: firstName.value.trim(),
              last_name: lastName.value.trim(),
            }),
          });
          setRole("admin");
        } else {
          resp = await api("/api/auth/signup", {
            method: "POST",
            body: JSON.stringify({
              email: email.value.trim(),
              password: passwordInput.value,
              first_name: firstName.value.trim(),
              last_name: lastName.value.trim(),
            }),
          });
          setRole("user");
        }
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
    el("label", {}, ["Confirm password", confirmWrap]),
  ];

  const card = el("div", { class: "auth-card" }, [
    ...authBrandBlock(),
    el("h2", { class: "auth-title", text: roleMode === "admin" ? "Set up your onebox instance" : "Sign up" }),
    roleMode === "admin"
      ? el("p", { class: "muted", style: "font-size:0.86rem" }, ["This is the first account on this onebox instance, so it becomes the admin/owner account."])
      : null,
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

    fields = [el("label", {}, ["Reset code", token]), el("label", {}, ["New password", newPwWrap]), el("label", {}, ["Confirm new password", confirmPwWrap])];
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
  const card = el("a", { href: opts.href, class: "stat-card" }, [
    el("div", { class: "stat-card-icon", text: opts.icon }),
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
    statGrid.appendChild(statCard({ href: "#/collections", icon: "🗂️", value: String(items.length), label: "Collections" }));
    statGrid.appendChild(
      statCard({ href: "#/collections", icon: "📇", value: String(totalRecords), label: "Total records", sub: "across all collections" })
    );
  }
  statGrid.appendChild(
    statCard({
      href: "#/rag",
      icon: "📚",
      value: String(ragResp.total || 0),
      label: "Documents",
      sub: ragResp.total ? ready + " ready · " + processing + " processing" : "none yet",
    })
  );
  statGrid.appendChild(statCard({ href: "#/files", icon: "📁", value: String(filesResp.total || 0), label: "Files stored" }));
  statGrid.appendChild(
    statCard({
      href: "#/usage",
      icon: "✨",
      value: String((usageResp.items || []).length),
      label: "AI calls this month",
      sub: "$" + (usageResp.total_cost_estimate || 0).toFixed(4) + " est. spend",
    })
  );

  clear(activityCard);
  activityCard.appendChild(el("h3", { text: "Recent activity" }));
  const events = []
    .concat((filesResp.items || []).map((f) => ({ icon: "📁", text: '"' + f.filename + '" uploaded', created: f.created })))
    .concat(
      (ragResp.items || []).map((s) => ({
        icon: "📚",
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
        "🚀",
        "Let's get you started",
        admin
          ? "Create a collection, upload a file, or ingest a document to see activity show up here."
          : "Upload a file on the Files page, or ingest a document on RAG sources to enable grounded Q&A."
      )
    );
  } else if (events.length === 0) {
    activityCard.appendChild(emptyState("👋", "Nothing here yet", "Upload a file or a document to see activity show up here."));
  } else {
    const list = el("div", { class: "activity-list" });
    for (const ev of events) {
      list.appendChild(
        el("div", { class: "activity-row" }, [
          el("span", { class: "activity-icon", text: ev.icon }),
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
  container.appendChild(el("h2", { text: "Account" }));
  const admin = isAdminRole();
  const account = await loadAccount(true);

  const avatarSlot = avatarNode(account, "avatar-lg");
  const avatarInput = el("input", { type: "file", accept: "image/*" });
  const avatarStatus = el("div", { class: "error-text" });
  const avatarEndpoint = admin ? "/api/admins/me/avatar" : "/api/auth/me/avatar";
  const avatarBtn = actionButton("Upload photo", { class: "btn-secondary" }, async () => {
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
      throw e;
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
        el("h3", { text: "Password" }),
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
    el("h3", { text: "Recovery phrase" }),
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
    el("h3", { text: "Change password" }),
    el("div", { class: "col" }, [
      el("label", {}, ["Current password", currentPwWrap]),
      el("label", {}, ["New password", newPwWrap]),
      el("label", {}, ["Confirm new password", confirmPwWrap]),
      submitBtn,
      status,
    ]),
  ]);
}

// -- collections -------------------------------------------------------

const FIELD_TYPES = ["text", "number", "bool", "date", "json"];

async function renderCollections(container) {
  container.appendChild(el("h2", { text: "Collections" }));
  const list = el("div", { class: "card" }, [el("p", { class: "muted", text: "Loading…" })]);
  container.appendChild(list);
  container.appendChild(renderCreateCollectionForm());

  const resp = await api("/api/collections");
  clear(list);
  const items = resp.items || [];
  if (items.length === 0) {
    list.appendChild(emptyState("🗂️", "No collections yet", "Create one below to start storing data with a REST API and realtime updates for free."));
    return;
  }
  const table = el("table", {}, [
    el("thead", {}, el("tr", {}, [el("th", { text: "Name" }), el("th", { text: "Fields" }), el("th", { text: "" })])),
  ]);
  const tbody = el("tbody");
  for (const c of items) {
    const fieldNames = (c.schema.fields || []).map((f) => f.name + ":" + f.type).join(", ");
    const openLink = el("a", { href: "#/records/" + encodeURIComponent(c.name), text: c.name });
    const delBtn = deleteButton("Delete", 'Delete collection "' + c.name + '" and all its records? This cannot be undone.', async () => {
      await api("/api/collections/" + encodeURIComponent(c.name), { method: "DELETE" });
      toastSuccess('Collection "' + c.name + '" deleted');
      navigate();
    });
    tbody.appendChild(
      el("tr", {}, [el("td", {}, openLink), el("td", { class: "muted", text: fieldNames }), el("td", {}, delBtn)])
    );
  }
  table.appendChild(tbody);
  list.appendChild(table);
}

function renderCreateCollectionForm() {
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
    const removeBtn = el("button", { class: "btn-secondary", text: "×", type: "button" });
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
      navigate();
    } catch (e) {
      status.textContent = e.message;
      throw e;
    }
  });

  return el("div", { class: "card" }, [
    el("h3", { text: "New collection" }),
    el("div", { class: "col" }, [
      el("label", {}, ["Name", nameInput]),
      el("div", { class: "muted", text: "Fields" }),
      fieldsWrap,
      addFieldBtn,
      el("div", { class: "row" }, [submitBtn]),
      status,
    ]),
  ]);
}

// -- records -----------------------------------------------------------

async function renderRecords(container, name) {
  container.appendChild(el("a", { href: "#/collections", text: "← Collections", class: "back-link" }));
  container.appendChild(el("h2", { text: name }));

  const collection = await api("/api/collections/" + encodeURIComponent(name));
  const fields = collection.schema.fields || [];

  const status = el("div", { class: "error-text" });
  const table = el("table");
  const tbody = el("tbody");
  const thead = el(
    "thead",
    {},
    el(
      "tr",
      {},
      ["id", "owner_id"]
        .concat(fields.map((f) => f.name))
        .concat(["created", ""])
        .map((h) => el("th", { text: h }))
    )
  );
  table.appendChild(thead);
  table.appendChild(tbody);

  const loadMoreBtn = actionButton("Load more", { class: "btn-secondary" }, loadPage);
  let nextCursor = "";
  let loadedAny = false;
  const tableCard = el("div", { class: "card" }, [table, loadMoreBtn]);
  const emptyCard = emptyState("📄", "No records yet", 'Add one below — or POST to /api/collections/' + name + '/records.');
  emptyCard.classList.add("hidden");

  function renderRow(rec) {
    const cells = ["id", "owner_id"].concat(fields.map((f) => f.name)).map((key, i) => {
      let val = rec[key];
      if (val === null || val === undefined) val = "";
      else if (typeof val === "object") val = JSON.stringify(val);
      else val = String(val);
      return el("td", { class: i < 2 ? "id-cell" : "", text: val });
    });
    const delBtn = deleteButton("Delete", "Delete this record? This cannot be undone.", async () => {
      await api("/api/collections/" + encodeURIComponent(name) + "/records/" + rec.id, { method: "DELETE" });
      row.remove();
      toastSuccess("Record deleted");
      if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
    });
    const created = el("td", { class: "muted", text: rec.created || "" });
    const row = el("tr", { "data-id": rec.id }, cells.concat([created, el("td", {}, delBtn)]));
    return row;
  }

  // upsertRow is the single insert/update path for a record row, used by
  // both the New Record form's own success callback and the realtime SSE
  // handler below. Both can observe the same create (the SSE message and
  // the form's fetch response race independently), so insertion has to be
  // idempotent on rec.id rather than each caller inserting unconditionally.
  function upsertRow(rec) {
    const existing = tbody.querySelector('tr[data-id="' + rec.id + '"]');
    if (existing) existing.replaceWith(renderRow(rec));
    else tbody.insertBefore(renderRow(rec), tbody.firstChild);
    tableCard.classList.remove("hidden");
    emptyCard.classList.add("hidden");
  }

  async function loadPage() {
    const qs = new URLSearchParams({ limit: "30" });
    if (nextCursor) qs.set("cursor", nextCursor);
    const resp = await api("/api/collections/" + encodeURIComponent(name) + "/records?" + qs.toString());
    for (const rec of resp.items || []) { tbody.appendChild(renderRow(rec)); loadedAny = true; }
    nextCursor = resp.nextCursor || "";
    loadMoreBtn.style.display = nextCursor ? "" : "none";
    if (!loadedAny) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
  }

  const recordsPane = el("div", {}, [tableCard, emptyCard, renderCreateRecordForm(name, fields, upsertRow), status]);
  const apiPane = el("div", { class: "hidden" }, [renderAPISnippets(name, fields)]);

  const recordsTabBtn = el("button", { type: "button", class: "btn-secondary active", text: "Records" });
  const apiTabBtn = el("button", { type: "button", class: "btn-secondary", text: "API" });
  recordsTabBtn.addEventListener("click", () => {
    recordsTabBtn.classList.add("active");
    apiTabBtn.classList.remove("active");
    recordsPane.classList.remove("hidden");
    apiPane.classList.add("hidden");
  });
  apiTabBtn.addEventListener("click", () => {
    apiTabBtn.classList.add("active");
    recordsTabBtn.classList.remove("active");
    apiPane.classList.remove("hidden");
    recordsPane.classList.add("hidden");
  });

  container.appendChild(el("div", { class: "row", style: "margin-bottom:12px" }, [recordsTabBtn, apiTabBtn]));
  container.appendChild(recordsPane);
  container.appendChild(apiPane);

  await loadPage();

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

  const snippets = [
    {
      title: "List records",
      curl: `curl "${base}/api/collections/${enc}/records" \\\n  -H "Authorization: Bearer $TOKEN"`,
      js: `const { items } = await client.records("${name}").list();`,
    },
    {
      title: "Create a record",
      curl: `curl -X POST "${base}/api/collections/${enc}/records" \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '${exampleJSON}'`,
      js: `const record = await client.records("${name}").create(${exampleJSON});`,
    },
    {
      title: "Update a record",
      curl: `curl -X PATCH "${base}/api/collections/${enc}/records/RECORD_ID" \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '${exampleJSON}'`,
      js: `const record = await client.records("${name}").update("RECORD_ID", ${exampleJSON});`,
    },
    {
      title: "Delete a record",
      curl: `curl -X DELETE "${base}/api/collections/${enc}/records/RECORD_ID" \\\n  -H "Authorization: Bearer $TOKEN"`,
      js: `await client.records("${name}").delete("RECORD_ID");`,
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
          navigator.clipboard.writeText(code).then(() => toastSuccess(label + " copied"));
        },
      });
      return el("div", {}, [el("div", { class: "row", style: "justify-content:space-between" }, [el("span", { class: "muted", text: label }), copyBtn]), pre]);
    }
    return el("div", { class: "card" }, [el("h3", { text: s.title }), codeBlock("curl", s.curl), codeBlock("JS SDK", s.js)]);
  });

  return el("div", {}, [
    el("p", { class: "muted", text: "$TOKEN is a session token from /api/auth/login or /api/admins/login. client is a onebox-js OneboxClient instance." }),
    ...cards,
  ]);
}

function renderCreateRecordForm(collectionName, fields, onCreated) {
  const status = el("div", { class: "error-text" });
  const inputs = fields.map((f) => {
    if (f.type === "bool") return { field: f, input: el("input", { type: "checkbox" }) };
    if (f.type === "number") return { field: f, input: el("input", { type: "number", step: "any" }) };
    if (f.type === "json") return { field: f, input: el("textarea", { rows: "2", placeholder: "{}" }) };
    return { field: f, input: el("input", { type: "text" }) };
  });

  const submitBtn = actionButton("Add record", {}, async () => {
    clear(status);
    const body = {};
    try {
      for (const { field, input } of inputs) {
        if (field.type === "bool") {
          body[field.name] = input.checked;
        } else if (field.type === "number") {
          if (input.value !== "") body[field.name] = Number(input.value);
        } else if (field.type === "json") {
          if (input.value.trim()) body[field.name] = JSON.parse(input.value);
        } else if (input.value !== "") {
          body[field.name] = input.value;
        }
      }
      const rec = await api("/api/collections/" + encodeURIComponent(collectionName) + "/records", {
        method: "POST",
        body: JSON.stringify(body),
      });
      onCreated(rec);
      toastSuccess("Record saved");
      for (const { input, field } of inputs) {
        if (field.type === "bool") input.checked = false;
        else input.value = "";
      }
    } catch (e) {
      status.textContent = e.message;
      throw e;
    }
  });

  return el("div", { class: "card" }, [
    el("h3", { text: "New record" }),
    el(
      "div",
      { class: "col" },
      inputs
        .map(({ field, input }) => el("label", {}, [field.name + (field.required ? " *" : ""), input]))
        .concat([submitBtn, status])
    ),
  ]);
}

// -- files ---------------------------------------------------------------

async function renderFiles(container) {
  container.appendChild(el("h2", { text: "File Storage" }));

  const fileInput = el("input", { type: "file" });
  const uploadStatus = el("div", { class: "error-text" });
  const table = el("table", {}, [
    el(
      "thead",
      {},
      el("tr", {}, [el("th", { text: "Filename" }), el("th", { text: "Size" }), el("th", { text: "Mime" }), el("th", { text: "Created" }), el("th", { text: "" })])
    ),
  ]);
  const tbody = el("tbody");
  table.appendChild(tbody);
  const tableCard = el("div", { class: "card" }, [table]);
  const emptyCard = emptyState("📁", "No files yet", "Upload one above.");
  emptyCard.classList.add("hidden");

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

  function renderRow(f) {
    const dlBtn = el("button", { class: "btn-secondary", text: "Download", onclick: () => downloadFile(f.id, f.filename) });
    const delBtn = deleteButton("Delete", 'Delete "' + f.filename + '"? This cannot be undone.', async () => {
      await api("/api/files/" + f.id, { method: "DELETE" });
      row.remove();
      toastSuccess("File deleted");
      if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
    });
    const row = el("tr", {}, [
      el("td", { text: f.filename }),
      el("td", { text: String(f.size) }),
      el("td", { class: "muted", text: f.mime }),
      el("td", { class: "muted", text: f.created }),
      el("td", { class: "row" }, [dlBtn, delBtn]),
    ]);
    return row;
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
    if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
  }
  tableCard.appendChild(loadMoreBtn);

  const uploadBtn = actionButton("Upload", {}, async () => {
    clear(uploadStatus);
    if (!fileInput.files[0]) return;
    const form = new FormData();
    form.append("file", fileInput.files[0]);
    try {
      const rec = await api("/api/files", { method: "POST", body: form });
      tbody.insertBefore(renderRow(rec), tbody.firstChild);
      tableCard.classList.remove("hidden");
      emptyCard.classList.add("hidden");
      toastSuccess('"' + rec.filename + '" uploaded');
      fileInput.value = "";
    } catch (e) {
      uploadStatus.textContent = e.message;
      throw e;
    }
  });

  container.appendChild(el("div", { class: "card" }, [el("div", { class: "row" }, [fileInput, uploadBtn]), uploadStatus]));
  container.appendChild(tableCard);
  container.appendChild(emptyCard);

  await loadPage();
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
  container.appendChild(el("h2", { text: "RAG sources" }));

  const fileInput = el("input", { type: "file", accept: ".pdf,.txt,.md,.docx" });
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
  const tableCard = el("div", { class: "card" }, [table]);
  const emptyCard = emptyState("📚", "No documents ingested yet", "Upload a PDF, TXT, MD, or DOCX above to enable grounded Q&A.");
  emptyCard.classList.add("hidden");

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
    if (!tbody.firstChild) { tableCard.classList.add("hidden"); emptyCard.classList.remove("hidden"); }
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

  const uploadBtn = actionButton("Upload & ingest", {}, async () => {
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
      throw e;
    }
  });

  container.appendChild(
    el("div", { class: "card" }, [
      el("div", { class: "row" }, [fileInput, uploadBtn]),
      el("p", { class: "muted", text: "Accepts .pdf, .txt, .md, .docx — ingestion runs in the background." }),
      uploadStatus,
    ])
  );
  container.appendChild(tableCard);
  container.appendChild(emptyCard);

  await loadPage();
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
  container.appendChild(el("h2", { text: "Usage" }));

  const resp = await api("/api/usage");
  const items = resp.items || [];

  container.appendChild(
    el("div", { class: "card" }, [
      el("h3", { text: "Estimated spend (shown range)" }),
      el("p", { style: "font-size:1.6rem;font-weight:700;margin:0", text: "$" + resp.total_cost_estimate.toFixed(4) }),
    ])
  );

  if (items.length === 0) {
    container.appendChild(el("div", { class: "card" }, [emptyState("📊", "No usage recorded yet", "Usage from /api/llm/chat and /api/rag/answer calls will show up here.")]));
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
  container.appendChild(el("h2", {}, ["📜 Logs"]));

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
  const tableCard = el("div", { class: "card" }, [table]);
  const emptyCard = emptyState("📜", "No requests logged yet", "API requests will show up here as they happen.");
  emptyCard.classList.add("hidden");

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
  container.appendChild(tableCard);
  container.appendChild(emptyCard);

  await load();
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
  container.appendChild(el("h2", {}, ["🗃️ Backups"]));

  const exportBtn = actionButton("Download full backup (.zip)", {}, async () => {
    await downloadAuthed("/api/backups/export", "onebox-backup-" + new Date().toISOString().slice(0, 10) + ".zip");
    toastSuccess("Backup downloaded");
  });

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
      el("h3", { text: "Full backup" }),
      el("p", { class: "muted", text: "A single .zip with the database and every stored file. Restoring merges the backup's data into this instance's matching tables — collections only present in the backup are skipped and reported." }),
      el("div", { class: "row" }, [exportBtn]),
      el("div", { class: "row", style: "margin-top:12px" }, [restoreInput, restoreBtn]),
      restoreStatus,
    ])
  );

  const collectionsCard = el("div", { class: "col" }, [el("p", { class: "muted", text: "Loading collections…" })]);
  container.appendChild(el("div", { class: "card" }, [el("h3", { text: "Per-collection export / import" }), collectionsCard]));

  const resp = await api("/api/collections");
  clear(collectionsCard);
  const items = resp.items || [];
  if (items.length === 0) {
    collectionsCard.appendChild(emptyState("🗂️", "No collections yet", "Create one on the Collections page first."));
    return;
  }
  for (const c of items) {
    collectionsCard.appendChild(renderCollectionBackupRow(c));
  }
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
  container.appendChild(el("h2", { text: "Settings" }));

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
  const anthropicModel = textField("anthropic_model", "Model", "claude-sonnet-5");
  const anthropicTest = testButton("anthropic", () => ({ api_key: anthropicKey.input.value }));

  const openaiKey = secretField("openai_api_key", "OpenAI API key");
  const openaiBaseURL = textField("openai_base_url", "Base URL", "https://api.openai.com/v1");
  const openaiTest = testButton("openai", () => ({ api_key: openaiKey.input.value, base_url: openaiBaseURL.input.value }));

  const ollamaBaseURL = textField("ollama_base_url", "Base URL", "http://localhost:11434");
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

  const allFields = [anthropicKey, anthropicModel, openaiKey, openaiBaseURL, ollamaBaseURL, embeddingProvider, embeddingKey, embeddingBaseURL, embeddingModel];

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
      el("h3", {}, ["🤖 Anthropic"]),
      el("p", { class: "muted", text: "Powers /api/llm/chat for claude-* models and is the default for /api/rag/answer." }),
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
      el("h3", {}, ["🌐 OpenAI-compatible"]),
      el("p", { class: "muted", text: "Powers /api/llm/chat for gpt-*/o1-*/o3-* models. Also works with any OpenAI-compatible API by changing the base URL." }),
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
      el("h3", {}, ["🖥️ Ollama (local)"]),
      el("p", { class: "muted", text: "Runs any other model name against a local Ollama instance — no API key needed. Shared between the LLM gateway and embeddings (below) when Ollama is selected." }),
      el("div", { class: "col" }, [el("label", {}, [ollamaBaseURL.label, ollamaBaseURL.input]), ollamaTest.btn, ollamaTest.result]),
    ])
  );

  container.appendChild(
    el("div", { class: "card provider-card" }, [
      el("h3", {}, ["📚 Embedding provider"]),
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
