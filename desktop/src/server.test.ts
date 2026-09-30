import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { LogFile } from "./log";
import { anyFreePort } from "./ports";
import { ServerSupervisor, type ServerStatus } from "./server";

// A stand-in for the Go binary: records how it was started, then behaves as
// FAKE_MODE says.
const FAKE = `#!/usr/bin/env node
const fs = require("node:fs");
const http = require("node:http");
const port = Number(process.argv[process.argv.indexOf("-port") + 1]);
fs.appendFileSync(process.env.FAKE_RECORD, JSON.stringify({ argv: process.argv.slice(2), pid: process.pid, desktop: process.env.OMNIPLEX_DESKTOP }) + "\\n");
const mode = process.env.FAKE_MODE;
if (mode === "crash") process.exit(3);
if (mode === "stubborn") process.on("SIGTERM", () => {});
else process.on("SIGTERM", () => { fs.appendFileSync(process.env.FAKE_RECORD, "SIGTERM\\n"); process.exit(0); });
http.createServer((req, res) => {
  if (mode === "mute") return;
  res.setHeader("content-type", "application/json");
  res.end(JSON.stringify({ ok: true, version: "test" }));
}).listen(port, "127.0.0.1");
`;

const fast = { maxRestarts: 2, baseDelayMs: 10, maxDelayMs: 20, stableAfterMs: 60000 };

describe.skipIf(process.platform === "win32")("ServerSupervisor", () => {
  let dir: string;
  let binary: string;
  let record: string;
  let supervisor: ServerSupervisor | undefined;

  beforeEach(() => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), "omniplex-desktop-test-"));
    binary = path.join(dir, "omniplex");
    record = path.join(dir, "record.jsonl");
    fs.writeFileSync(binary, FAKE, { mode: 0o755 });
  });

  afterEach(async () => {
    await supervisor?.stop();
    supervisor = undefined;
    fs.rmSync(dir, { recursive: true, force: true });
  });

  function launch(mode: string, extra: { healthTimeoutMs?: number; stopTimeoutMs?: number } = {}) {
    const statuses: ServerStatus[] = [];
    const waiters: Array<[(s: ServerStatus) => boolean, () => void]> = [];
    supervisor = new ServerSupervisor({
      binary,
      env: { ...process.env, FAKE_MODE: mode, FAKE_RECORD: record, OMNIPLEX_DESKTOP: "1" },
      log: new LogFile(dir, false),
      pickPort: () => anyFreePort(),
      onStatus: (s) => {
        statuses.push(s);
        for (const [match, done] of waiters) if (match(s)) done();
      },
      restart: fast,
      ...extra,
    });
    const until = (kind: ServerStatus["kind"]) =>
      new Promise<void>((resolve) => {
        if (statuses.some((s) => s.kind === kind)) return resolve();
        waiters.push([(s) => s.kind === kind, resolve]);
      });
    supervisor.start();
    return { statuses, until, sup: supervisor };
  }

  const starts = () =>
    fs
      .readFileSync(record, "utf8")
      .split("\n")
      .filter((l) => l.startsWith("{"))
      .map((l) => JSON.parse(l) as { argv: string[]; pid: number; desktop: string });

  it("starts the server privately on the chosen port and reports it once healthy", async () => {
    const { until, sup } = launch("ok");
    await until("running");
    const status = sup.current();
    expect(status.kind).toBe("running");
    const [first] = starts();
    expect(first.argv).toContain("-private");
    expect(first.argv).toContain("-exit-on-stdin-close");
    expect(first.argv[first.argv.indexOf("-port") + 1]).toBe(String((status as { port: number }).port));
    expect(first.desktop).toBe("1");
  });

  it("stops the server with SIGTERM", async () => {
    const { until, sup } = launch("ok");
    await until("running");
    await sup.stop();
    expect(fs.readFileSync(record, "utf8")).toContain("SIGTERM");
    expect(sup.current().kind).toBe("stopped");
  });

  it("kills a server that ignores SIGTERM", async () => {
    const { until, sup } = launch("stubborn", { stopTimeoutMs: 200 });
    await until("running");
    const { pid } = starts()[0];
    await sup.stop();
    expect(() => process.kill(pid, 0)).toThrow();
  });

  it("restarts a crashing server, then gives up", async () => {
    const { until, statuses } = launch("crash");
    await until("failed");
    expect(starts()).toHaveLength(1 + fast.maxRestarts);
    expect(statuses.at(-1)).toMatchObject({ kind: "failed" });
  });

  it("treats a server that never answers as failed", async () => {
    const { until } = launch("mute", { healthTimeoutMs: 300 });
    await until("failed");
    expect(starts()).toHaveLength(1 + fast.maxRestarts);
  });

  it("retry starts over after giving up", async () => {
    const { until, sup } = launch("crash");
    await until("failed");
    sup.retry();
    await new Promise((r) => setTimeout(r, 200));
    expect(starts()).toHaveLength(2 * (1 + fast.maxRestarts));
  });

  it("fails clearly when the binary is missing", async () => {
    fs.rmSync(binary);
    const { until, statuses } = launch("ok");
    await until("failed");
    expect(statuses.at(-1)).toMatchObject({ kind: "failed", message: expect.stringContaining(binary) });
  });
});
