// @vitest-environment jsdom
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { PullRequest } from "./protocol";
import { PR_POLL_MS, useSessionPR } from "./useSessionPR";

const MERGED: PullRequest = {
  number: 75,
  state: "MERGED",
  merged: true,
  mergedAt: "2026-08-20T01:02:03Z",
};
const OPEN: PullRequest = { number: 75, state: "OPEN", merged: false };

function Probe({
  sessionId,
  eligible,
  fetchPR,
  seen,
}: {
  sessionId: string | null;
  eligible: boolean;
  fetchPR: (id: string) => Promise<PullRequest | null>;
  seen: (pr: PullRequest | null) => void;
}) {
  seen(useSessionPR(sessionId, eligible, fetchPR));
  return null;
}

// The hook awaits the fetch before scheduling the next poll, so advancing the
// clock is not enough on its own — the microtask queue has to drain too.
async function tick(ms = 0) {
  await act(async () => {
    if (ms) vi.advanceTimersByTime(ms);
    await Promise.resolve();
  });
}

describe("useSessionPR", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("reports a merge to whoever is watching", async () => {
    const fetchPR = vi.fn().mockResolvedValue(MERGED);
    const seen = vi.fn();
    render(<Probe sessionId="s1" eligible fetchPR={fetchPR} seen={seen} />);
    await tick();
    expect(seen).toHaveBeenLastCalledWith(MERGED);
  });

  it("stops asking once the branch has landed", async () => {
    const fetchPR = vi.fn().mockResolvedValue(MERGED);
    render(<Probe sessionId="s1" eligible fetchPR={fetchPR} seen={() => {}} />);
    await tick();
    expect(fetchPR).toHaveBeenCalledTimes(1);
    await tick(PR_POLL_MS * 3);
    // A merge cannot be undone, so a second question has no answer to find.
    expect(fetchPR).toHaveBeenCalledTimes(1);
  });

  it("keeps asking while the pull request is still open", async () => {
    const fetchPR = vi.fn().mockResolvedValue(OPEN);
    render(<Probe sessionId="s1" eligible fetchPR={fetchPR} seen={() => {}} />);
    await tick();
    expect(fetchPR).toHaveBeenCalledTimes(1);
    await tick(PR_POLL_MS);
    expect(fetchPR).toHaveBeenCalledTimes(2);
  });

  it("asks nothing of a session that could not have a pull request", async () => {
    const fetchPR = vi.fn().mockResolvedValue(MERGED);
    render(<Probe sessionId="s1" eligible={false} fetchPR={fetchPR} seen={() => {}} />);
    await tick(PR_POLL_MS * 2);
    expect(fetchPR).not.toHaveBeenCalled();
  });

  it("treats a failed lookup as simply not knowing", async () => {
    const fetchPR = vi.fn().mockRejectedValue(new Error("gh is not installed"));
    const seen = vi.fn();
    render(<Probe sessionId="s1" eligible fetchPR={fetchPR} seen={seen} />);
    await tick();
    expect(seen).toHaveBeenLastCalledWith(null);
    // And it tries again later: gh may be installed, or the PR opened, since.
    await tick(PR_POLL_MS);
    expect(fetchPR).toHaveBeenCalledTimes(2);
  });

  it("does not show one session's merge under another's transcript", async () => {
    const fetchPR = vi.fn().mockImplementation(async (id: string) => (id === "s1" ? MERGED : null));
    const seen = vi.fn();
    const { rerender } = render(
      <Probe sessionId="s1" eligible fetchPR={fetchPR} seen={seen} />,
    );
    await tick();
    expect(seen).toHaveBeenLastCalledWith(MERGED);

    rerender(<Probe sessionId="s2" eligible fetchPR={fetchPR} seen={seen} />);
    // Cleared on the switch itself, before the new session's answer arrives.
    expect(seen).toHaveBeenLastCalledWith(null);
    await tick();
    expect(seen).toHaveBeenLastCalledWith(null);
  });
});
