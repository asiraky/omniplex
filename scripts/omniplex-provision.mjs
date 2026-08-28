#!/usr/bin/env node
/**
 * Provision hook for omniplex's own repository.
 *
 * omniplex runs its dev server out of a checkout. An agent editing that same
 * checkout restarts the server underneath itself, so work on omniplex happens in a
 * worktree — and a worktree is only useful if the app can actually run there.
 * That needs three things this hook provides: dependencies, a port pair
 * nothing else has, and a database of its own.
 *
 * The contract (see internal/session/lifecycle.go):
 *   - cwd is the project root, and OMNIPLEX_CONTEXT_FILE holds the request
 *   - stdout and stderr stream into the session transcript as they arrive
 *   - OMNIPLEX_RESULT_FILE must come back holding {cwd, branch, resources}
 *
 * Naming matters here: omniplex provisions the worktree itself only for hooks called
 * `worktree-setup` or `.claude/worktree/setup`, which exist to support scripts
 * written before this contract did. This hook is not one of those, so it owns
 * worktree creation outright — which is the honest arrangement, since it is
 * also the thing that decides the worktree is fit to run in.
 *
 * Idempotent: a retried provision reuses the worktree and rewrites the
 * generated files.
 */

import { execFileSync } from "node:child_process";
import { createServer } from "node:net";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, isAbsolute, join } from "node:path";
import { DatabaseSync } from "node:sqlite";

// Offset from the defaults in scripts/dev, so a worktree's ports are visibly
// not the main checkout's when both are running.
const FIRST_SERVER_PORT = 8800;
const FIRST_VITE_PORT = 5300;

const contextFile = process.env.OMNIPLEX_CONTEXT_FILE;
const resultFile = process.env.OMNIPLEX_RESULT_FILE;
if (!contextFile || !resultFile) {
  console.error("omniplex-provision must be run by omniplex: OMNIPLEX_CONTEXT_FILE and OMNIPLEX_RESULT_FILE are unset");
  process.exit(2);
}

const ctx = JSON.parse(readFileSync(contextFile, "utf8"));
const projectRoot = ctx.projectRoot ?? process.cwd();
const branch = ctx.requestedBranch;
if (!branch) {
  console.error("no branch was requested, so there is nothing to create");
  process.exit(2);
}
const worktree = isAbsolute(ctx.suggestedWorktreePath ?? "")
  ? ctx.suggestedWorktreePath
  : join(projectRoot, ctx.suggestedWorktreePath ?? join(".worktrees", branch.replaceAll("/", "-")));

const git = (...args) => execFileSync("git", args, { cwd: projectRoot, encoding: "utf8" }).trim();
const step = (message) => console.log(`→ ${message}`);

// ---- 1. the worktree ----

if (existsSync(worktree)) {
  step(`reusing ${worktree}`);
} else {
  let exists = true;
  try {
    git("show-ref", "--verify", "--quiet", `refs/heads/${branch}`);
  } catch {
    exists = false;
  }
  // A branch that already exists is checked out as it stands; only a new one
  // gets a base, so a retry cannot silently reset someone's work to main.
  const args = exists
    ? ["worktree", "add", worktree, branch]
    : ["worktree", "add", worktree, "-b", branch, ctx.baseRef || "HEAD"];
  step(`git ${args.join(" ")}`);
  console.log(git(...args));
}

// ---- 2. dependencies ----

// node_modules is not tracked, so a fresh worktree has none and every web
// build fails there. Go needs nothing: its module cache is machine-wide.
step("npm install");
execFileSync("npm", ["install", "--no-audit", "--no-fund"], { cwd: worktree, stdio: "inherit" });

// ---- 3. a port pair and a database of its own ----

const freePort = async (from, span = 200) => {
  for (let port = from; port < from + span; port += 1) {
    const free = await new Promise(done => {
      const probe = createServer();
      probe.once("error", () => done(false));
      probe.once("listening", () => probe.close(() => done(true)));
      probe.listen(port, "127.0.0.1");
    });
    if (free) return port;
  }
  throw new Error(`no free port between ${from} and ${from + span}`);
};

const serverPort = await freePort(FIRST_SERVER_PORT);
const vitePort = await freePort(FIRST_VITE_PORT);
step(`ports: server ${serverPort}, vite ${vitePort}`);

