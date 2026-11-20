// MCP bridge: a Pi extension that gives a Pi session the stdio MCP servers
// Omniplex hands it, as native Pi tools.
//
// Pi has no MCP client of its own, and the community ones give up on a tool
// call after 60 s, which Omniplex's tools outlast by design: a write waits on
// a person tapping a card for up to 25 minutes. So this extension speaks MCP
// itself (newline-delimited JSON-RPC over the server's stdio) and never times
// a call out. A call ends when the server answers or the turn is stopped; a
// stop sends notifications/cancelled for it.
//
// Config arrives in OMNIPLEX_PI_MCP, a JSON array of {name, command, args,
// env}. Env values can be credentials, so the variable is read once and then
// removed from this process's environment: Pi's shell tool copies
// process.env when it runs a command, and the agent must not find the token
// there.
//
// Each server's tools are registered as mcp__<server>__<tool>, the names
// Claude Code gives them, which are also the names Omniplex's own server
// instructions use. Each server's instructions are appended to the system
// prompt.
//
// Servers start on session_start, which Pi awaits before it reads its first
// RPC command, so the tools exist before the first prompt. They stop on
// session_shutdown and, failing that, when this process exits.

import { spawn } from "node:child_process";

const CONFIG_ENV = "OMNIPLEX_PI_MCP";
// Session replacement (fork, new session) builds a fresh extension instance
// after the variable is gone, so the parsed config is kept on globalThis.
const CONFIG_KEY = Symbol.for("omniplex.pi.mcpConfig");
const CHILDREN_KEY = Symbol.for("omniplex.pi.mcpChildren");
const PROTOCOL_VERSION = "2025-06-18";
// Starting a server and listing its tools is quick; a server that has not
// answered in this long is not going to, and Pi's startup waits on it.
const HANDSHAKE_MS = 30_000;
// What providers accept as a function name.
const NAME_MAX = 64;
const STDERR_TAIL = 2048;

function log(message) {
  process.stderr.write(`[omniplex mcp bridge] ${message}\n`);
}

function takeConfig() {
  if (!(CONFIG_KEY in globalThis)) {
    const raw = process.env[CONFIG_ENV];
    delete process.env[CONFIG_ENV];
    let servers = [];
    if (raw) {
      try {
        servers = JSON.parse(raw);
      } catch (err) {
        log(`${CONFIG_ENV} is not JSON: ${err.message}`);
      }
    }
    globalThis[CONFIG_KEY] = Array.isArray(servers) ? servers.filter((s) => s && s.name && s.command) : [];
  }
  return globalThis[CONFIG_KEY];
}

// Every live server process, so an exit that skips session_shutdown still
// takes them down.
function children() {
  if (!globalThis[CHILDREN_KEY]) {
    const set = new Set();
    globalThis[CHILDREN_KEY] = set;
    process.once("exit", () => {
      for (const child of set) child.kill("SIGTERM");
    });
  }
  return globalThis[CHILDREN_KEY];
}

const safe = (s) => String(s).replace(/[^A-Za-z0-9_-]/g, "_");

export function toolName(server, tool) {
  return `mcp__${safe(server)}__${safe(tool)}`;
}

class Client {
  constructor(server, cwd) {
    this.name = server.name;
    this.nextId = 1;
    this.pending = new Map();
    this.buf = "";
    this.stderr = "";
    this.dead = null;

    const env = { ...process.env, ...(server.env || {}) };
    this.child = spawn(server.command, server.args || [], { cwd, env, stdio: ["pipe", "pipe", "pipe"] });
    children().add(this.child);

    this.child.stdin.on("error", () => {});
    this.child.stdout.setEncoding("utf8");
    this.child.stdout.on("data", (chunk) => this.onData(chunk));
    this.child.stderr.setEncoding("utf8");
    this.child.stderr.on("data", (chunk) => {
      this.stderr = (this.stderr + chunk).slice(-STDERR_TAIL);
      for (const line of chunk.split("\n")) if (line.trim()) log(`${this.name}: ${line}`);
    });
    this.child.on("error", (err) => this.fail(`could not start: ${err.message}`));
    this.child.on("exit", (code, signal) => {
      children().delete(this.child);
      const tail = this.stderr.trim();
      this.fail(`exited (${signal || code})${tail ? `: ${tail}` : ""}`);
    });
  }

