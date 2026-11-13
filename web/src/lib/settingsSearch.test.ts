import { describe, expect, it } from "vitest";

import type { Folder, HarnessMeta, Project } from "~/protocol";
import {
  searchSettings,
  settingsIndex,
  type SettingEntry,
} from "./settingsSearch";

const folder = (id: string, path: string, git: boolean): Folder => ({
  id,
  path,
  git,
  copiesDir: ".worktrees",
  provisionTimeoutSeconds: 1800,
  deprovisionTimeoutSeconds: 600,
});

const project = (id: string, name: string, folders: Folder[]): Project => ({
  id,
  name,
  defaults: { harness: "claude", workspace: "local" },
  folders,
  createdAt: 0,
  updatedAt: 0,
});

const harness = (
  id: string,
  name: string,
  instances: { id: string; displayName: string }[],
) =>
  ({
    id,
    name,
    accent: "#000",
    models: [],
    permissionModes: [],
    availability: { state: "ready" },
    instances: instances.map((i) => ({
      ...i,
      driver: id,
      enabled: true,
      availability: { state: "ready" },
    })),
  }) as unknown as HarnessMeta;

const entry = (
  label: string,
  over: Partial<SettingEntry> = {},
): SettingEntry => ({
  place: { kind: "general" },
  anchor: label,
  label,
  description: "",
  where: "General",
  ...over,
});

const labels = (es: SettingEntry[]) => es.map((e) => e.label);
const anchors = (es: SettingEntry[]) => es.map((e) => e.anchor);

describe("searchSettings", () => {
  const entries = [entry("Alpha"), entry("Beta"), entry("Gamma")];

  it("finds nothing for an empty or blank query", () => {
    expect(searchSettings(entries, "")).toEqual([]);
    expect(searchSettings(entries, "   \t ")).toEqual([]);
  });

  it("ignores case on both sides", () => {
    const es = [
      entry("Base Branch", {
        description: "Where COPIES start",
        where: "MyProj",
      }),
    ];
    expect(labels(searchSettings(es, "bAsE"))).toEqual(["Base Branch"]);
    expect(labels(searchSettings(es, "copies"))).toEqual(["Base Branch"]);
    expect(labels(searchSettings(es, "myproj"))).toEqual(["Base Branch"]);
  });

  it("needs every word, each in any field", () => {
    const es = [
      entry("Sound", {
        description: "a chime",
        keywords: ["audio"],
        where: "Notifications",
      }),
      entry("Voice", { description: "a chime" }),
    ];
    // "sound" from the label, "audio" from keywords, "notif" from where.
    expect(labels(searchSettings(es, "sound audio notif"))).toEqual(["Sound"]);
    expect(labels(searchSettings(es, "chime"))).toEqual(["Sound", "Voice"]);
    // One word that appears nowhere rules the entry out.
    expect(searchSettings(es, "chime nowhere")).toEqual([]);
  });

  it("puts a label starting with the query first, then a label holding every word, then the rest", () => {
    const es = [
      entry("Push only", { anchor: "out" }),
      entry("Other", { anchor: "desc", description: "push sound here" }),
      entry("Sound push", { anchor: "words" }),
      entry("Push sound alerts", { anchor: "starts" }),
    ];
    expect(anchors(searchSettings(es, "push sound"))).toEqual([
      "starts",
      "words",
      "desc",
    ]);
  });

  it("keeps the given order within a rank", () => {
    const es = [
      entry("Default b", { anchor: "1" }),
      entry("x", { anchor: "2", keywords: ["default"] }),
      entry("Default a", { anchor: "3" }),
    ];
    expect(anchors(searchSettings(es, "default"))).toEqual(["1", "3", "2"]);
  });

  it("trims the query before comparing it with the start of a label", () => {
    const es = [
      entry("Branch base", { anchor: "words" }),
      entry("Base branch", { anchor: "starts" }),
    ];
    expect(anchors(searchSettings(es, "  base br  "))).toEqual([
      "starts",
      "words",
    ]);
  });
});