// A fresh database rather than a copy of the main one. omniplex's store applies its
// whole schema on open, so an empty file is fully migrated; and the log holds
// live sessions, so a copy would offer this worktree's own session for resume
// and end up with two harnesses writing two divergent copies of one log.
// Reference data is seeded into it below.
const db = join(worktree, ".omniplex", "dev.db");
mkdirSync(join(worktree, ".omniplex"), { recursive: true });


writeFileSync(
  join(worktree, ".omniplex", "worktree.env"),
  [
    "# Written by scripts/omniplex-provision.mjs and read by scripts/dev.",
    "# Keeps this worktree's server off the ports and database of the checkout",
    "# it came from, so both can run at once.",
    `OMNIPLEX_PORT=${serverPort}`,
    `OMNIPLEX_VITE_PORT=${vitePort}`,
    `OMNIPLEX_DB=${db}`,
    "",
  ].join("\n"),
);

writeFileSync(
  join(worktree, "WORKTREE.md"),
  `# ${basename(worktree)}

This is a worktree of \`${projectRoot}\`, on branch \`${branch}\`.

Run the app with \`npm run dev\` as usual. It picks up \`.omniplex/worktree.env\` and
starts on **http://127.0.0.1:${serverPort}** with its own database at
\`.omniplex/dev.db\` — so it will not collide with the checkout this came from, and
nothing you do here touches its sessions. That database is seeded with the
projects and labels of the one it came from; sessions are not copied.

Both files are generated and ignored by Git. Delete the worktree with the
session; do not \`git worktree remove\` it by hand while a session holds it.
`,
);

// ---- 4. reference data ----

/**
 * Copies the projects and labels of the database this server runs on into the
 * worktree's own, so the app there opens on something usable instead of an
 * empty sidebar — the projects you can start a session in, and the labels the
 * list is organised by.
 *
 * Only those two tables. Sessions, events and snapshots are deliberately left
 * behind: a copied session is a live session whose harness process belongs to
 * the other server, and resuming it from here would put two harnesses on one
 * transcript. Devices are left behind too — a pairing token belongs to the
 * origin it was issued for, and this worktree is a different port.
 *
 * The DDL is read out of the source database rather than written here, so this
 * cannot drift from internal/store/store.go. Best-effort throughout: a
 * worktree with an empty sidebar is a nuisance, a failed provision is not.
 */
const seedReferenceData = (source, target) => {
  if (!existsSync(source)) {
    step(`no database at ${source}; the worktree starts empty`);
    return;
  }
  let dst;
  try {
    dst = new DatabaseSync(target);
    dst.exec("PRAGMA busy_timeout=5000");
    // The source is attached, not opened: it is very likely the live server's
    // database, and this only ever reads from it.
    dst.exec(`ATTACH DATABASE '${source.replaceAll("'", "''")}' AS src`);
    const tables = dst
      .prepare(
        "SELECT name, sql FROM src.sqlite_master WHERE type = 'table' AND name IN ('projects', 'labels')",
      )
      .all();
    for (const { name, sql } of tables) {
      dst.exec(
        sql.replace(
          /^\s*CREATE TABLE\s+(IF NOT EXISTS\s+)?["`]?(\w+)["`]?/i,
          (_, _ifNotExists, table) => `CREATE TABLE IF NOT EXISTS main.${table}`,
        ),
      );
      const { changes } = dst.prepare(`INSERT INTO main.${name} SELECT * FROM src.${name}`).run();
      step(`seeded ${changes} ${name} from ${source}`);
    }
    dst.exec("DETACH DATABASE src");
  } catch (err) {
    step(`could not seed from ${source}: ${err.message}`);
  } finally {
    dst?.close();
  }
};

// Whatever database the server running this hook is on — its own, in a
// worktree; the default one under $HOME when nothing set it.
seedReferenceData(process.env.OMNIPLEX_DB || join(homedir(), ".omniplex", "omniplex.db"), db);

writeFileSync(
  resultFile,
  JSON.stringify({
    cwd: worktree,
    branch,
    resources: { url: `http://127.0.0.1:${serverPort}`, port: serverPort, vitePort, database: db },
  }),
);
step("ready");
