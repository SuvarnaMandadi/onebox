// onebox admin dashboard — plain JS, no build step, no framework.
// Hash-based routing; every view is a render(container) function.

const TOKEN_KEY = "onebox_admin_token";
const THEME_KEY = "onebox_theme";

function getToken() { return localStorage.getItem(TOKEN_KEY) || ""; }
function setToken(t) { localStorage.setItem(TOKEN_KEY, t); }
function clearToken() { localStorage.removeItem(TOKEN_KEY); }

// api() wraps fetch: sends the admin bearer token, parses JSON, and
// throws a readable Error using the server's {code,message} envelope.
async function api(path, opts = {}) {
  const headers = Object.assign({}, opts.headers || {});
  const token = getToken();
  if (token) headers["Authorization"] = "Bearer " + token;
  if (opts.body && !(opts.body instanceof FormData)) {
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(path, Object.assign({}, opts, { headers }));
  if (res.status === 401) {
    clearToken();
    location.hash = "#/login";
    throw new Error("session expired, please log in again");
  }
  if (res.status === 204) return null;
  const isJSON = (res.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await res.json() : await res.text();
  if (!res.ok) {
    const msg = isJSON && body && body.message ? body.message : String(body);
    throw new Error(msg);
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

document.getElementById("logoutBtn").addEventListener("click", () => {
  clearToken();
  location.hash = "#/login";
  navigate();
});

function currentRoute() {
  const hash = location.hash.replace(/^#\/?/, "");
  const parts = hash.split("/").filter(Boolean);
  return parts;
}

function updateActiveNav(routeName) {
  document.querySelectorAll("#sidebar nav a").forEach((a) => {
    a.classList.toggle("active", a.dataset.route === routeName);
  });
}

async function navigate() {
  const authed = !!getToken();
  shell.classList.toggle("hidden", !authed);
  loginRoot.classList.toggle("hidden", authed);

  if (!authed) {
    clear(loginRoot);
    renderLogin(loginRoot);
    return;
  }

  clear(app);
  const parts = currentRoute();
  if (parts.length === 0) {
    location.hash = "#/collections";
    return;
  }
  try {
    if (parts[0] === "collections" && parts.length === 1) {
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
    } else if (parts[0] === "settings") {
      updateActiveNav("settings");
      await renderSettings(app);
    } else {
      location.hash = "#/collections";
    }
  } catch (e) {
    app.appendChild(el("div", { class: "card error-text", text: e.message }));
    toastError(e.message);
  }
}

window.addEventListener("hashchange", navigate);
window.addEventListener("DOMContentLoaded", navigate);

// -- login -----------------------------------------------------------

function renderLogin(container) {
  const email = el("input", { type: "email", placeholder: "email", autocomplete: "username" });
  const password = el("input", { type: "password", placeholder: "password", autocomplete: "current-password" });
  const status = el("div", { class: "error-text" });

  async function submit(endpoint) {
    clear(status);
    try {
      const resp = await api("/api/admins/" + endpoint, {
        method: "POST",
        body: JSON.stringify({ email: email.value, password: password.value }),
      });
      setToken(resp.token);
      toastSuccess(endpoint === "signup" ? "Admin account created" : "Logged in");
      location.hash = "#/collections";
      navigate();
    } catch (e) {
      status.textContent = e.message;
    }
  }

  const loginBtn = actionButton("Log in", {}, () => submit("login"));
  const bootstrapBtn = actionButton("Create first admin account", { class: "btn-secondary" }, () => submit("signup"));

  container.appendChild(
    el("div", { class: "card login-card" }, [
      el("h2", { text: "onebox admin" }),
      el("div", { class: "col" }, [email, password, el("div", { class: "row" }, [loginBtn, bootstrapBtn]), status]),
      el("p", { class: "muted", text: "New install? Use \"Create first admin account\" once — it only works before any admin exists." }),
    ])
  );
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

  container.appendChild(tableCard);
  container.appendChild(emptyCard);
  container.appendChild(renderCreateRecordForm(name, fields, upsertRow));
  container.appendChild(status);

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
  container.appendChild(el("h2", { text: "Files" }));

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
  function textField(key, label, placeholder) {
    const input = el("input", { type: "text", value: current[key] || "", placeholder: placeholder || "" });
    return { key, input, secret: false, label };
  }
  function selectField(key, label, options) {
    const input = el("select", {}, options.map((o) => el("option", { value: o, text: o })));
    input.value = current[key] || options[0];
    return { key, input, secret: false, label };
  }

  const fields = [
    secretField("anthropic_api_key", "Anthropic API key"),
    textField("anthropic_model", "Anthropic model", "claude-sonnet-5"),
    secretField("openai_api_key", "OpenAI API key"),
    textField("openai_base_url", "OpenAI base URL", "https://api.openai.com/v1"),
    selectField("embedding_provider", "Embedding provider", ["openai", "ollama"]),
    secretField("embedding_api_key", "Embedding API key"),
    textField("embedding_base_url", "Embedding base URL"),
    textField("embedding_model", "Embedding model", "text-embedding-3-small"),
    textField("ollama_base_url", "Ollama base URL", "http://localhost:11434"),
  ];

  const saveBtn = actionButton("Save", { loadingLabel: "Saving..." }, async () => {
    clear(status);
    const body = {};
    for (const f of fields) {
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
    el("div", { class: "card" }, [
      el("h3", { text: "LLM & embedding providers" }),
      el("p", { class: "muted", text: "Keys are encrypted at rest and never shown again once saved. Saving applies immediately, no restart needed." }),
      el(
        "div",
        { class: "col" },
        fields.map((f) => el("label", {}, [f.label, f.input])).concat([saveBtn, status])
      ),
    ])
  );

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
