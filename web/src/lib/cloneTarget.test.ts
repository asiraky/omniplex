import { describe, expect, it } from "vitest";

import { cloneDestination, parentDirectory, repoName } from "./cloneTarget";

describe("repoName", () => {
  it("takes the last segment of an https URL", () => {
    expect(repoName("https://github.com/asiraky/omniplex")).toBe("omniplex");
    expect(repoName("https://github.com/asiraky/omniplex.git")).toBe("omniplex");
    expect(repoName("https://github.com/asiraky/omniplex/")).toBe("omniplex");
  });

  it("handles scp-style ssh, where the host is not part of the path", () => {
    expect(repoName("git@github.com:asiraky/omniplex.git")).toBe("omniplex");
    expect(repoName("git@github.com:omniplex")).toBe("omniplex");
    expect(repoName("ssh://git@github.com/asiraky/omniplex.git")).toBe("omniplex");
  });

  it("accepts a bare owner/repo", () => {
    expect(repoName("asiraky/omniplex")).toBe("omniplex");
    expect(repoName("omniplex")).toBe("omniplex");
  });

  it("strips a query or fragment before looking at the path", () => {
    expect(repoName("https://github.com/asiraky/omniplex.git?ref=main")).toBe("omniplex");
  });

  it("keeps dots that are not the .git suffix", () => {
    expect(repoName("https://github.com/asiraky/omni.plex.git")).toBe("omni.plex");
    expect(repoName("https://gitlab.com/group/sub/dot.files")).toBe("dot.files");
  });

  it("says nothing rather than guessing when there is no name yet", () => {
    expect(repoName("")).toBe("");
    expect(repoName("   ")).toBe("");
    expect(repoName("https://github.com")).toBe("");
    expect(repoName("https://github.com/")).toBe("");
    expect(repoName("/")).toBe("");
    expect(repoName("..")).toBe("");
  });
});

describe("cloneDestination", () => {
  it("joins the parent directory to the name git would pick", () => {
    expect(cloneDestination("/home/aaron/code", "git@github.com:asiraky/omniplex.git")).toBe(
      "/home/aaron/code/omniplex",
    );
  });

  it("does not double a trailing slash on the parent", () => {
    expect(cloneDestination("/home/aaron/code/", "asiraky/omniplex")).toBe(
      "/home/aaron/code/omniplex",
    );
  });

  it("is empty while the URL still names nothing", () => {
    expect(cloneDestination("/home/aaron/code", "https://github.com/")).toBe("");
  });
});

describe("parentDirectory", () => {
  it("is where the next clone should be offered", () => {
    expect(parentDirectory("/home/aaron/code/omniplex")).toBe("/home/aaron/code");
    expect(parentDirectory("/home/aaron/code/omniplex/")).toBe("/home/aaron/code");
    expect(parentDirectory("/omniplex")).toBe("/");
    expect(parentDirectory("omniplex")).toBe("");
  });
});
