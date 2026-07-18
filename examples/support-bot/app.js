// onebox example: support bot. Plain fetch(), no SDK/build step.
const BASE_URL = "http://localhost:8090";
const TOKEN_KEY = "onebox_supportbot_token";

async function api(path, opts = {}) {
  const token = localStorage.getItem(TOKEN_KEY);
  const headers = new Headers(opts.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (opts.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const res = await fetch(BASE_URL + path, { ...opts, headers });
  const isJSON = (res.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await res.json() : await res.text();
  if (!res.ok) throw new Error((isJSON && body.message) || String(body));
  return body;
}

// A support widget's visitors aren't asked to sign up explicitly — a
// throwaway onebox identity is created transparently on first message.
async function ensureSession() {
  if (localStorage.getItem(TOKEN_KEY)) return;
  const email = `visitor-${Date.now()}@example.com`;
  const password = crypto.randomUUID();
  const { token } = await api("/api/auth/signup", { method: "POST", body: JSON.stringify({ email, password }) });
  localStorage.setItem(TOKEN_KEY, token);
}

const messagesEl = document.getElementById("messages");
const input = document.getElementById("input");

function addMessage(text, role) {
  const div = document.createElement("div");
  div.className = "msg " + role;
  div.textContent = text;
  messagesEl.appendChild(div);
  messagesEl.scrollTop = messagesEl.scrollHeight;
  return div;
}

async function send() {
  const question = input.value.trim();
  if (!question) return;
  input.value = "";
  addMessage(question, "user");
  const thinking = addMessage("...", "bot");

  try {
    await ensureSession();
    const { answer, sources } = await api("/api/rag/answer", {
      method: "POST",
      body: JSON.stringify({ query: question, top_k: 5 }),
    });
    thinking.textContent = answer;
    if (sources.length === 0) {
      addMessage("(No matching documents were found — has your team uploaded any yet?)", "system");
    }
  } catch (e) {
    thinking.textContent = "Sorry, something went wrong: " + e.message;
  }
}

document.getElementById("sendBtn").addEventListener("click", send);
input.addEventListener("keydown", (e) => {
  if (e.key === "Enter") send();
});
