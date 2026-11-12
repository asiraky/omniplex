// @vitest-environment jsdom
import { act, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";

import { TurnStatusLine } from "./TurnStatusLine";

const NOW = 1_731_300_000_000;
const line = () => screen.getByRole("status").textContent;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});
afterEach(() => {
  vi.useRealTimers();
});

describe("a retrying turn", () => {
  it("says which attempt, why, and counts down to the next", () => {
    render(
      <TurnStatusLine
        running
        status={{ state: "retrying", attempt: 2, max: 10, reason: "overloaded", retryAt: NOW + 8000 }}
      />,
    );
    expect(line()).toBe("Retrying (2/10) — overloaded, next in 8s");

    act(() => vi.advanceTimersByTime(3000));
    expect(line()).toBe("Retrying (2/10) — overloaded, next in 5s");
  });

  it("drops the countdown once the retry is due, and stops ticking", () => {
    render(<TurnStatusLine running status={{ state: "retrying", attempt: 2, max: 10, retryAt: NOW + 2000 }} />);
    expect(line()).toBe("Retrying (2/10), next in 2s");

    act(() => vi.advanceTimersByTime(2000));
    expect(line()).toBe("Retrying (2/10)");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("says nothing about timing when the retry time is already past", () => {
    render(<TurnStatusLine running status={{ state: "retrying", reason: "rate limit", retryAt: NOW - 5000 }} />);
    expect(line()).toBe("Retrying — rate limit");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("counts minutes once the wait is a minute or more", () => {
    render(<TurnStatusLine running status={{ state: "retrying", retryAt: NOW + 90_000 }} />);
    expect(line()).toBe("Retrying, next in 1m 30s");
  });

  it("names the attempt alone when the limit is unknown", () => {
    render(<TurnStatusLine running status={{ state: "retrying", attempt: 3 }} />);
    expect(line()).toBe("Retrying (attempt 3)");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("says just retrying when neither attempt nor limit is known", () => {
    render(<TurnStatusLine running status={{ state: "retrying", max: 10 }} />);
    expect(line()).toBe("Retrying");
  });

  it("restarts the countdown when the next attempt is scheduled", () => {
    const { rerender } = render(
      <TurnStatusLine running status={{ state: "retrying", attempt: 1, max: 3, retryAt: NOW + 1000 }} />,
    );
    act(() => vi.advanceTimersByTime(1000));
    expect(line()).toBe("Retrying (1/3)");

    rerender(wrap(<TurnStatusLine running status={{ state: "retrying", attempt: 2, max: 3, retryAt: NOW + 5000 }} />));
    expect(line()).toBe("Retrying (2/3), next in 4s");
  });
});

describe("a compacting turn", () => {
  it("says it is compacting context", () => {
    render(<TurnStatusLine running status={{ state: "compacting" }} />);
    expect(line()).toBe("Compacting context…");
    expect(vi.getTimerCount()).toBe(0);
  });
});
