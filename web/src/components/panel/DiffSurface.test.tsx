// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import type { ComponentProps } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { DiffSurface } from "./DiffSurface";
import { forgetDiffPlaces } from "./diffScroll";
import { render, viewport, wrap } from "~/test/harness";
import type { BranchCommits, FileDiff, ThreadChanges } from "~/protocol";

// Long enough that no container holds it: this is the line the wrap toggle exists for.
const LONG = "x".repeat(400);
const TEXT = `const a = "${LONG}";`;

const CHANGES: ThreadChanges = {
  root: "/tmp/wt",
  branch: "feature/wrap",
  mode: "branch",
  baseRef: "main",
  additions: 1,
  deletions: 0,
  files: [{ path: "web/src/long.ts", status: "modified", additions: 1, deletions: 0 }],
};

const DIFF: FileDiff = {
  path: "web/src/long.ts",
  status: "modified",
  patch: `@@ -1 +1 @@\n+${TEXT}\n`,
};

function changesWith(...paths: string[]): ThreadChanges {
  return {
    root: "/wt",
    mode: "branch",
    additions: paths.length,
    deletions: 0,
    files: paths.map((path) => ({ path, status: "modified" as const, additions: 1, deletions: 0 })),
  };
}

function diffOf(path: string, text = `line in ${path}`): FileDiff {
  return { path, status: "modified", patch: `@@ -1 +1 @@\n-old\n+${text}\n` };
}

const never = () => new Promise<never>(() => {});

type Props = ComponentProps<typeof DiffSurface>;

function surface(overrides: Partial<Props> = {}) {
  return (
    <DiffSurface
      threadId="s1"
      changes={CHANGES}
      loading={false}
      error=""
      onRefresh={() => {}}
      loadDiff={() => Promise.resolve(DIFF)}
      loadCommits={never}
      findInDiff={never}
      comparison="branch"
      onComparisonChange={() => {}}
      {...overrides}
    />
  );
}

function renderSurface(overrides: Partial<Props> = {}) {
  return render(surface(overrides));
}

const row = (name: RegExp | string) => screen.getByRole("button", { name });

/** The diff line's row, whose classes say whether it wraps. */
async function openDiffRow(): Promise<HTMLElement> {
  fireEvent.click(row(/long\.ts/));
  const line = await screen.findByText(TEXT);
  return line.parentElement as HTMLElement;
}

beforeEach(() => {
  localStorage.clear();
  forgetDiffPlaces();
  Element.prototype.scrollIntoView = vi.fn();
});

describe("DiffSurface wrap toggle", () => {
  const toggle = () => screen.getByRole("button", { name: "Wrap text" });

  it("starts unwrapped, with rows sized to their longest line so the container can scroll", async () => {
    renderSurface();
    const line = await openDiffRow();

    expect(line.className).toContain("whitespace-pre");
    expect(line.className).not.toContain("whitespace-pre-wrap");
    // Without this the line spills out of a container-width box and cannot be scrolled to.
    expect(line.className).toContain("w-max");
    expect(toggle().getAttribute("aria-pressed")).toBe("false");
  });

  it("wraps once pressed, and remembers the choice for the next mount", async () => {
    const first = renderSurface();
    fireEvent.click(toggle());
    expect((await openDiffRow()).className).toContain("whitespace-pre-wrap");
    first.unmount();
    forgetDiffPlaces();

    renderSurface();
    expect(toggle().getAttribute("aria-pressed")).toBe("true");
    expect((await openDiffRow()).className).toContain("whitespace-pre-wrap");
  });
});

