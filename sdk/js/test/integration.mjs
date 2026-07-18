// Live integration smoke test against a real running onebox server.
// Not part of `npm test` (no server there) — run manually:
//   node test/integration.mjs
import assert from "node:assert/strict";
import { OneboxClient } from "../dist/index.js";

const client = new OneboxClient({ baseUrl: "http://localhost:8090" });

console.log("admin bootstrap...");
const admin = await client.admins.signup("admin@example.com", "hunter22222");
assert.ok(admin.token);
client.setToken(admin.token);

console.log("create collection...");
const collection = await client.collections.create("notes", {
  fields: [{ name: "body", type: "text", required: true }],
});
assert.equal(collection.name, "notes");

console.log("user signup...");
const userClient = new OneboxClient({ baseUrl: "http://localhost:8090" });
const user = await userClient.auth.signup("writer@example.com", "hunter22222");
userClient.setToken(user.token);

console.log("create + list records...");
const notes = userClient.records("notes");
const created = await notes.create({ body: "hello from the SDK integration test" });
assert.ok(created.id);
const listed = await notes.list();
assert.equal(listed.items.length, 1);
assert.equal(listed.items[0].body, "hello from the SDK integration test");

console.log("update + delete record...");
const updated = await notes.update(created.id, { body: "updated body" });
assert.equal(updated.body, "updated body");
await notes.delete(created.id);
const afterDelete = await notes.list();
assert.equal(afterDelete.items.length, 0);

console.log("file upload + download + delete...");
const blob = new Blob(["hello sdk file"], { type: "text/plain" });
const file = await userClient.files.upload(blob, "hello.txt");
assert.equal(file.filename, "hello.txt");
const downloaded = await userClient.files.download(file.id);
const text = await downloaded.text();
assert.equal(text, "hello sdk file");
await userClient.files.delete(file.id);

console.log("llm.chat surfaces the server's error cleanly when unconfigured...");
try {
  await userClient.llm.chat("gpt-4o", [{ role: "user", content: "hi" }]);
  throw new Error("expected llm.chat to reject with no OpenAI key configured");
} catch (e) {
  assert.ok(e.message.includes("OpenAI"), `expected error mentioning OpenAI, got: ${e.message}`);
}

console.log("ALL SDK INTEGRATION CHECKS PASSED");
