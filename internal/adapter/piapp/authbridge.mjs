// Auth bridge between the Go pi adapter and Pi's own ModelRuntime.
//
// Pi has no headless login CLI: its sign-in flows (API keys, OAuth, device
// codes) live in the ModelRuntime class the TUI drives directly. This script
// loads that runtime from the user's own Pi install — never a copy we ship —
// and narrates one auth operation over NDJSON so the Go side can relay it to
// whichever UI asked. Credentials stay entirely in Pi's storage; the only
// secret that passes through here is a prompt answer on its way in.
//
// Usage: node authbridge.mjs <pkgRoot> <cmd> [provider] [authType]
//   cmd: methods | status | login | logout
//
// stdout (one JSON object per line):
//   {"type":"notify","event":{...}}          display-only auth event
//   {"type":"prompt","id":N,"prompt":{...}}  question; blocks until answered
//   {"type":"result","data":{...}}           success, then exit 0
//   {"type":"error","message":"..."}         failure, then exit 1
// stdin (one JSON object per line):
//   {"type":"answer","id":N,"value":"..."}
//   {"type":"cancel","id":N}
//
// Line framing note: both sides emit JSON through serializers that escape
// U+2028/U+2029 (ES2019 JSON.stringify, Go encoding/json), so splitting on
// LF alone is sound here even via readline.

import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { createInterface } from "node:readline";

const [, , pkgRoot, cmd, provider, authType] = process.argv;

function emit(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

function fail(message) {
  emit({ type: "error", message: String(message) });
  process.exit(1);
}

if (!pkgRoot || !cmd) {
  fail("usage: authbridge.mjs <pkgRoot> <cmd> [provider] [authType]");
}

// Answers to in-flight prompts, keyed by the id we handed out.
let nextPromptId = 0;
const pending = new Map();

const rl = createInterface({ input: process.stdin, terminal: false });
rl.on("line", (line) => {
  line = line.trim();
  if (line === "") return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch {
    return;
  }
  const waiter = pending.get(msg.id);
  if (!waiter) return;
  pending.delete(msg.id);
  if (msg.type === "answer") waiter.resolve(String(msg.value ?? ""));
  else waiter.reject(new Error("cancelled"));
});
// The Go side closing stdin is abandonment: reject anything still waiting so
// the flow unwinds instead of hanging on a prompt no one will answer.
rl.on("close", () => {
  for (const waiter of pending.values()) waiter.reject(new Error("cancelled"));
  pending.clear();
});

// The interaction Pi's login flows call back into. Prompts round-trip through
// the Go side; notify events are display-only and stream one way.
const interaction = {
  notify(event) {
    emit({ type: "notify", event });
  },
  prompt(prompt) {
    const id = ++nextPromptId;
    // The prompt may carry an AbortSignal that never crosses a process
    // boundary; strip it and everything else non-serializable.
    const { signal, ...wire } = prompt;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      emit({ type: "prompt", id, prompt: wire });
      if (signal) {
        signal.addEventListener("abort", () => {
          if (pending.delete(id)) reject(new Error("aborted"));
        });
      }
    });
  },
};

try {
  const mod = await import(pathToFileURL(join(pkgRoot, "dist", "index.js")).href);
  const { ModelRuntime } = mod;
  // No explicit paths: the runtime resolves auth.json and models.json from
  // PI_CODING_AGENT_DIR (or ~/.pi/agent), which the Go side sets through the
  // instance's env overlay — the same isolation every other pi spawn gets.
  const runtime = await ModelRuntime.create({});

  switch (cmd) {
    case "methods": {
      const methods = [];
      for (const p of runtime.getProviders()) {
        // An apiKey block without login() is ambient-only (env vars, AWS
        // profiles): there is nothing interactive to offer for it.
        if (p.auth?.apiKey?.login) {
          methods.push({
            provider: p.id,
            type: "api_key",
            label: p.auth.apiKey.name || `${p.name} API key`,
          });
        }
        if (p.auth?.oauth) {
          methods.push({
            provider: p.id,
            type: "oauth",
            label: p.auth.oauth.name || p.name,
            loginLabel: p.auth.oauth.loginLabel || "",
            subscription: !!p.auth.oauth.isSubscription,
          });
        }
      }
      emit({ type: "result", data: { methods } });
      break;
    }

    case "status": {
      // listCredentials is metadata only — provider id and credential type,
      // never key material.
      const stored = await runtime.listCredentials();
      const statuses = [];
      for (const p of runtime.getProviders()) {
        let check;
        try {
          check = await runtime.checkAuth(p.id);
        } catch {
          check = undefined;
        }
        statuses.push({
          provider: p.id,
          connected: !!check,
          type: check?.type || "",
          source: check?.source || "",
          stored: stored.some((c) => c.providerId === p.id),
        });
      }
      emit({ type: "result", data: { statuses } });
      break;
    }

    case "login": {
      if (!provider || !authType) fail("login needs a provider and auth type");
      await runtime.login(provider, authType, interaction);
      emit({ type: "result", data: {} });
      break;
    }

    case "logout": {
      if (!provider) fail("logout needs a provider");
      await runtime.logout(provider);
      emit({ type: "result", data: {} });
      break;
    }

    default:
      fail(`unknown command: ${cmd}`);
  }
  process.exit(0);
} catch (err) {
  fail(err && err.message ? err.message : err);
}
