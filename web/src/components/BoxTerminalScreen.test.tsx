// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TerminalSurface } from "~/components/panel/TerminalSurface";
import { endBoxShell } from "~/lib/termSocket";
import { render, viewport } from "~/test/harness";

import { BoxTerminalScreen } from "./BoxTerminalScreen";

type SurfaceProps = Parameters<typeof TerminalSurface>[0];
const surface = vi.hoisted(() => ({ props: null as SurfaceProps | null }));

vi.mock("~/components/panel/TerminalSurface", () => ({
  TerminalSurface: (props: SurfaceProps) => {
    surface.props = props;
    return (
      <div data-terminal="">
        <textarea aria-label="Terminal input" />
      </div>
    );
  },
}));

vi.mock("~/lib/termSocket", () => ({ endBoxShell: vi.fn(() => Promise.resolve()) }));

beforeEach(() => {
  surface.props = null;
  vi.mocked(endBoxShell).mockClear();
});

function mount(over: Partial<Parameters<typeof BoxTerminalScreen>[0]> = {}) {
  const props = { sidebarOpen: true, onShowSidebar: vi.fn(), onClose: vi.fn(), ...over };
  render(<BoxTerminalScreen {...props} />);
  return props;
}

async function openEndConfirm() {
  fireEvent.pointerDown(screen.getByRole("button", { name: "Terminal actions" }), { button: 0, ctrlKey: false });
  fireEvent.click(await screen.findByRole("menuitem", { name: /End shell/ }));
  return screen.findByRole("dialog");
}

describe("BoxTerminalScreen", () => {
  it("shows where the shell is as it moves, with ~ for home", () => {
    mount();
    expect(screen.queryByText(/box ·/)).toBeNull();
    act(() => surface.props!.onCwd!("/home/me/code/omniplex", "/home/me"));
    expect(screen.getByText("box · ~/code/omniplex")).toBeTruthy();
    act(() => surface.props!.onCwd!("/tmp", "/home/me"));
    expect(screen.getByText("box · /tmp")).toBeTruthy();
  });

  it("takes the keyboard on a desktop as it opens", () => {
    mount();
    expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "Terminal input" }));
  });

  it("leaves the keyboard down on a phone", () => {
    viewport("phone");
    mount();
    expect(document.activeElement).not.toBe(screen.getByRole("textbox", { name: "Terminal input" }));
  });

  it("closes from its X on a desktop", () => {
    const props = mount();
    expect(screen.queryByRole("button", { name: "Back" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Close terminal" }));
    expect(props.onClose).toHaveBeenCalledOnce();
  });

  it("closes from Back on a phone", () => {
    viewport("phone");
    const props = mount();
    expect(screen.queryByRole("button", { name: "Close terminal" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(props.onClose).toHaveBeenCalledOnce();
  });

  it("offers the sidebar back on a desktop only while it is hidden", () => {
    const props = mount({ sidebarOpen: false });
    fireEvent.click(screen.getByRole("button", { name: /Show threads/ }));
    expect(props.onShowSidebar).toHaveBeenCalledOnce();
  });

  it("does not offer the sidebar while it is showing", () => {
    mount({ sidebarOpen: true });
    expect(screen.queryByRole("button", { name: /Show threads/ })).toBeNull();
  });

  it("ends the shell once confirmed", async () => {
    mount();
    const dialog = await openEndConfirm();
    expect(endBoxShell).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "End shell" }));
    expect(endBoxShell).toHaveBeenCalledExactlyOnceWith("box");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("leaves the shell running when the confirm is cancelled", async () => {
    mount();
    const dialog = await openEndConfirm();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(endBoxShell).not.toHaveBeenCalled();
  });

  it("hands add-to-chat to the surface only when there is a thread to take it", () => {
    const onAddToChat = vi.fn();
    const { unmount } = render(
      <BoxTerminalScreen sidebarOpen onShowSidebar={vi.fn()} onClose={vi.fn()} onAddToChat={onAddToChat} />,
    );
    surface.props!.onAddToChat!("boom");
    expect(onAddToChat).toHaveBeenCalledWith("boom");
    unmount();

    mount();
    expect(surface.props?.onAddToChat).toBeUndefined();
  });
});
