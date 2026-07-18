// onebox admin dashboard — plain JS, no build step, no framework.
// Hash-based routing; every view is a render(container) function.

const TOKEN_KEY = "onebox_admin_token";

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

function errorBox(message) {
  return el("div", { class: "error", text: message });
}

// -- router --------------------------------------------------------------

const app = document.getElementById("app");
const topbar = document.getElementById("topbar");
document.getElementById("logoutBtn").addEventListener("click", () => {
  clearToken();
  location.hash = "#/login";
});

function currentRoute() {
  const hash = location.hash.replace(/^#\/?/, "");
  const parts = hash.split("/").filter(Boolean);
  return parts;
}

async function navigate() {
  const authed = !!getToken();
  topbar.classList.toggle("hidden", !authed);
  clear(app);

  const parts = currentRoute();
  if (!authed) {
    renderLogin(app);
    return;
  }
  if (parts.length === 0) {
    location.hash = "#/collections";
    return;
  }
  try {
    if (parts[0] === "collections" && parts.length === 1) {
      await renderCollections(app);
    } else if (parts[0] === "records" && parts.length === 2) {
      await renderRecords(app, decodeURIComponent(parts[1]));
    } else if (parts[0] === "files") {
      await renderFiles(app);
    } else if (parts[0] === "rag") {
      await renderRAGSources(app);
    } else if (parts[0] === "usage") {
      await renderUsage(app);
    } else if (parts[0] === "settings") {
      await renderSettings(app);
    } else {
      location.hash = "#/collections";
    }
  } catch (e) {
    app.appendChild(errorBox(e.message));
  }
}

window.addEventListener("hashchange", navigate);
window.addEventListener("DOMContentLoaded", navigate);

// -- login -----------------------------------------------------------

function renderLogin(container) {
  const email = el("input", { type: "email", placeholder: "email", autocomplete: "username" });
  const password = el("input", { type: "password", placeholder: "password", autocomplete: "current-password" });
  const status = el("div", { class: "error" });

  async function submit(endpoint) {
    clear(status);
    try {
      const resp = await api("/api/admins/" + endpoint, {
        method: "POST",
        body: JSON.stringify({ email: email.value, password: password.value }),
      });
      setToken(resp.token);
      location.hash = "#/collections";
      navigate();
    } catch (e) {
      status.appendChild(document.createTextNode(e.message));
    }
  }

  const loginBtn = el("button", { text: "Log in", onclick: () => submit("login") });
  const bootstrapBtn = el("button", { class: "secondary", text: "Create first admin account", onclick: () => submit("signup") });

  container.appendChild(
    el("div", { class: "login-box card" }, [
      el("h2", { text: "onebox admin" }),
      el("div", { class: "col" }, [email, password, el("div", { class: "row" }, [loginBtn, bootstrapBtn]), status]),
      el("p", { class: "muted", text: "New install? Use \"Create first admin account\" once — it only works before any admin exists." }),
    ])
  );
}

// -- collections -------------------------------------------------------

const FIELD_TYPES = ["text", "number", "bool", "date", "json"];
const RULE_KINDS = ["authenticated", "public", "owner"];

async function renderCollections(container) {
  const list = el("div", { class: "card" }, [el("p", { class: "muted", text: "Loading…" })]);
  container.appendChild(el("h2", { text: "Collections" }));
  container.appendChild(list);
  container.appendChild(renderCreateCollectionForm());

  const resp = await api("/api/collections");
  clear(list);
  const items = resp.items || [];
  if (items.length === 0) {
    list.appendChild(el("p", { class: "muted", text: "No collections yet — create one below." }));
    return;
  }
  const table = el("table", {}, [
    el("thead", {}, el("tr", {}, [el("th", { text: "Name" }), el("th", { text: "Fields" }), el("th", { text: "" })])),
  ]);
  const tbody = el("tbody");
  for (const c of items) {
    const fieldNames = (c.schema.fields || []).map((f) => f.name + ":" + f.type).join(", ");
    const openLink = el("a", { href: "#/records/" + encodeURIComponent(c.name), text: c.name });
    const delBtn = el("button", {
      class: "danger",
      text: "Delete",
      onclick: async () => {
        if (!confirm('Delete collection "' + c.name + '" and all its records?')) return;
        await api("/api/collections/" + encodeURIComponent(c.name), { method: "DELETE" });
        navigate();
      },
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
  const status = el("div", { class: "error" });
  const fields = [];

  function addFieldRow(name = "", type = "text", required = false) {
    const nameEl = el("input", { placeholder: "field name", value: name });
    const typeEl = el("select", {}, FIELD_TYPES.map((t) => el("option", { value: t, text: t })));
    typeEl.value = type;
    const reqEl = el("input", { type: "checkbox" });
    reqEl.checked = required;
    const reqLabel = el("label", {}, [reqEl, " required"]);
    const removeBtn = el("button", { class: "secondary", text: "×" });
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

  const addFieldBtn = el("button", { class: "secondary", text: "+ field", onclick: () => addFieldRow() });
  const submitBtn = el("button", {
    text: "Create collection",
    onclick: async () => {
      clear(status);
      const schema = {
        fields: fields
          .filter((f) => f.nameEl.value.trim())
          .map((f) => ({ name: f.nameEl.value.trim(), type: f.typeEl.value, required: f.reqEl.checked })),
      };
      try {
        await api("/api/collections", {
          method: "POST",
          body: JSON.stringify({ name: nameInput.value.trim(), schema }),
        });
        navigate();
      } catch (e) {
        status.appendChild(document.createTextNode(e.message));
      }
    },
  });

  return el("div", { class: "card" }, [
    el("h3", { text: "New collection" }),
    el("div", { class: "col" }, [
      el("label", {}, ["Name ", nameInput]),
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
  container.appendChild(el("a", { href: "#/collections", text: "← Collections", class: "muted" }));
  container.appendChild(el("h2", { text: name }));

  const collection = await api("/api/collections/" + encodeURIComponent(name));
  const fields = collection.schema.fields || [];

  const status = el("div", { class: "error" });
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

  const loadMoreBtn = el("button", { class: "secondary", text: "Load more" });
  let nextCursor = "";

  function renderRow(rec) {
    const cells = ["id", "owner_id"].concat(fields.map((f) => f.name)).map((key) => {
      let val = rec[key];
      if (val === null || val === undefined) val = "";
      else if (typeof val === "object") val = JSON.stringify(val);
      else val = String(val);
      return el("td", { text: val });
    });
    const delBtn = el("button", {
      class: "danger",
      text: "Delete",
      onclick: async () => {
        if (!confirm("Delete record " + rec.id + "?")) return;
        await api("/api/collections/" + encodeURIComponent(name) + "/records/" + rec.id, { method: "DELETE" });
        row.remove();
      },
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
  }

  async function loadPage() {
    const qs = new URLSearchParams({ limit: "30" });
    if (nextCursor) qs.set("cursor", nextCursor);
    const resp = await api("/api/collections/" + encodeURIComponent(name) + "/records?" + qs.toString());
    for (const rec of resp.items || []) tbody.appendChild(renderRow(rec));
    nextCursor = resp.nextCursor || "";
    loadMoreBtn.style.display = nextCursor ? "" : "none";
  }
  loadMoreBtn.addEventListener("click", loadPage);

  container.appendChild(el("div", { class: "card" }, [table, loadMoreBtn]));
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
  const status = el("div", { class: "error" });
  const inputs = fields.map((f) => {
    if (f.type === "bool") return { field: f, input: el("input", { type: "checkbox" }) };
    if (f.type === "number") return { field: f, input: el("input", { type: "number", step: "any" }) };
    if (f.type === "json") return { field: f, input: el("textarea", { rows: "2", placeholder: "{}" }) };
    return { field: f, input: el("input", { type: "text" }) };
  });

  const submitBtn = el("button", {
    text: "Add record",
    onclick: async () => {
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
        for (const { input, field } of inputs) {
          if (field.type === "bool") input.checked = false;
          else input.value = "";
        }
      } catch (e) {
        status.appendChild(document.createTextNode(e.message));
      }
    },
  });

  return el("div", { class: "card" }, [
    el("h3", { text: "New record" }),
    el(
      "div",
      { class: "col" },
      inputs
        .map(({ field, input }) => el("label", {}, [field.name + (field.required ? " *" : "") + " ", input]))
        .concat([submitBtn, status])
    ),
  ]);
}

// -- files ---------------------------------------------------------------

async function renderFiles(container) {
  container.appendChild(el("h2", { text: "Files" }));

  const fileInput = el("input", { type: "file" });
  const uploadStatus = el("div", { class: "error" });
  const table = el("table", {}, [
    el(
      "thead",
      {},
      el("tr", {}, [el("th", { text: "Filename" }), el("th", { text: "Size" }), el("th", { text: "Mime" }), el("th", { text: "Created" }), el("th", { text: "" })])
    ),
  ]);
  const tbody = el("tbody");
  table.appendChild(tbody);

  async function downloadFile(id, filename) {
    const res = await fetch("/api/files/" + id, { headers: { Authorization: "Bearer " + getToken() } });
    if (!res.ok) { alert("download failed"); return; }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = el("a", { href: url, download: filename });
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }

  function renderRow(f) {
    const dlBtn = el("button", { class: "secondary", text: "Download", onclick: () => downloadFile(f.id, f.filename) });
    const delBtn = el("button", {
      class: "danger",
      text: "Delete",
      onclick: async () => {
        if (!confirm('Delete "' + f.filename + '"?')) return;
        await api("/api/files/" + f.id, { method: "DELETE" });
        row.remove();
      },
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

  const loadMoreBtn = el("button", { class: "secondary", text: "Load more" });
  let nextCursor = "";
  async function loadPage() {
    const qs = new URLSearchParams({ limit: "30" });
    if (nextCursor) qs.set("cursor", nextCursor);
    const resp = await api("/api/files?" + qs.toString());
    for (const f of resp.items || []) tbody.appendChild(renderRow(f));
    nextCursor = resp.nextCursor || "";
    loadMoreBtn.style.display = nextCursor ? "" : "none";
  }
  loadMoreBtn.addEventListener("click", loadPage);

  const uploadBtn = el("button", {
    text: "Upload",
    onclick: async () => {
      clear(uploadStatus);
      if (!fileInput.files[0]) return;
      const form = new FormData();
      form.append("file", fileInput.files[0]);
      try {
        const rec = await api("/api/files", { method: "POST", body: form });
        tbody.insertBefore(renderRow(rec), tbody.firstChild);
        fileInput.value = "";
      } catch (e) {
        uploadStatus.appendChild(document.createTextNode(e.message));
      }
    },
  });

  container.appendChild(el("div", { class: "card" }, [el("div", { class: "row" }, [fileInput, uploadBtn]), uploadStatus]));
  container.appendChild(el("div", { class: "card" }, [table, loadMoreBtn]));

  await loadPage();
}

// -- rag sources -----------------------------------------------------------

async function renderRAGSources(container) {
  container.appendChild(el("h2", { text: "RAG sources" }));

  const fileInput = el("input", { type: "file", accept: ".pdf,.txt,.md,.docx" });
  const uploadStatus = el("div", { class: "error" });
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

  function renderRow(src) {
    const delBtn = el("button", {
      class: "danger",
      text: "Delete",
      onclick: async () => {
        if (!confirm('Delete "' + src.filename + '"?')) return;
        await api("/api/rag/sources/" + src.id, { method: "DELETE" });
        row.remove();
      },
    });
    let statusText = src.status;
    if (src.status === "error" && src.error) statusText += ": " + src.error;
    const row = el("tr", { "data-id": src.id }, [
      el("td", { text: src.filename }),
      el("td", { class: src.status === "error" ? "error" : "muted", text: statusText }),
      el("td", { text: String(src.chunk_count) }),
      el("td", { class: "muted", text: src.created }),
      el("td", {}, delBtn),
    ]);
    return row;
  }

  const loadMoreBtn = el("button", { class: "secondary", text: "Load more" });
  let nextCursor = "";
  async function loadPage() {
    const qs = new URLSearchParams({ limit: "30" });
    if (nextCursor) qs.set("cursor", nextCursor);
    const resp = await api("/api/rag/sources?" + qs.toString());
    for (const s of resp.items || []) tbody.appendChild(renderRow(s));
    nextCursor = resp.nextCursor || "";
    loadMoreBtn.style.display = nextCursor ? "" : "none";
  }
  loadMoreBtn.addEventListener("click", loadPage);

  // Ingestion (extract/chunk/embed) runs in the background server-side;
  // poll the source's status until it leaves pending/processing so the
  // row updates without a manual refresh.
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
      if (src.status === "done" || src.status === "error") return;
    }
  }

  const uploadBtn = el("button", {
    text: "Upload & ingest",
    onclick: async () => {
      clear(uploadStatus);
      if (!fileInput.files[0]) return;
      const form = new FormData();
      form.append("file", fileInput.files[0]);
      try {
        const src = await api("/api/rag/sources", { method: "POST", body: form });
        tbody.insertBefore(renderRow(src), tbody.firstChild);
        fileInput.value = "";
        pollUntilDone(src.id);
      } catch (e) {
        uploadStatus.appendChild(document.createTextNode(e.message));
      }
    },
  });

  container.appendChild(
    el("div", { class: "card" }, [
      el("div", { class: "row" }, [fileInput, uploadBtn]),
      el("p", { class: "muted", text: "Accepts .pdf, .txt, .md, .docx — ingestion runs in the background." }),
      uploadStatus,
    ])
  );
  container.appendChild(el("div", { class: "card" }, [table, loadMoreBtn]));

  await loadPage();
}

// -- usage -----------------------------------------------------------------

async function renderUsage(container) {
  container.appendChild(el("h2", { text: "Usage" }));

  const resp = await api("/api/usage");
  const items = resp.items || [];

  container.appendChild(
    el("div", { class: "card" }, [
      el("h3", { text: "Estimated spend (shown range)" }),
      el("p", {}, ["$" + resp.total_cost_estimate.toFixed(4)]),
    ])
  );

  if (items.length === 0) {
    container.appendChild(el("div", { class: "card" }, [el("p", { class: "muted", text: "No usage recorded yet." })]));
    return;
  }

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
        el("td", { class: "muted", text: u.user_id || "" }),
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
  const status = el("div", { class: "error" });

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

  const saveBtn = el("button", {
    text: "Save",
    onclick: async () => {
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
        navigate(); // re-fetch so secret fields show the fresh masked state
      } catch (e) {
        status.appendChild(document.createTextNode(e.message));
      }
    },
  });

  container.appendChild(
    el("div", { class: "card" }, [
      el("h3", { text: "LLM & embedding providers" }),
      el("p", { class: "muted", text: "Keys are encrypted at rest and never shown again once saved. Saving applies immediately, no restart needed." }),
      el(
        "div",
        { class: "col" },
        fields.map((f) => el("label", {}, [f.label + " ", f.input])).concat([saveBtn, status])
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
