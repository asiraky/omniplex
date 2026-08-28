// The Claude harness bridge.
//
// This is the only place in the project that talks to Anthropic's Agent SDK.
// It is deliberately dumb: it relays SDK messages to the host and relays the
// host's permission decisions back. It holds no canonical-event knowledge, so
// upgrading the SDK is `npm update` rather than a rewrite.
//
// Wire format: JSON-RPC 2.0, one object per line, over stdin/stdout.
//
//   host -> here (notifications):  prompt (text + image paths), interrupt
//   host -> here (request):        setModel, setEffort, setPermissionMode, supportedCommands, stopTask
//   here -> host (notifications):  message, models, fatal
//   here -> host (request):        permission  -> {behavior, updatedInput?, message?}

// Imported first, and deliberately so: it installs stdout discipline before
// any other module is evaluated. See guard.mjs.
import { writeFrame } from "./guard.mjs";

import { query } from "@anthropic-ai/claude-agent-sdk";
import { readFile } from "node:fs/promises";
import { createInterface } from "node:readline";

const send = writeFrame;
const notify = (method, params) => send({ jsonrpc: "2.0", method, params });
const respond = (id, result) => send({ jsonrpc: "2.0", id, result });
const respondError = (id, message) => send({ jsonrpc: "2.0", id, error: { code: -32000, message } });

// ---------------------------------------------------------------------------
// lifecycle
//
// The host owns this process. When the host's end of the pipe closes — which
// includes the host being SIGKILLed, where no handler of ours would run — our
// stdin reaches EOF and we exit, taking the harness process with us. Without
// this, killing the host orphans both this process and the several-hundred-MB
// harness it spawned.
// ---------------------------------------------------------------------------
let shuttingDown = false;
const die = (code) => {
  if (shuttingDown) return;
  shuttingDown = true;
  process.exit(code);
};
process.stdin.on("end", () => die(0));
process.stdin.on("close", () => die(0));
process.on("SIGTERM", () => die(0));
process.on("SIGINT", () => die(0));
process.on("uncaughtException", (err) => {
  notify("fatal", { message: `uncaught: ${err?.stack ?? String(err)}` });
  die(1);
});
process.on("unhandledRejection", (err) => {
  notify("fatal", { message: `unhandled rejection: ${err?.stack ?? String(err)}` });
  die(1);
});

// ---------------------------------------------------------------------------
// config, supplied by the host on argv as a single JSON blob
// ---------------------------------------------------------------------------
const config = JSON.parse(process.argv[2] ?? "{}");

// ---------------------------------------------------------------------------
// prompts: the host sends them one at a time; the SDK consumes an async
// iterable, so this is a queue with a waiter.
// ---------------------------------------------------------------------------
const queued = [];
const waiters = [];
const pushPrompt = (turn) => (waiters.length ? waiters.shift()(turn) : queued.push(turn));
const nextPrompt = () =>
  new Promise((resolve) => (queued.length ? resolve(queued.shift()) : waiters.push(resolve)));

// The host sends image paths, not bytes; the picture becomes a base64 content
// block here, at the last possible moment. An image that cannot be read becomes
// a note in its place rather than failing the turn: losing a screenshot is recoverable,
// losing the question that came with it is not.
async function imageBlocks(images) {
  const blocks = [];
  for (const image of images ?? []) {
    try {
      const data = await readFile(image.path);
      blocks.push({
        type: "image",
        source: { type: "base64", media_type: image.mediaType, data: data.toString("base64") },
      });
    } catch (e) {
      // Say so in the message rather than inventing an event: the model is
      // told an image was meant to be here, and the transcript the host
      // renders is untouched.
      blocks.push({ type: "text", text: `[an attached image could not be read: ${e?.message ?? e}]` });
    }
  }
  return blocks;
}

async function* prompts() {
  while (!shuttingDown) {
    const { text, images } = await nextPrompt();
    // Images lead: the model is being asked about them, and the question that
    // follows reads as a caption rather than a preamble. An image-only message
    // carries no empty text block, which the API rejects.
    const content = [...(await imageBlocks(images))];
    if (text || content.length === 0) content.push({ type: "text", text });
    yield {
      type: "user",
      message: { role: "user", content },
      parent_tool_use_id: null,
    };
  }
}

