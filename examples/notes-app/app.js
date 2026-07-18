// onebox example: AI notes. Plain fetch(), no SDK/build step.
const BASE_URL = "http://localhost:8090";
const TOKEN_KEY = "onebox_notesapp_token";

async function api(path, opts = {}) {
  const token = localStorage.getItem(TOKEN_KEY);
  const headers = new Headers(opts.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (opts.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const res = await fetch(BASE_URL + path, { ...opts, headers });
  if (res.status === 204) return null;
  const isJSON = (res.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await res.json() : await res.text();
  if (!res.ok) throw new Error((isJSON && body.message) || String(body));
  return body;
}

const authEl = document.getElementById("auth");
const appEl = document.getElementById("app");
const authStatus = document.getElementById("authStatus");

document.getElementById("signupBtn").addEventListener("click", () => authenticate("signup"));
document.getElementById("loginBtn").addEventListener("click", () => authenticate("login"));

async function authenticate(kind) {
  authStatus.textContent = "";
  const email = document.getElementById("email").value;
  const password = document.getElementById("password").value;
  try {
    const { token } = await api(`/api/auth/${kind}`, { method: "POST", body: JSON.stringify({ email, password }) });
    localStorage.setItem(TOKEN_KEY, token);
    showApp();
  } catch (e) {
    authStatus.textContent = e.message;
  }
}

function showApp() {
  authEl.style.display = "none";
  appEl.style.display = "block";
  loadNotes();
}

const notesList = document.getElementById("notesList");

async function loadNotes() {
  notesList.innerHTML = "";
  let resp;
  try {
    resp = await api("/api/collections/notes/records?sort=-created&limit=50");
  } catch (e) {
    notesList.textContent =
      'Could not load notes: ' + e.message + '. Did you create the "notes" collection? See README.';
    return;
  }
  for (const note of resp.items) notesList.appendChild(renderNote(note));
}

function renderNote(note) {
  const div = document.createElement("div");
  div.className = "note";

  const body = document.createElement("div");
  body.textContent = note.body;

  const summarizeBtn = document.createElement("button");
  summarizeBtn.textContent = "Summarize with AI";
  const summaryEl = document.createElement("div");
  summaryEl.className = "summary";
  summarizeBtn.addEventListener("click", async () => {
    summaryEl.textContent = "thinking...";
    try {
      const { content } = await api("/api/llm/chat", {
        method: "POST",
        body: JSON.stringify({
          model: "claude-sonnet-5",
          messages: [
            { role: "system", content: "Summarize the user's note in one short sentence." },
            { role: "user", content: note.body },
          ],
        }),
      });
      summaryEl.textContent = content;
    } catch (e) {
      summaryEl.textContent = "error: " + e.message;
    }
  });

  const deleteBtn = document.createElement("button");
  deleteBtn.textContent = "Delete";
  deleteBtn.addEventListener("click", async () => {
    await api(`/api/collections/notes/records/${note.id}`, { method: "DELETE" });
    div.remove();
  });

  div.append(body, summarizeBtn, deleteBtn, summaryEl);
  return div;
}

document.getElementById("addBtn").addEventListener("click", async () => {
  const bodyInput = document.getElementById("bodyInput");
  if (!bodyInput.value.trim()) return;
  const note = await api("/api/collections/notes/records", {
    method: "POST",
    body: JSON.stringify({ body: bodyInput.value.trim() }),
  });
  bodyInput.value = "";
  notesList.prepend(renderNote(note));
});

if (localStorage.getItem(TOKEN_KEY)) showApp();
