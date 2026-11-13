// @vitest-environment jsdom
import { waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import type { PanelControls } from "~/app/usePanel";
import type { OpenPath } from "~/lib/openPath";
import type { ThreadState } from "~/protocol";
import { render } from "~/test/harness";

import { ThreadPanel } from "./ThreadPanel";

const { panelProps, toast } = vi.hoisted(() => ({
  panelProps: { onOpenPath: undefined as OpenPath | undefined },
  toast: { info: vi.fn() },
}));

vi.mock("./panel/Panel", () => ({
  Panel: (props: { onOpenPath: OpenPath }) => {
    panelProps.onOpenPath = props.onOpenPath;
    return null;
  },
}));
vi.mock("~/lib/toast", () => ({ toast }));

// A path as the terminal hands it on, opened through a thread whose recorded
// working directory is `cwd`.
async function openFromTerminal(cwd: string) {
  const openPath = vi.fn();
  const panel = { loaded: true, openPath } as unknown as PanelControls;
  render(
    <ThreadStoreContext.Provider value={createThreadStore({ cwd } as ThreadState)}>
      <ThreadPanel
        clientRef={{ current: null }}
        threadId="t1"
        panel={panel}
        pr={null}
        onAddToChat={() => {}}
        onAddComment={() => {}}
      />
    </ThreadStoreContext.Provider>,
  );
  await waitFor(() => expect(panelProps.onOpenPath).toBeDefined());
  return { open: panelProps.onOpenPath!, openPath };
}

beforeEach(() => {
  panelProps.onOpenPath = undefined;
  toast.info.mockClear();
});

describe("ThreadPanel: a terminal's paths", () => {
  it("opens a path the terminal placed under the checkout root, though the thread sits in a subdirectory", async () => {
    const { open, openPath } = await openFromTerminal("/r/web");
    open("web/src/a.ts", 3);
    open("go.mod");
    expect(openPath.mock.calls).toEqual([
      ["web/src/a.ts", 3],
      ["go.mod", undefined],
    ]);
    expect(toast.info).not.toHaveBeenCalled();
  });

  it("says a path outside the checkout is, rather than opening it", async () => {
    const { open, openPath } = await openFromTerminal("/r/web");
    open("/etc/hosts");
    open("~/notes.md");
    open("../other/a.ts");
    expect(openPath).not.toHaveBeenCalled();
    expect(toast.info).toHaveBeenCalledTimes(3);
  });

  it("places an absolute path against the thread's directory when the shell could not", async () => {
    // The shell's answer is the root with symlinks resolved; the thread
    // records it as the user reaches it.
    const { open, openPath } = await openFromTerminal("/link/r");
    open("/link/r/go.mod", 2);
    expect(openPath).toHaveBeenCalledWith("go.mod", 2);
  });
});