  fail(why) {
    if (this.dead) return;
    this.dead = `MCP server ${this.name} ${why}`;
    for (const p of this.pending.values()) p.reject(new Error(this.dead));
    this.pending.clear();
  }

  onData(chunk) {
    this.buf += chunk;
    let nl;
    while ((nl = this.buf.indexOf("\n")) >= 0) {
      const line = this.buf.slice(0, nl).trim();
      this.buf = this.buf.slice(nl + 1);
      if (!line) continue;
      let msg;
      try {
        msg = JSON.parse(line);
      } catch {
        continue;
      }
      this.onMessage(msg);
    }
  }

  onMessage(msg) {
    if (msg.method !== undefined) {
      // A request from the server. Only ping is worth answering.
      if (msg.id !== undefined) {
        if (msg.method === "ping") this.send({ jsonrpc: "2.0", id: msg.id, result: {} });
        else this.send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: `method not found: ${msg.method}` } });
      }
      return;
    }
    const p = this.pending.get(msg.id);
    if (!p) return;
    this.pending.delete(msg.id);
    if (msg.error) p.reject(new Error(msg.error.message || `MCP error ${msg.error.code}`));
    else p.resolve(msg.result ?? {});
  }

  send(msg) {
    if (this.dead) return;
    this.child.stdin.write(JSON.stringify(msg) + "\n");
  }

  // request sends one call and waits for its answer: forever, unless
  // timeoutMs is set or signal aborts it. An abort cancels the call at the
  // server, which then sends no reply.
  request(method, params, { signal, timeoutMs } = {}) {
    if (this.dead) return Promise.reject(new Error(this.dead));
    if (signal?.aborted) return Promise.reject(new Error("Cancelled"));
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      let timer;
      const done = () => {
        clearTimeout(timer);
        signal?.removeEventListener("abort", onAbort);
      };
      const onAbort = () => {
        if (!this.pending.delete(id)) return;
        done();
        this.send({ jsonrpc: "2.0", method: "notifications/cancelled", params: { requestId: id, reason: "Stopped by the user" } });
        reject(new Error("Cancelled"));
      };
      this.pending.set(id, {
        resolve: (v) => (done(), resolve(v)),
        reject: (e) => (done(), reject(e)),
      });
      signal?.addEventListener("abort", onAbort, { once: true });
      if (timeoutMs) {
        timer = setTimeout(() => {
          if (!this.pending.delete(id)) return;
          done();
          reject(new Error(`MCP server ${this.name} did not answer ${method} within ${timeoutMs / 1000}s`));
        }, timeoutMs);
      }
      this.send({ jsonrpc: "2.0", id, method, params });
    });
  }

  async handshake() {
    const init = await this.request(
      "initialize",
      {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: { name: "omniplex-pi-bridge", version: "1" },
      },
      { timeoutMs: HANDSHAKE_MS },
    );
    this.send({ jsonrpc: "2.0", method: "notifications/initialized" });
    const tools = [];
    let cursor;
    do {
      const page = await this.request("tools/list", cursor ? { cursor } : {}, { timeoutMs: HANDSHAKE_MS });
      tools.push(...(page.tools || []));
      cursor = page.nextCursor;
    } while (cursor);
    return { instructions: typeof init.instructions === "string" ? init.instructions.trim() : "", tools };
  }

  close() {
    if (!this.dead) this.fail("was stopped");
    this.child.stdin.end();
    this.child.kill("SIGTERM");
  }
}