// ---------------------------------------------------------------------------
// permissions: each canUseTool call becomes an outbound request; the host's
// response resolves it. The SDK's AbortSignal fires if the turn is cancelled,
// so a request the host never answers cannot hang the process forever.
// ---------------------------------------------------------------------------
const pending = new Map();
let requestSeq = 0;

const canUseTool = (toolName, input, { signal, suggestions }) =>
  new Promise((resolve) => {
    const id = ++requestSeq;
    let settled = false;
    const settle = (value) => {
      if (settled) return;
      settled = true;
      pending.delete(id);
      resolve(value);
    };

    pending.set(id, settle);
    signal?.addEventListener?.("abort", () => settle({ behavior: "deny", message: "Cancelled" }), {
      once: true,
    });

    send({
      jsonrpc: "2.0",
      id,
      method: "permission",
      params: { toolName, input, suggestions: suggestions ?? [] },
    });
  });

// ---------------------------------------------------------------------------
// model listing
//
// A second, much smaller job this bridge does: ask the SDK which models the
// installed Claude Code offers, print them, and exit. It runs as its own
// short-lived process so the host never has to hold a session open to find
// out — and so a hung query cannot take a live session down with it.
// ---------------------------------------------------------------------------
if (config.op === "models") {
  const listing = query({
    // The SDK needs a prompt stream; this one never yields, because the
    // control request below is the only thing we want from the query.
    prompt: (async function* () {
      await new Promise(() => {});
    })(),
    options: {
      cwd: config.cwd || process.cwd(),
      ...(config.claudePath ? { pathToClaudeCodeExecutable: config.claudePath } : {}),
    },
  });
  // Exit only once the frame has actually left the pipe: process.exit would
  // otherwise truncate a buffered write and the host would see nothing.
  const finish = (code) => process.stdout.write("", () => die(code));
  try {
    notify("models", { models: await listing.supportedModels() });
    // Ending the generator asks the CLI to shut down. Not awaited: the answer
    // is already on the wire, and a slow teardown must not delay it.
    listing.return(undefined).catch(() => {});
    finish(0);
  } catch (err) {
    notify("fatal", { message: err?.stack ?? String(err) });
    finish(1);
  }
  // finish() only *schedules* the exit, for after the write drains. Without
  // this the module body would run on and start a second Claude Code — a
  // whole session process spawned by a listing, racing the exit that is about
  // to kill it.
  await new Promise(() => {});
}

// ---------------------------------------------------------------------------
// the session
// ---------------------------------------------------------------------------
const session = query({
  prompt: prompts(),
  options: {
    cwd: config.cwd,
    includePartialMessages: true,
    canUseTool,
    ...(config.model ? { model: config.model } : {}),
    ...(config.permissionMode ? { permissionMode: config.permissionMode } : {}),
    // The SDK's deliberate second opt-in for bypassPermissions. It permits the
    // mode (at start or via a later setPermissionMode) without enabling it;
    // the host's UI confirms with the human before ever selecting bypass.
    ...(config.allowDangerouslySkipPermissions ? { allowDangerouslySkipPermissions: true } : {}),
    ...(config.effort ? { effort: config.effort } : {}),
    // Mutually exclusive by SDK contract: sessionId names a new conversation,
    // resume continues an existing one.
    ...(config.resume ? { resume: config.resume } : {}),
    ...(config.sessionId && !config.resume ? { sessionId: config.sessionId } : {}),
    // Use the Claude Code the host resolved. We never ship one.
    ...(config.claudePath ? { pathToClaudeCodeExecutable: config.claudePath } : {}),
  },
});