describe("DiffSurface comparison", () => {
  it("offers uncommitted and branch changes and reports the selection", async () => {
    const change = vi.fn();
    renderSurface({ onComparisonChange: change });

    fireEvent.click(screen.getByRole("combobox", { name: "Diff comparison" }));
    fireEvent.click(await screen.findByRole("option", { name: "Uncommitted changes" }));
    expect(change).toHaveBeenCalledWith("uncommitted");
  });

  it("offers no turn until one is known", async () => {
    renderSurface({ turn: null });
    fireEvent.click(screen.getByRole("combobox", { name: "Diff comparison" }));
    await screen.findByRole("option", { name: "Branch changes" });
    expect(screen.queryByRole("option", { name: /^Turn/ })).toBeNull();
  });

  it("offers a known turn and reports picking it", async () => {
    const change = vi.fn();
    renderSurface({ turn: { id: "t3", number: 3 }, onComparisonChange: change });
    fireEvent.click(screen.getByRole("combobox", { name: "Diff comparison" }));
    fireEvent.click(await screen.findByRole("option", { name: "Turn 3" }));
    expect(change).toHaveBeenCalledWith("turn");
  });
});

// Newest first, as the server lists them.
const COMMITS: BranchCommits = {
  baseRef: "main",
  commits: [
    { sha: "ccc3333000000", subject: "Third", author: "a", time: 3 },
    { sha: "bbb2222000000", subject: "Second", author: "a", time: 2 },
    { sha: "aaa1111000000", subject: "First", author: "a", time: 1 },
  ],
};

describe("DiffSurface commit scope", () => {
  const main = () => screen.getByRole("combobox", { name: "Diff comparison" });
  const range = () => screen.getByRole("combobox", { name: "Extend to a range of commits" });

  it("reads the branch's commits when the select opens, and picks one as a single-commit scope", async () => {
    const loadCommits = vi.fn(() => Promise.resolve(COMMITS));
    const change = vi.fn();
    renderSurface({ loadCommits, onComparisonChange: change });
    // Not before it is asked for: the wire is the scarce thing on a phone.
    expect(loadCommits).not.toHaveBeenCalled();

    fireEvent.click(main());
    fireEvent.click(await screen.findByRole("option", { name: /bbb2222\s*Second/ }));
    expect(loadCommits).toHaveBeenCalledOnce();
    expect(change).toHaveBeenCalledWith("commit", { from: "bbb2222000000", to: "bbb2222000000" });
  });

  it("says so when the list is cut short or cannot be read", async () => {
    const loadCommits = vi
      .fn(() => Promise.resolve({ ...COMMITS, truncated: true }))
      .mockRejectedValueOnce(new Error("not a git checkout"));
    renderSurface({ loadCommits });

    fireEvent.click(main());
    expect(await screen.findByRole("option", { name: "not a git checkout" })).toBeTruthy();
    fireEvent.keyDown(screen.getByRole("listbox"), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());

    fireEvent.click(main());
    expect(await screen.findByRole("option", { name: "Only the latest 3 commits are listed." })).toBeTruthy();
    expect(screen.queryByRole("option", { name: "not a git checkout" })).toBeNull();
  });

  it("extends a commit into a range through any newer commit", async () => {
    const loadCommits = vi.fn(() => Promise.resolve(COMMITS));
    const change = vi.fn();
    renderSurface({
      comparison: "commit",
      commits: { from: "bbb2222000000", to: "bbb2222000000" },
      loadCommits,
      onComparisonChange: change,
    });
    expect(main().textContent).toBe("bbb2222");
    // Mounted in commit mode, the range picker needs the list without being opened.
    await waitFor(() => expect(loadCommits).toHaveBeenCalledOnce());

    fireEvent.click(range());
    expect(await screen.findByRole("option", { name: "Only this commit" })).toBeTruthy();
    // Older than the chosen commit is not a range through it.
    expect(screen.queryByRole("option", { name: /aaa1111/ })).toBeNull();
    fireEvent.click(screen.getByRole("option", { name: /through\s*ccc3333\s*Third/ }));
    expect(change).toHaveBeenCalledWith("commit", { from: "bbb2222000000", to: "ccc3333000000" });
  });

  it("labels a range by its ends, and says when its commits changed nothing", () => {
    renderSurface({
      changes: { ...changesWith(), mode: "commit" },
      comparison: "commit",
      commits: { from: "aaa1111000000", to: "ccc3333000000" },
      loadCommits: () => Promise.resolve(COMMITS),
    });
    expect(main().textContent).toBe("aaa1111…ccc3333");
    expect(screen.getByText("These commits changed nothing.")).toBeTruthy();
  });
});

