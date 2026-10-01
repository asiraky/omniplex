// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";
import type { SetupCheck, SetupReport } from "~/protocol";

import { SetupPage } from "./Setup";

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn() }));
vi.mock("~/lib/toast", () => ({ toast }));

afterEach(() => vi.unstubAllGlobals());

const git = (ready: boolean): SetupCheck => ({
  id: "git",
  name: "Git",
  kind: "tool",
  availability: ready
    ? { state: "ready" }
    : {
        state: "unavailable",
        reason: "Git is not installed.",
        remedy: [{ text: "Download Git", url: "https://git-scm.com/downloads" }],
      },
});

const signedOut = (id: string, name: string, command: string): SetupCheck => ({
  id,
  name,
  kind: "harness",
  availability: {
    state: "unavailable",
    reason: `${name} is not signed in.`,
    remedy: [{ text: "Sign in", command, action: "login" }],
  },
});

const notInstalled = (id: string, name: string, command: string): SetupCheck => ({
  id,
  name,
  kind: "harness",
  availability: {
    state: "unavailable",
    reason: `${name} is not installed.`,
    remedy: [{ text: "Run this in a terminal", command }],
  },
});

const readyHarness = (id: string, name: string): SetupCheck => ({
  id,
  name,
  kind: "harness",
  availability: { state: "ready" },
});

const report = (checks: SetupCheck[], ready = false): SetupReport => ({
  platform: "darwin",
  ready,
  checks,
});

const card = (name: string) => screen.getByRole("article", { name });

function renderPage(load: () => Promise<SetupReport>, extra: Partial<Parameters<typeof SetupPage>[0]> = {}) {
  const onContinue = vi.fn();
  const onLogin = vi.fn();
  const utils = render(
    <SetupPage load={load} onLogin={onLogin} onContinue={onContinue} {...extra} />,
  );
  return { ...utils, onContinue, onLogin };
}