// ---------------------------------------------------------------------------
// host -> here
// ---------------------------------------------------------------------------
createInterface({ input: process.stdin }).on("line", (line) => {
  if (!line.trim()) return;

  let frame;
  try {
    frame = JSON.parse(line);
  } catch {
    notify("fatal", { message: "host sent an unparseable frame" });
    return;
  }

  // A response to one of our permission requests.
  if (frame.id !== undefined && frame.method === undefined) {
    const settle = pending.get(frame.id);
    if (!settle) return; // already aborted or answered; not an error
    if (frame.error) {
      settle({ behavior: "deny", message: frame.error.message ?? "Denied" });
      return;
    }
    settle(frame.result);
    return;
  }

  switch (frame.method) {
    case "prompt":
      pushPrompt({ text: frame.params.text ?? "", images: frame.params.images ?? [] });
      break;
    case "interrupt":
      // Interrupt is a request: writing it to the SDK is not the same as the
      // SDK accepting it. The host closes its canonical turn from this reply,
      // because an interrupted streaming query does not reliably yield a
      // terminal result message of its own.
      session
        .interrupt()
        .then(() => {
          if (frame.id !== undefined) respond(frame.id, {});
        })
        .catch((e) => {
          if (frame.id !== undefined) respondError(frame.id, e?.message ?? String(e));
          else notify("fatal", { message: `interrupt failed: ${e?.message ?? e}` });
        });
      break;
    case "setModel":
      // A request, not a notification: a model the harness will not accept
      // must reach the human as an error, not be silently recorded as the
      // chosen one while the session keeps running the old model.
      session
        .setModel(frame.params.model || undefined)
        .then(() => {
          if (frame.id !== undefined) respond(frame.id, {});
        })
        .catch((e) => {
          if (frame.id !== undefined) respondError(frame.id, e?.message ?? String(e));
        });
      break;
    case "setEffort":
      // A request, like setModel: mid-session reasoning-effort change via
      // applyFlagSettings (effortLevel additionally accepts the session-scoped
      // "max" here). A refused level must reach the human as an error.
      session
        .applyFlagSettings({ effortLevel: frame.params.effort || undefined })
        .then(() => {
          if (frame.id !== undefined) respond(frame.id, {});
        })
        .catch((e) => {
          if (frame.id !== undefined) respondError(frame.id, e?.message ?? String(e));
        });
      break;
    case "setPermissionMode":
      // A request, not a notification: a refused mode (managed settings can
      // disable bypass and auto) must reach the human as an error, not vanish.
      session
        .setPermissionMode(frame.params.mode)
        .then(() => {
          if (frame.id !== undefined) respond(frame.id, {});
        })
        .catch((e) => {
          if (frame.id !== undefined) respondError(frame.id, e?.message ?? String(e));
        });
      break;
    case "stopTask":
      // A request, like setModel: a task id the harness does not know must
      // reach the human as an error. The outcome itself arrives as a
      // task_notification on the message stream, not in this reply.
      session
        .stopTask(frame.params.taskId)
        .then(() => {
          if (frame.id !== undefined) respond(frame.id, {});
        })
        .catch((e) => {
          if (frame.id !== undefined) respondError(frame.id, e?.message ?? String(e));
        });
      break;
    case "supportedCommands":
      session
        .supportedCommands()
        .then((commands) => {
          if (frame.id !== undefined) respond(frame.id, { commands });
        })
        .catch((e) => {
          if (frame.id !== undefined) respondError(frame.id, e?.message ?? String(e));
        });
      break;
    default:
      if (frame.id !== undefined) respondError(frame.id, `unknown method: ${frame.method}`);
  }
});

// ---------------------------------------------------------------------------
// here -> host: relay every SDK message verbatim. Mapping to canonical events
// happens in Go, so this file stays stable across protocol changes.
// ---------------------------------------------------------------------------
// The SDK's own occupancy accounting. The per-turn `usage` on a result message
// is a cost-accounting total summed across every request in the turn, so using
// it as a "how full is the window" gauge overcounts by roughly O(N²) in the
// number of tool calls. getContextUsage() is the harness's actual answer:
// tokens in use now, the window they are measured against, and the category
// breakdown. We ask for it once the turn settles and relay it as a synthetic
// message the host maps like any other. Errors are swallowed — an older CLI
// without the control method simply leaves the host on its fallback estimate.
const reportContextUsage = () => {
  if (typeof session.getContextUsage !== "function") return;
  Promise.resolve()
    .then(() => session.getContextUsage())
    .then((usage) => notify("message", { message: { type: "context_usage", usage } }))
    .catch(() => {});
};

try {
  for await (const message of session) {
    notify("message", { message });
    if (message?.type === "result") reportContextUsage();
  }
  notify("fatal", { message: "session ended" });
  die(0);
} catch (err) {
  notify("fatal", { message: err?.stack ?? String(err) });
  die(1);
}