describe("DiffSurface reading diffs", () => {
  it("drops a diff read against a replaced change list, and reads the open file again against the new one", async () => {
    const reads: { changes: ThreadChanges; resolve: (d: FileDiff) => void }[] = [];
    const loadDiff = (_path: string, changes: ThreadChanges) =>
      new Promise<FileDiff>((resolve) => reads.push({ changes, resolve }));
    const { rerender } = renderSurface({ loadDiff });

    fireEvent.click(row(/long\.ts/));
    const next = { ...CHANGES };
    rerender(wrap(surface({ changes: next, loadDiff })));
    await act(async () => reads[0].resolve(DIFF));
    expect(screen.queryByText(TEXT)).toBeNull();

    // Still open, so it is read again without being closed and reopened.
    expect(reads).toHaveLength(2);
    expect(reads[1].changes).toBe(next);
    await act(async () => reads[1].resolve(DIFF));
    expect(screen.getByText(TEXT)).toBeTruthy();
  });

  it("asks for whitespace-blind and whole-file diffs, and re-reads the open one on each change", async () => {
    const loadDiff = vi.fn((_p: string, _c: ThreadChanges, _o: unknown) => Promise.resolve(DIFF));
    renderSurface({ loadDiff });
    await openDiffRow();
    expect(loadDiff).toHaveBeenLastCalledWith("web/src/long.ts", CHANGES, {
      ignoreWhitespace: false,
      fullContext: false,
    });

    fireEvent.click(screen.getByRole("button", { name: "Ignore whitespace" }));
    await waitFor(() => expect(loadDiff).toHaveBeenCalledTimes(2));
    expect(loadDiff).toHaveBeenLastCalledWith("web/src/long.ts", CHANGES, {
      ignoreWhitespace: true,
      fullContext: false,
    });

    fireEvent.click(screen.getByRole("button", { name: "Show full files" }));
    await waitFor(() => expect(loadDiff).toHaveBeenCalledTimes(3));
    expect(loadDiff).toHaveBeenLastCalledWith("web/src/long.ts", CHANGES, {
      ignoreWhitespace: true,
      fullContext: true,
    });
    expect(await screen.findByText(TEXT)).toBeTruthy();
  });
});

describe("DiffSurface hide tests", () => {
  const MIXED = changesWith("src/a.ts", "src/a.test.ts", "tests/helper.ts");
  const hide = () => screen.getByRole("button", { name: "Hide test files" });

  it("is not offered when nothing in the list is a test", () => {
    renderSurface({ changes: changesWith("src/a.ts") });
    expect(screen.queryByRole("button", { name: "Hide test files" })).toBeNull();
  });

  it("drops test files from the list and says how many it hid", () => {
    renderSurface({ changes: MIXED });
    fireEvent.click(hide());

    expect(row(/a\.ts/).textContent).toContain("src/a.ts");
    expect(screen.queryByRole("button", { name: /a\.test\.ts/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /helper\.ts/ })).toBeNull();
    expect(screen.getByText(/^1 file/).textContent).toBe("1 file · 2 hidden2 test files hidden");
  });

  it("still shows a hidden test file the transcript asks for, until tests are hidden again", async () => {
    localStorage.setItem("omniplex.diffHideTests", "1");
    const onRevealHandled = vi.fn();
    renderSurface({ changes: MIXED, reveal: { path: "src/a.test.ts", nonce: 4 }, onRevealHandled });

    expect((await screen.findByRole("button", { name: /a\.test\.ts/ })).getAttribute("aria-expanded")).toBe("true");
    expect(onRevealHandled).toHaveBeenCalledWith(4);
    expect(screen.queryByRole("button", { name: /helper\.ts/ })).toBeNull();

    fireEvent.click(hide());
    fireEvent.click(hide());
    expect(screen.queryByRole("button", { name: /a\.test\.ts/ })).toBeNull();
  });
});

