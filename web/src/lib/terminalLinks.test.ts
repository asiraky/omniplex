import { describe, expect, it } from "vitest";

import { findTerminalLinks } from "./terminalLinks";

/** The single path link expected for `text` sitting at its first occurrence in `line`. */
function pathAt(line: string, text: string, path: string, lineNo?: number) {
  const start = line.indexOf(text);
  return { kind: "path", start, end: start + text.length, path, ...(lineNo === undefined ? {} : { line: lineNo }) };
}

describe("findTerminalLinks", () => {
  it("finds a URL and trims the punctuation that ends a sentence", () => {
    expect(findTerminalLinks("see https://example.com/docs.")).toEqual([
      { kind: "url", start: 4, end: 28, url: "https://example.com/docs" },
    ]);
    expect(findTerminalLinks("ok? HTTP://Example.com/a!?")).toEqual([
      { kind: "url", start: 4, end: 24, url: "HTTP://Example.com/a" },
    ]);
  });

  it("drops a closing bracket a URL did not open, and keeps one it did", () => {
    expect(findTerminalLinks("(https://example.com/docs), next")).toEqual([
      { kind: "url", start: 1, end: 25, url: "https://example.com/docs" },
    ]);
    const wiki = "https://en.wikipedia.org/wiki/Foo_(bar)";
    expect(findTerminalLinks(`[${wiki}]`)).toEqual([{ kind: "url", start: 1, end: 1 + wiki.length, url: wiki }]);
  });

  it("keeps a URL's query and fragment, and finds no path inside it", () => {
    const url = "http://localhost:5173/src/App.tsx?x=1#top";
    expect(findTerminalLinks(`  ➜  Local:   ${url}`)).toEqual([
      { kind: "url", start: 14, end: 14 + url.length, url },
    ]);
  });

  it.each([
    ["absolute", "/home/dev/app/src/main.ts"],
    ["dot-relative", "./scripts/build"],
    ["parent-relative", "../other/README.md"],
    ["bare with an extension", "web/src/App.tsx"],
    ["bare with a trailing slash", "web/"],
    ["with brackets and parens", "src/routes/(app)/[id]/+page.svelte"],
    ["through a scoped package", "node_modules/.pnpm/react@18.3.1/node_modules/react/index.js"],
    ["unicode", "src/żółć.ts"],
  ])("finds a %s path", (_, text) => {
    const line = `see ${text} for details`;
    expect(findTerminalLinks(line)).toEqual([pathAt(line, text, text)]);
  });

  it("gives a ~/ path back as written for the caller to decide about", () => {
    const line = "wrote ~/notes/today.md";
    expect(findTerminalLinks(line)).toEqual([pathAt(line, "~/notes/today.md", "~/notes/today.md")]);
  });

  it("splits a :line or :line:col anchor off the path but keeps it in the range", () => {
    for (const [line, text, path] of [
      ["src/a.ts:10:5: error TS2304", "src/a.ts:10:5", "src/a.ts"],
      ["/home/dev/app/main.c:10:5: error: expected ';'", "/home/dev/app/main.c:10:5", "/home/dev/app/main.c"],
      ["./main.go:7: undefined: x", "./main.go:7", "./main.go"],
      ["at ./src/main.ts:12.", "./src/main.ts:12", "./src/main.ts"],
    ] as const) {
      const lineNo = Number(text.split(":")[1]);
      expect(findTerminalLinks(line)).toEqual([pathAt(line, text, path, lineNo)]);
    }
  });

  it("drops punctuation and an unopened bracket after a prefixed path", () => {
    for (const [line, text] of [
      ["wrote ./out/report.txt:", "./out/report.txt"],
      ["(see /etc/hosts), then", "/etc/hosts"],
      ["(see /etc/hosts.)", "/etc/hosts"],
      ['File "/usr/lib/python3/x.py", line 3', "/usr/lib/python3/x.py"],
    ] as const) {
      expect(findTerminalLinks(line)).toEqual([pathAt(line, text, text)]);
    }
    // A bracket the path opened is part of it.
    expect(findTerminalLinks("/a/(b)")).toEqual([pathAt("/a/(b)", "/a/(b)", "/a/(b)")]);
  });

  it("keeps a question mark inside a filesystem path", () => {
    expect(findTerminalLinks("wrote /tmp/report?draft")).toEqual([
      pathAt("wrote /tmp/report?draft", "/tmp/report?draft", "/tmp/report?draft"),
    ]);
  });

  it("links a bare filename only as name.ext:line", () => {
    const line = "main.go:12: missing return";
    expect(findTerminalLinks(line)).toEqual([pathAt(line, "main.go:12", "main.go", 12)]);
    expect(findTerminalLinks("open App.tsx and package.json, e.g. foo.bar")).toEqual([]);
    expect(findTerminalLinks("listening on localhost:3000 and 127.0.0.1:8080")).toEqual([]);
    expect(findTerminalLinks("Connecting to api.github.com:443")).toEqual([]);
  });

  it("does not link a slashed word with nothing else saying it is a file", () => {
    expect(
      findTerminalLinks("Your branch is up to date with 'origin/main'. 12 KB/s, read/write"),
    ).toEqual([]);
  });

  it("does not link counters, dates or comment slashes", () => {
    expect(findTerminalLinks("[3/10] tests 1/2/2024 // done")).toEqual([]);
    expect(findTerminalLinks("ratio 1/2:3")).toEqual([]);
  });

  it.each([
    "connected to redis://127.0.0.1:6379/0",
    "uploaded s3://bucket/reports/today.csv",
    "    at main (file:///Users/jane/app/src/index.mjs:3:7)",
    "+postgres://localhost:5432/app",
    "git clone git@github.com:acme/widgets.git",
    "remote: git@gitlab.com:group/sub/project.git",
  ])("finds no path in a non-web URL or scp remote: %s", (line) => {
    expect(findTerminalLinks(line)).toEqual([]);
  });

  it("keeps a path beside a non-web URL, and one with an @ in it", () => {
    const line = "src/db.ts:4 redis://localhost:6379/0 src/contact@support.vue:12:3";
    expect(findTerminalLinks(line)).toEqual([
      pathAt(line, "src/db.ts:4", "src/db.ts", 4),
      pathAt(line, "src/contact@support.vue:12:3", "src/contact@support.vue", 12),
    ]);
  });

  it.each([
    " GET /api/trpc/post.list?batch=1&input=%7B%7D 200 in 35ms",
    'Started GET "/posts?page=2" for ::1',
    "proxying api/trpc/post.list?batch=1 to the backend",
  ])("finds no path in a logged request route: %s", (line) => {
    expect(findTerminalLinks(line)).toEqual([]);
  });

  it("returns several links sorted and apart", () => {
    const line = "src/server.ts:12 GET /api/users?id=42 200 /app/bin/server:3:7 https://example.com/x";
    expect(findTerminalLinks(line)).toEqual([
      pathAt(line, "src/server.ts:12", "src/server.ts", 12),
      pathAt(line, "/app/bin/server:3:7", "/app/bin/server", 3),
      { kind: "url", start: line.indexOf("https"), end: line.length, url: "https://example.com/x" },
    ]);
  });

  it("finds nothing in an empty line or plain prose", () => {
    expect(findTerminalLinks("")).toEqual([]);
    expect(findTerminalLinks("All tests passed.")).toEqual([]);
  });

  // Each of these is quadratic under a naive pattern; at this length that is
  // billions of steps and the test times out.
  it.each([
    ["letters", "a".repeat(200_000)],
    ["dotted words", "a.".repeat(100_000)],
    ["a path ending in dots", "/x" + ".".repeat(200_000) + "y"],
    ["a path ending in parens", "/x" + ")".repeat(200_000)],
    ["unclosed brackets", "a/" + "[".repeat(200_000)],
    ["colons and digits", "a" + ":1".repeat(100_000) + "x"],
    ["at signs", "a@".repeat(100_000)],
  ])("stays linear on a long run of %s", (_, line) => {
    const t = performance.now();
    findTerminalLinks(line);
    expect(performance.now() - t).toBeLessThan(1000);
  });
});