describe("settingsIndex", () => {
  const alpha = project("p1", "alpha", [folder("a1", "/src/alpha", true)]);
  const beta = project("p2", "beta", [folder("b1", "/src/beta", true)]);

  it("offers the voice settings only with an ElevenLabs key", () => {
    const without = settingsIndex({
      projects: [alpha],
      harnesses: [],
      hasElevenLabsKey: false,
    });
    const withKey = settingsIndex({
      projects: [alpha],
      harnesses: [],
      hasElevenLabsKey: true,
    });

    const voice = (es: SettingEntry[]) =>
      es.filter(
        (e) => e.anchor === "voice" || e.anchor === "dictation-cleanup",
      );
    expect(voice(without)).toEqual([]);
    const found = voice(withKey).map((e) => e.place);
    expect(found).toContainEqual({ kind: "general" });
    expect(found).toContainEqual({ kind: "project", id: "p1" });
    // The key itself is set from General either way.
    expect(without.some((e) => e.anchor === "elevenlabs-key")).toBe(true);
  });

  it("offers workspace settings only for git folders", () => {
    const plain = project("p3", "plain", [folder("c1", "/src/plain", false)]);
    const es = settingsIndex({
      projects: [plain, alpha],
      harnesses: [],
      hasElevenLabsKey: false,
    });
    const workspace = es.filter((e) =>
      /^(base-branch|provision|deprovision)-/.test(e.anchor),
    );
    expect(workspace.length).toBeGreaterThan(0);
    expect(
      workspace.every((e) => e.place.kind === "project" && e.place.id === "p1"),
    ).toBe(true);
    expect(workspace.every((e) => e.anchor.endsWith("-a1"))).toBe(true);
    // The non-git project still has its own settings.
    expect(
      es.some((e) => e.place.kind === "project" && e.place.id === "p3"),
    ).toBe(true);
  });

  it("names the folder in a workspace's place only when the project has more than one git folder", () => {
    const two = project("p4", "duo", [
      folder("d1", "/src/duo/web", true),
      folder("d2", "/src/duo/api", true),
      folder("d3", "/src/duo/notes", false),
    ]);
    const es = settingsIndex({
      projects: [alpha, two],
      harnesses: [],
      hasElevenLabsKey: false,
    });
    const whereOf = (anchor: string) =>
      es.find((e) => e.anchor === anchor)!.where;

    expect(whereOf("base-branch-a1")).toBe("alpha");
    expect(whereOf("base-branch-d1")).toContain("/src/duo/web");
    expect(whereOf("base-branch-d2")).toContain("/src/duo/api");
    expect(whereOf("base-branch-d1")).toContain("duo");
    // Settings that are the project's, not a folder's, never carry a path.
    const projectWide = es.filter(
      (e) =>
        e.place.kind === "project" &&
        e.place.id === "p4" &&
        !/-d\d$/.test(e.anchor),
    );
    expect(projectWide.length).toBeGreaterThan(0);
    expect(projectWide.every((e) => e.where === "duo")).toBe(true);
  });

  it("finds a project's own setting by the project's name and the setting's", () => {
    const es = settingsIndex({
      projects: [alpha, beta],
      harnesses: [],
      hasElevenLabsKey: false,
    });
    const hits = searchSettings(es, "beta branch");
    expect(hits.length).toBeGreaterThan(0);
    expect(
      hits.every((e) => e.place.kind === "project" && e.place.id === "p2"),
    ).toBe(true);
    expect(hits[0].anchor).toBe("base-branch-b1");
  });

  it("tells two git folders' workspaces apart by path", () => {
    const two = project("p4", "duo", [
      folder("d1", "/src/duo/web", true),
      folder("d2", "/src/duo/api", true),
    ]);
    const es = settingsIndex({
      projects: [two],
      harnesses: [],
      hasElevenLabsKey: false,
    });
    expect(anchors(searchSettings(es, "api base branch"))).toEqual([
      "base-branch-d2",
    ]);
  });

  it("finds a harness by an account's name, falling back to the account id", () => {
    const es = settingsIndex({
      projects: [],
      harnesses: [
        harness("codex", "Codex", [{ id: "c-1", displayName: "Zephyrwork" }]),
        harness("claude", "Claude", [{ id: "quuxacct", displayName: "" }]),
      ],
      hasElevenLabsKey: false,
    });
    const named = searchSettings(es, "zephyrwork");
    expect(named.map((e) => [e.anchor, e.place])).toEqual([
      ["harness-codex", { kind: "providers" }],
    ]);
    expect(anchors(searchSettings(es, "quuxacct"))).toEqual(["harness-claude"]);
  });

  it("copes with a harness that reports no instances", () => {
    const h = {
      ...harness("pi", "Pi", []),
      instances: undefined,
    } as unknown as HarnessMeta;
    const es = settingsIndex({
      projects: [],
      harnesses: [h],
      hasElevenLabsKey: false,
    });
    expect(anchors(searchSettings(es, "pi")).includes("harness-pi")).toBe(true);
  });

  it("gives every entry a distinct place and anchor", () => {
    const two = project("p4", "duo", [
      folder("d1", "/a", true),
      folder("d2", "/b", true),
    ]);
    const es = settingsIndex({
      projects: [alpha, beta, two],
      harnesses: [
        harness("codex", "Codex", [{ id: "c", displayName: "Work" }]),
      ],
      hasElevenLabsKey: true,
    });
    const keys = es.map(
      (e) =>
        `${e.place.kind === "project" ? e.place.id : e.place.kind}:${e.anchor}`,
    );
    expect(new Set(keys).size).toBe(keys.length);
  });
});