describe("DiffSurface find", () => {
  const LIST = changesWith("src/alpha.ts", "src/beta.ts", "src/gamma.ts");
  const input = () => screen.getByRole("searchbox", { name: "Find in diff" });
  const listed = () =>
    screen
      .queryAllByRole("button", { name: /\.ts/ })
      .map((b) => b.textContent?.replace(/^\w|\+.*$/g, "").trim());

  function openFind(overrides: Partial<Props> = {}) {
    const loadDiff = vi.fn((path: string) => Promise.resolve(diffOf(path, `${path} has beta in it`)));
    renderSurface({ changes: LIST, loadDiff, ...overrides });
    fireEvent.click(screen.getByRole("button", { name: "Find in diff" }));
    return loadDiff;
  }

  it("opens focused, and filters to path matches and the server's line matches, asking once per pause", async () => {
    const findInDiff = vi.fn((_q: string, _c: ThreadChanges) => Promise.resolve(["src/gamma.ts"]));
    openFind({ findInDiff });
    expect(document.activeElement).toBe(input());

    for (const q of ["b", "be", "bet", "beta"]) fireEvent.change(input(), { target: { value: q } });
    expect(listed()).toEqual(["src/beta.ts"]);
    await waitFor(() => expect(listed()).toEqual(["src/beta.ts", "src/gamma.ts"]));
    expect(findInDiff.mock.calls).toEqual([["beta", LIST]]);
    expect(screen.getByText("2 of 3 files")).toBeTruthy();
  });

  it("steps through the matching files, opening each and marking the query", async () => {
    const findInDiff = vi.fn(() => Promise.resolve(["src/gamma.ts"]));
    const loadDiff = openFind({ findInDiff });
    fireEvent.change(input(), { target: { value: "beta" } });
    await waitFor(() => expect(listed()).toHaveLength(2));
    expect(screen.getByText("0/2")).toBeTruthy();

    fireEvent.keyDown(input(), { key: "Enter" });
    expect(screen.getByText("1/2")).toBeTruthy();
    expect(row(/beta\.ts/).getAttribute("aria-expanded")).toBe("true");
    await waitFor(() => expect(document.querySelector("[data-diff-match]")?.textContent).toBe("beta"));
    expect(loadDiff).toHaveBeenLastCalledWith("src/beta.ts", LIST, expect.anything());

    fireEvent.keyDown(input(), { key: "Enter" });
    expect(screen.getByText("2/2")).toBeTruthy();
    expect(row(/gamma\.ts/).getAttribute("aria-expanded")).toBe("true");

    fireEvent.keyDown(input(), { key: "Enter", shiftKey: true });
    expect(screen.getByText("1/2")).toBeTruthy();
    expect(row(/beta\.ts/).getAttribute("aria-expanded")).toBe("true");

    fireEvent.keyDown(input(), { key: "Escape" });
    expect(screen.queryByRole("searchbox")).toBeNull();
    expect(listed()).toHaveLength(3);
    expect(document.querySelector("[data-diff-match]")).toBeNull();
  });

  it("drops an answer to a query that has since changed", async () => {
    const answers: ((paths: string[]) => void)[] = [];
    const findInDiff = vi.fn(() => new Promise<string[]>((resolve) => answers.push(resolve)));
    openFind({ findInDiff });

    fireEvent.change(input(), { target: { value: "beta" } });
    await waitFor(() => expect(answers).toHaveLength(1));
    fireEvent.change(input(), { target: { value: "gamma" } });
    await waitFor(() => expect(answers).toHaveLength(2));

    await act(async () => answers[0](["src/alpha.ts"]));
    expect(listed()).toEqual(["src/gamma.ts"]);
    await act(async () => answers[1]([]));
    expect(listed()).toEqual(["src/gamma.ts"]);
  });
});