// toResult maps an MCP tools/call result onto a Pi tool result. Pi marks a
// result failed only when execute throws, so an isError result throws.
function toResult(result, server, tool) {
  const content = [];
  for (const item of result.content || []) {
    switch (item.type) {
      case "text":
        content.push({ type: "text", text: item.text ?? "" });
        break;
      case "image":
        content.push({ type: "image", data: item.data, mimeType: item.mimeType });
        break;
      case "resource":
        content.push({ type: "text", text: item.resource?.text ?? `[resource ${item.resource?.uri ?? ""}]` });
        break;
      case "resource_link":
        content.push({ type: "text", text: `[${item.name || item.uri}](${item.uri})` });
        break;
      default:
        content.push({ type: "text", text: JSON.stringify(item) });
    }
  }
  if (content.length === 0 && result.structuredContent !== undefined) {
    content.push({ type: "text", text: JSON.stringify(result.structuredContent) });
  }
  if (result.isError) {
    const text = content
      .filter((c) => c.type === "text")
      .map((c) => c.text)
      .join("\n");
    throw new Error(text || `${server}/${tool} failed`);
  }
  if (content.length === 0) content.push({ type: "text", text: "(no output)" });
  return { content, details: { server, tool } };
}

function parametersOf(tool) {
  const schema = tool.inputSchema && typeof tool.inputSchema === "object" ? { ...tool.inputSchema } : {};
  if (!schema.type) schema.type = "object";
  if (schema.type === "object" && !schema.properties) schema.properties = {};
  return schema;
}

export default function mcpBridge(pi) {
  const servers = takeConfig();
  if (servers.length === 0) return;

  // live maps a server's name to its running client. Tools are registered
  // once per instance and look their client up per call, so a restart after
  // a session_shutdown keeps the same tools.
  const live = new Map();
  const registered = new Set();
  let instructions = "";
  let starting = null;

  const start = async (cwd) => {
    const parts = [];
    const results = await Promise.allSettled(
      servers.map(async (server) => {
        const client = new Client(server, cwd);
        try {
          const got = await client.handshake();
          return { server, client, ...got };
        } catch (err) {
          client.close();
          throw new Error(`${server.name}: ${err.message}`);
        }
      }),
    );
    for (const r of results) {
      if (r.status === "rejected") {
        log(`skipped a server: ${r.reason.message}`);
        continue;
      }
      const { server, client, tools } = r.value;
      live.set(server.name, client);
      if (r.value.instructions) parts.push(`## ${server.name}\n\n${r.value.instructions}`);
      for (const tool of tools) {
        const name = toolName(server.name, tool.name);
        if (name.length > NAME_MAX) {
          log(`skipped ${server.name}/${tool.name}: ${name} is longer than ${NAME_MAX} characters`);
          continue;
        }
        if (registered.has(name)) continue;
        registered.add(name);
        pi.registerTool({
          name,
          label: tool.title || tool.annotations?.title || `${server.name}/${tool.name}`,
          description: tool.description || `${tool.name} from the ${server.name} MCP server.`,
          parameters: parametersOf(tool),
          async execute(_toolCallId, params, signal) {
            const c = live.get(server.name);
            if (!c) throw new Error(`MCP server ${server.name} is not running`);
            const result = await c.request("tools/call", { name: tool.name, arguments: params ?? {} }, { signal });
            return toResult(result, server.name, tool.name);
          },
        });
      }
    }
    instructions = parts.length ? `# MCP Server Instructions\n\n${parts.join("\n\n")}` : "";
  };

  pi.on("session_start", async (_event, ctx) => {
    starting ??= start(ctx?.cwd || process.cwd());
    await starting;
  });

  pi.on("session_shutdown", async () => {
    for (const c of live.values()) c.close();
    live.clear();
    starting = null;
  });

  pi.on("before_agent_start", async (event) => {
    if (!instructions) return;
    return { systemPrompt: `${event.systemPrompt}\n\n${instructions}` };
  });
}
