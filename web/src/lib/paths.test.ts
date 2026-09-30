import { describe, expect, it } from "vitest";

import { detectPath } from "./paths";

describe("detectPath", () => {
  it("accepts slashed paths", () => {
    expect(detectPath("internal/thread/lifecycle.go")).toEqual({
      path: "internal/thread/lifecycle.go",
      line: undefined,
    });
    expect(detectPath("web/src/App.tsx")).toEqual({
      path: "web/src/App.tsx",
      line: undefined,
    });
    expect(detectPath("./web/main.ts")).toEqual({
      path: "web/main.ts",
      line: undefined,
    });
  });

  it("accepts bare filenames with an extension", () => {
    expect(detectPath("App.tsx")).toEqual({ path: "App.tsx", line: undefined });
    expect(detectPath("go.mod")).toEqual({ path: "go.mod", line: undefined });
  });

  it("accepts the extensionless allowlist", () => {
    expect(detectPath("Makefile")).toEqual({
      path: "Makefile",
      line: undefined,
    });
    expect(detectPath("Dockerfile")).toEqual({
      path: "Dockerfile",
      line: undefined,
    });
    expect(detectPath("README")).toEqual({ path: "README", line: undefined });
  });

  it("splits line and column anchors", () => {
    expect(detectPath("ws.go:289")).toEqual({ path: "ws.go", line: 289 });
    expect(detectPath("web/src/App.tsx:12:5")).toEqual({
      path: "web/src/App.tsx",
      line: 12,
    });
  });

  it("rejects domains and URLs", () => {
    expect(detectPath("example.com")).toBeNull();
    expect(detectPath("claude.ai")).toBeNull();
    expect(detectPath("https://example.com/a/b.ts")).toBeNull();
    expect(detectPath("www.example.com")).toBeNull();
  });

  it("rejects bare words, flags, dates and code", () => {
    expect(detectPath("filter")).toBeNull();
    expect(detectPath("useMemo")).toBeNull();
    expect(detectPath("--no-color")).toBeNull();
    expect(detectPath("1/2/2024")).toBeNull();
    expect(detectPath("a + b")).toBeNull();
    expect(detectPath("items.map(f)")).toBeNull();
    expect(detectPath("..")).toBeNull();
    expect(detectPath("../outside.txt")).toBeNull();
  });

  it("keeps directory references", () => {
    expect(detectPath("web/src/")).toEqual({
      path: "web/src",
      line: undefined,
    });
  });

  it("rejects slashed tokens that are protocol/method names, not paths", () => {
    expect(detectPath("system/init")).toBeNull();
    expect(detectPath("turn/interrupt")).toBeNull();
    expect(detectPath("rawMaxTokens/maxTokens")).toBeNull();
    expect(detectPath("a/b")).toBeNull();
  });

  it("keeps extensionless slashed paths when anchored or explicitly a directory", () => {
    expect(detectPath("internal/adapter/claude.go")).toEqual({
      path: "internal/adapter/claude.go",
      line: undefined,
    });
    expect(detectPath("system/init:5")).toEqual({
      path: "system/init",
      line: 5,
    });
    expect(detectPath("internal/thread/")).toEqual({
      path: "internal/thread",
      line: undefined,
    });
  });

  it("keeps extensionless slashed paths with a relative or absolute prefix", () => {
    expect(detectPath("./scripts/build")).toEqual({
      path: "scripts/build",
      line: undefined,
    });
    expect(detectPath("/Users/aaron/code/omniplex/bin/dev")).toEqual({
      path: "/Users/aaron/code/omniplex/bin/dev",
      line: undefined,
    });
  });

  it("rejects slash commands and skill names", () => {
    expect(detectPath("/code-review")).toBeNull();
    expect(detectPath("/loop")).toBeNull();
    expect(detectPath("/simplify")).toBeNull();
    // A bare filename at the root is likelier a skill than a file.
    expect(detectPath("/readme")).toBeNull();
    expect(detectPath("/license")).toBeNull();
    // Still a path once there is a directory above the name, an extension, a
    // trailing slash, a line anchor, or a leading dot.
    expect(detectPath("/usr/bin/dev")).toEqual({
      path: "/usr/bin/dev",
      line: undefined,
    });
    expect(detectPath("/tmp/")).toEqual({ path: "/tmp", line: undefined });
    expect(detectPath("/main.go")).toEqual({
      path: "/main.go",
      line: undefined,
    });
    expect(detectPath("/loop:12")).toEqual({ path: "/loop", line: 12 });
    expect(detectPath("/.env")).toEqual({ path: "/.env", line: undefined });
  });

  it("keeps known bare filenames inside a directory", () => {
    expect(detectPath("docs/README")).toEqual({
      path: "docs/README",
      line: undefined,
    });
    expect(detectPath(".github/CODEOWNERS")).toEqual({
      path: ".github/CODEOWNERS",
      line: undefined,
    });
  });

  it("does not treat a dotted hostname segment as an extension", () => {
    expect(detectPath("github.com/asiraky/omniplex")).toBeNull();
    expect(detectPath("api.example.com/system/init")).toBeNull();
  });
});
