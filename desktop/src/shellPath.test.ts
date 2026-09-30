import { describe, expect, it } from "vitest";
import { getPath, knownToolDirs, mergePaths, parseLoginShellPath, resolveServerPath, withPath } from "./shellPath";

const M = "__OMNIPLEX_PATH__";

describe("parseLoginShellPath", () => {
  it("extracts PATH from between the markers, ignoring rc-file noise", () => {
    const out = `Last login: today\nwelcome!\n${M}/opt/homebrew/bin:/usr/bin${M}\nbye\n`;
    expect(parseLoginShellPath(out)).toBe("/opt/homebrew/bin:/usr/bin");
  });

  it("returns undefined when the output is cut off or empty", () => {
    expect(parseLoginShellPath("")).toBeUndefined();
    expect(parseLoginShellPath(`${M}/usr/bin`)).toBeUndefined();
    expect(parseLoginShellPath(`${M}${M}`)).toBeUndefined();
  });
});

describe("mergePaths", () => {
  it("keeps the first occurrence of each directory in priority order", () => {
    expect(mergePaths(":", "/a:/b", "/b:/c:", undefined, "/a:/d")).toBe("/a:/b:/c:/d");
  });

  it("treats Windows directories case-insensitively", () => {
    expect(mergePaths(";", "C:\\Git\\cmd;C:\\x", "c:\\git\\CMD;C:\\y")).toBe("C:\\Git\\cmd;C:\\x;C:\\y");
  });
});

describe("resolveServerPath", () => {
  it("puts the login shell's PATH before the inherited one and appends install dirs", async () => {
    const result = await resolveServerPath({
      platform: "darwin",
      env: { PATH: "/usr/bin:/bin", SHELL: "/bin/zsh" },
      home: "/Users/me",
      readShellPath: async (shell) => (shell === "/bin/zsh" ? "/Users/me/.nvm/bin:/usr/bin" : undefined),
    });
    const dirs = result.split(":");
    expect(dirs[0]).toBe("/Users/me/.nvm/bin");
    expect(dirs).toContain("/opt/homebrew/bin");
    expect(dirs).toContain("/Users/me/.local/bin");
    expect(new Set(dirs).size).toBe(dirs.length);
  });

  it("still finds the usual install dirs when the login shell gives nothing", async () => {
    const result = await resolveServerPath({
      platform: "darwin",
      env: { PATH: "/usr/bin:/bin" },
      home: "/Users/me",
      readShellPath: async () => undefined,
    });
    expect(result.startsWith("/usr/bin:/bin:")).toBe(true);
    expect(result.split(":")).toContain("/Users/me/.claude/local");
  });

  it("does not run a shell on Windows", async () => {
    const env = { Path: "C:\\Windows", APPDATA: "C:\\Users\\me\\AppData\\Roaming" };
    const result = await resolveServerPath({
      platform: "win32",
      env,
      home: "C:\\Users\\me",
      readShellPath: async () => {
        throw new Error("no shell on windows");
      },
    });
    expect(result.split(";")[0]).toBe("C:\\Windows");
    expect(result.split(";")).toContain("C:\\Users\\me\\AppData\\Roaming\\npm");
  });
});

describe("knownToolDirs", () => {
  it("only adds Homebrew's Apple Silicon prefix on macOS", () => {
    expect(knownToolDirs("darwin", "/h", {})).toContain("/opt/homebrew/bin");
    expect(knownToolDirs("linux", "/h", {})).not.toContain("/opt/homebrew/bin");
  });
});

describe("withPath", () => {
  it("replaces an existing Path key rather than adding a second one", () => {
    const env = withPath({ Path: "C:\\old", OTHER: "x" }, "C:\\new");
    expect(Object.keys(env).filter((k) => k.toUpperCase() === "PATH")).toEqual(["Path"]);
    expect(getPath(env)).toBe("C:\\new");
    expect(env.OTHER).toBe("x");
  });

  it("adds PATH when there was none", () => {
    expect(withPath({}, "/usr/bin").PATH).toBe("/usr/bin");
  });
});