describe("SetupPage", () => {
  it("shows each check's state from the report", async () => {
    const load = vi.fn().mockResolvedValue(
      report([
        git(true),
        signedOut("claude", "Claude Code", "claude auth login"),
        notInstalled("codex", "Codex", "npm i -g @openai/codex"),
      ]),
    );
    renderPage(load);

    await waitFor(() => expect(card("Git").dataset.status).toBe("ready"));
    expect(card("Claude Code").dataset.status).toBe("sign-in");
    expect(card("Codex").dataset.status).toBe("missing");
    // The reason is carried through; a ready check has nothing to fix.
    expect(within(card("Claude Code")).getByText("Claude Code is not signed in.")).toBeTruthy();
    expect(within(card("Git")).queryAllByRole("button")).toHaveLength(0);
  });

  it("opens the sign-in flow for the check whose remedy is a login", async () => {
    const claude = signedOut("claude", "Claude Code", "claude auth login");
    const { onLogin } = renderPage(async () => report([git(true), claude]));

    const button = await waitFor(() => within(card("Claude Code")).getByRole("button"));
    fireEvent.click(button);

    expect(onLogin).toHaveBeenCalledWith(claude);
  });

  it("offers the command itself when there is no sign-in flow to open", async () => {
    render(
      <SetupPage
        load={async () => report([signedOut("claude", "Claude Code", "claude auth login")])}
        onContinue={() => {}}
      />,
    );
    await waitFor(() => expect(screen.getByText("claude auth login")).toBeTruthy());
  });

  it("copies a command remedy", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    renderPage(async () => report([notInstalled("codex", "Codex", "npm i -g @openai/codex")]));

    const copy = await waitFor(() =>
      within(card("Codex")).getByRole("button", { name: /npm i -g @openai\/codex/ }),
    );
    await act(async () => fireEvent.click(copy));

    expect(writeText).toHaveBeenCalledWith("npm i -g @openai/codex");
  });

  it("opens a link remedy outside the app", async () => {
    renderPage(async () => report([git(false)]));

    const link = await waitFor(() => within(card("Git")).getByRole("link"));
    expect(link.getAttribute("href")).toBe("https://git-scm.com/downloads");
    expect(link.getAttribute("target")).toBe("_blank");
  });

  it("asks the server to recheck, then reloads the report", async () => {
    const load = vi
      .fn()
      .mockResolvedValueOnce(report([git(true), signedOut("claude", "Claude Code", "x")]))
      .mockResolvedValueOnce(report([git(true), readyHarness("claude", "Claude Code")], true));
    const onRecheck = vi.fn().mockResolvedValue(undefined);
    renderPage(load, { onRecheck });
    await waitFor(() => expect(card("Claude Code").dataset.status).toBe("sign-in"));

    fireEvent.click(screen.getByRole("button", { name: /Check again/ }));

    await waitFor(() => expect(card("Claude Code").dataset.status).toBe("ready"));
    expect(onRecheck).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(2);
    expect(onRecheck.mock.invocationCallOrder[0]).toBeLessThan(load.mock.invocationCallOrder[1]);
  });

  it("still reloads when the recheck fails", async () => {
    const load = vi.fn().mockResolvedValue(report([git(true)]));
    renderPage(load, { onRecheck: () => Promise.reject(new Error("offline")) });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole("button", { name: /Check again/ }));

    await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  });

  it("reloads when its refresh key changes", async () => {
    const load = vi.fn().mockResolvedValue(report([git(true)]));
    const { rerender } = render(<SetupPage load={load} onContinue={() => {}} refreshKey={1} />);
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));

    rerender(wrap(<SetupPage load={load} onContinue={() => {}} refreshKey={2} />));

    await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  });

  it("reports a failed check and recovers on the next one", async () => {
    const load = vi
      .fn()
      .mockRejectedValueOnce(new Error("The server answered 500."))
      .mockResolvedValueOnce(report([git(true)]));
    renderPage(load);

    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /Check again/ }));

    await waitFor(() => expect(card("Git").dataset.status).toBe("ready"));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps only the latest report when two checks cross", async () => {
    let resolveFirst!: (r: SetupReport) => void;
    const load = vi
      .fn()
      .mockImplementationOnce(() => new Promise<SetupReport>((r) => (resolveFirst = r)))
      .mockResolvedValueOnce(report([git(true)], true));
    // A sign-in dialog closing reloads while the first load is still out.
    const { rerender } = render(<SetupPage load={load} onContinue={() => {}} refreshKey={1} />);
    rerender(wrap(<SetupPage load={load} onContinue={() => {}} refreshKey={2} />));
    await waitFor(() => expect(card("Git").dataset.status).toBe("ready"));
    await act(async () => resolveFirst(report([git(false)])));

    expect(card("Git").dataset.status).toBe("ready");
  });

  it("makes continuing the primary action only once setup is ready", async () => {
    const load = vi
      .fn()
      .mockResolvedValueOnce(report([git(false)]))
      .mockResolvedValueOnce(report([git(true), readyHarness("claude", "Claude Code")], true));
    const { onContinue } = renderPage(load);
    const continueButton = () => screen.getAllByRole("button").at(-1)!;

    await waitFor(() => expect(card("Git").dataset.status).toBe("missing"));
    expect(continueButton().dataset.variant).toBe("outline");
    fireEvent.click(continueButton());
    expect(onContinue).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: /Check again/ }));
    await waitFor(() => expect(continueButton().dataset.variant).toBe("default"));
  });

  it("marks the other assistants optional once one is ready", async () => {
    renderPage(async () =>
      report(
        [
          git(true),
          readyHarness("claude", "Claude Code"),
          notInstalled("codex", "Codex", "npm i -g @openai/codex"),
        ],
        true,
      ),
    );

    await waitFor(() => expect(screen.getByRole("status").dataset.ready).toBe("true"));
    expect(within(card("Codex")).getByText(/Claude Code/)).toBeTruthy();
  });
});
