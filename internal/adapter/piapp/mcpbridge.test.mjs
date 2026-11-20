// Run with: npm run test:pi
import { test, after } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const dir = mkdtempSync(join(tmpdir(), "mcpbridge-"));
const cancelled = join(dir, "cancelled");

process.env.OMNIPLEX_PI_MCP = JSON.stringify([
  {
    name: "fake",
    command: process.execPath,
    args: [join(here, "testdata", "fakemcp.mjs")],
    env: { FAKE_MCP_TOKEN: "tok-123", FAKE_MCP_CANCELLED: cancelled },
  },
  { name: "broken", command: join(dir, "no-such-server") },
]);
const { default: mcpBridge } = await import("./mcpbridge.mjs");

// fakePi is the slice of Pi's ExtensionAPI the bridge uses.
function fakePi() {
  const handlers = {};
  const tools = new Map();
  return {
    handlers,
    tools,
    on: (event, fn) => (handlers[event] = fn),
    registerTool: (def) => tools.set(def.name, def),
  };
}

const pi = fakePi();
mcpBridge(pi);
await pi.handlers.session_start({ type: "session_start", reason: "startup" }, { cwd: dir });
after(() => pi.handlers.session_shutdown({ type: "session_shutdown", reason: "quit" }));

test("the config leaves the environment once read", () => {
  assert.equal(process.env.OMNIPLEX_PI_MCP, undefined);
});

test("registers each tool of a server that started, under its MCP name", () => {
  assert.deepEqual([...pi.tools.keys()].sort(), ["mcp__fake__boom", "mcp__fake__echo", "mcp__fake__wait"]);
  assert.equal(pi.tools.get("mcp__fake__wait").parameters.type, "object");
});

test("a call returns the server's content, run under the server's env", async () => {
  const res = await pi.tools.get("mcp__fake__echo").execute("c1", { text: "hi" }, new AbortController().signal);
  assert.deepEqual(res.content, [{ type: "text", text: "hi tok-123" }]);
});

test("an isError result throws with the server's text", async () => {
  await assert.rejects(pi.tools.get("mcp__fake__boom").execute("c2", {}, undefined), /it broke/);
});

test("aborting a call rejects it and cancels it at the server", async () => {
  const ac = new AbortController();
  const call = pi.tools.get("mcp__fake__wait").execute("c3", {}, ac.signal);
  setTimeout(() => ac.abort(), 50);
  await assert.rejects(call, /Cancelled/);
  for (let i = 0; i < 100 && !existsSync(cancelled); i++) await new Promise((r) => setTimeout(r, 20));
  assert.ok(Number.isInteger(JSON.parse(readFileSync(cancelled, "utf8"))));
});

test("the server's instructions join the system prompt", async () => {
  const out = await pi.handlers.before_agent_start({ systemPrompt: "BASE" });
  assert.match(out.systemPrompt, /^BASE\n\n/);
  assert.match(out.systemPrompt, /Use mcp__fake__echo to echo\./);
});

test("after shutdown a call fails instead of hanging", async () => {
  const other = fakePi();
  mcpBridge(other);
  await other.handlers.session_start({}, { cwd: dir });
  const ac = new AbortController();
  const call = other.tools.get("mcp__fake__wait").execute("c4", {}, ac.signal);
  await other.handlers.session_shutdown({});
  await assert.rejects(call, /fake/);
  await assert.rejects(other.tools.get("mcp__fake__echo").execute("c5", { text: "x" }), /not running/);
});
