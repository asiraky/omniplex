// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { emptyState } from "~/apply";
import { Panel } from "~/components/panel/Panel";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "~/components/ui/dropdown-menu";
import { render } from "~/test/harness";

function renderPanel(onClose: () => void) {
  const never = () => new Promise<never>(() => {});
  render(
    <>
      <Panel
        sessionId="s1"
        state={emptyState("s1")}
        command={never}
        open
        onClose={onClose}
        revision="r"
        loadChanges={never}
        loadDiff={never}
        loadTree={never}
        loadFile={never}
      />
      <DropdownMenu>
        <DropdownMenuTrigger>Menu</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </>,
  );
}

describe("Panel", () => {
  it("closes on Escape", () => {
    const onClose = vi.fn();
    renderPanel(onClose);
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("leaves Escape to an open menu, which closes alone", async () => {
    const onClose = vi.fn();
    renderPanel(onClose);
    fireEvent.pointerDown(screen.getByRole("button", { name: "Menu" }), { button: 0, ctrlKey: false });
    const item = await screen.findByRole("menuitem", { name: "Item" });
    fireEvent.keyDown(item, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menuitem")).toBeNull());
    expect(onClose).not.toHaveBeenCalled();
  });
});
