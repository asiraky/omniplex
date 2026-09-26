// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { frameFit, MiniBrowser } from "~/components/artefacts/MiniBrowser";
import { render } from "~/test/harness";

const frame = () => screen.getByTitle("page") as HTMLIFrameElement;

function fromFrame(data: unknown) {
  act(() => {
    window.dispatchEvent(new MessageEvent("message", { data, source: frame().contentWindow }));
  });
}

function mount(expiresAt = Date.now() + 3_600_000, refresh = vi.fn()) {
  render(
    <MiniBrowser title="page" preview={{ url: "/p/tok1/index.html", expiresAt }} refreshPreview={refresh} />,
  );
  return refresh;
}

describe("MiniBrowser", () => {
  it("follows the page's navigation", () => {
    mount();
    expect(screen.getByRole("button", { name: "Back" })).toHaveProperty("disabled", true);
    fromFrame({ omniplex: "nav", url: `${location.origin}/p/tok1/docs/b.html#x`, title: "B", canBack: true });
    expect(screen.getByTestId("url-pill").textContent).toBe("/docs/b.html#x");
    expect(screen.getByRole("button", { name: "Back" })).toHaveProperty("disabled", false);
  });

  it("enables forward only after going back", () => {
    mount();
    const forward = () => screen.getByRole("button", { name: "Forward" });
    fromFrame({ omniplex: "nav", url: "/p/tok1/b.html", title: "", canBack: true });
    expect(forward()).toHaveProperty("disabled", true);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    fromFrame({ omniplex: "nav", url: "/p/tok1/index.html", title: "", canBack: false });
    expect(forward()).toHaveProperty("disabled", false);
    // A fresh navigation drops the forward history, as a browser does.
    fromFrame({ omniplex: "nav", url: "/p/tok1/c.html", title: "", canBack: true });
    expect(forward()).toHaveProperty("disabled", true);
  });

  it("counts console errors and lists what was logged", () => {
    mount();
    fromFrame({ omniplex: "console", level: "error", text: "boom" });
    fromFrame({ omniplex: "console", level: "log", text: "fine" });
    fromFrame({ omniplex: "console", level: "error", text: "again" });
    expect(screen.getByLabelText("2 console errors")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Show the console" }));
    const list = screen.getByRole("list", { name: "Console messages" });
    expect(Array.from(list.querySelectorAll("li")).map((li) => li.textContent)).toEqual(["boom", "fine", "again"]);
  });

  it("ignores messages from anything but its own frame", () => {
    mount();
    act(() => {
      window.dispatchEvent(new MessageEvent("message", { data: { omniplex: "console", level: "error", text: "x" }, source: window }));
      window.dispatchEvent(
        new MessageEvent("message", { data: { omniplex: "nav", url: "/p/evil/x.html", title: "" }, source: window }),
      );
    });
    expect(screen.queryByLabelText(/console errors/)).toBeNull();
    expect(screen.getByTestId("url-pill").textContent).toBe("/index.html");
  });

  it("reloads onto a fresh token once the old one has lapsed, keeping the page", async () => {
    const refresh = vi.fn().mockResolvedValue({ url: "/p/tok2/index.html", expiresAt: Date.now() + 3_600_000 });
    mount(Date.now() - 1, refresh);
    fromFrame({ omniplex: "nav", url: "/p/tok1/docs/b.html", title: "" });
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(frame().getAttribute("src")).toBe("/p/tok2/docs/b.html"));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("reloads without asking for a token while the old one is good", async () => {
    const refresh = mount();
    fromFrame({ omniplex: "nav", url: "/p/tok1/b.html", title: "" });
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(frame().getAttribute("src")).toBe("/p/tok1/b.html"));
    expect(refresh).not.toHaveBeenCalled();
  });
});

describe("frameFit", () => {
  it("fills the stage at desktop width and shrinks a wider device to fit", () => {
    expect(frameFit(undefined, 500)).toEqual({ scale: 1 });
    expect(frameFit(390, 800)).toEqual({ width: 390, scale: 1 });
    expect(frameFit(768, 384)).toEqual({ width: 768, scale: 0.5 });
  });
});