describe("DiffSurface on a phone", () => {
  it("offers no split view, and reads unified even when split was chosen on a desktop", async () => {
    localStorage.setItem("omniplex.diffSplit", "1");
    viewport("phone");
    renderSurface({ loadDiff: () => Promise.resolve(diffOf("web/src/long.ts")) });

    fireEvent.pointerDown(screen.getByRole("button", { name: "Diff options" }), { button: 0, ctrlKey: false });
    expect(await screen.findByRole("menuitemcheckbox", { name: "Wrap text" })).toBeTruthy();
    expect(screen.queryByRole("menuitemcheckbox", { name: "Split view" })).toBeNull();
    fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());

    fireEvent.click(row(/long\.ts/));
    const line = await screen.findByText("line in web/src/long.ts");
    // Split view lays both sides out in one grid; unified is a stack of rows.
    expect(line.closest(".grid")).toBeNull();
  });

  it("splits on a desktop when asked", async () => {
    localStorage.setItem("omniplex.diffSplit", "1");
    renderSurface({ loadDiff: () => Promise.resolve(diffOf("web/src/long.ts")) });
    expect(screen.getByRole("button", { name: "Split view" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(row(/long\.ts/));
    const line = await screen.findByText("line in web/src/long.ts");
    expect(line.closest(".grid")).not.toBeNull();
    // Split always wraps, so the wrap toggle is not offered beside it.
    expect(screen.queryByRole("button", { name: "Wrap text" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Split view" }));
    expect(screen.getByRole("button", { name: "Wrap text" })).toBeTruthy();
  });

  it("opens find from the options menu, with the field focused", async () => {
    viewport("phone");
    renderSurface();
    fireEvent.pointerDown(screen.getByRole("button", { name: "Diff options" }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitem", { name: "Find in diff" }));
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("searchbox", { name: "Find in diff" })));
  });
});

describe("DiffSurface place memory", () => {
  const LIST = changesWith("src/a.ts", "src/b.ts");

  it("reopens the file that was open, reading it again, when the surface comes back", async () => {
    const loadDiff = vi.fn((path: string) => Promise.resolve(diffOf(path)));
    const first = renderSurface({ changes: LIST, loadDiff });
    fireEvent.click(row(/b\.ts/));
    await screen.findByText("line in src/b.ts");
    first.unmount();

    renderSurface({ changes: LIST, loadDiff });
    expect(row(/b\.ts/).getAttribute("aria-expanded")).toBe("true");
    expect(await screen.findByText("line in src/b.ts")).toBeTruthy();
    expect(loadDiff).toHaveBeenCalledTimes(2);
  });

  it("keeps each thread and scope to itself", async () => {
    const first = renderSurface({ changes: LIST, loadDiff: (p) => Promise.resolve(diffOf(p)) });
    fireEvent.click(row(/b\.ts/));
    first.unmount();

    const other = renderSurface({ threadId: "s2", changes: LIST });
    expect(row(/b\.ts/).getAttribute("aria-expanded")).toBe("false");
    other.unmount();

    renderSurface({ comparison: "uncommitted", changes: LIST });
    expect(row(/b\.ts/).getAttribute("aria-expanded")).toBe("false");
  });

  it("gives way to a reveal", async () => {
    const first = renderSurface({ changes: LIST, loadDiff: (p) => Promise.resolve(diffOf(p)) });
    fireEvent.click(row(/b\.ts/));
    first.unmount();

    renderSurface({ changes: LIST, reveal: { path: "src/a.ts", nonce: 1 } });
    expect(row(/a\.ts/).getAttribute("aria-expanded")).toBe("true");
    expect(row(/b\.ts/).getAttribute("aria-expanded")).toBe("false");
  });
});
