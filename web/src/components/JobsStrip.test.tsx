// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { emptyState } from "~/apply";
import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import { render } from "~/test/harness";

import { JobsStrip } from "./JobsStrip";

function mount(props: Partial<React.ComponentProps<typeof JobsStrip>> = {}) {
  const onOpen = vi.fn();
  render(
    <ThreadStoreContext.Provider value={createThreadStore(emptyState("s1"))}>
      <JobsStrip onOpen={onOpen} {...props} />
    </ThreadStoreContext.Provider>,
  );
  return onOpen;
}

describe("JobsStrip", () => {
  it("stays away with no jobs and nothing delegated", () => {
    mount();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows for delegated threads alone, and opens the panel", () => {
    const onOpen = mount({ delegatedLive: 2, delegatedNeedsYou: 1 });
    fireEvent.click(screen.getByRole("button", { name: "1 thread needs you, 2 threads" }));
    expect(onOpen).toHaveBeenCalled();
  });
});
