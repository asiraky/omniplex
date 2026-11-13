// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { elicitationDrafts } from "~/lib/elicitationDraft";
import { ElicitationPrompt } from "./ElicitationPrompt";
import type { PendingElicitation } from "~/protocol";

afterEach(() => {
  vi.useRealTimers();
});

// Drafts are module-level and keyed by request, so every test gets its own id.
let seq = 0;
function request(properties: Record<string, unknown>): PendingElicitation {
  return {
    requestId: `r-${++seq}`,
    prompt: "The assistant needs your input to continue.",
    schema: { type: "object", properties },
  } as PendingElicitation;
}

// The shape the Claude AskUserQuestion bridge produces: an enum field carrying
// the x- hints for multi-select, a free-text escape, and per-option rationale.
const features = {
  type: "string",
  title: "Which features?",
  enum: ["Caching", "Metrics", "Tracing"],
  "x-multiSelect": true,
  "x-allowOther": true,
  "x-optionDescriptions": { Caching: "faster reads" },
};
const color = { type: "string", title: "Which colour?", enum: ["Red", "Green"], "x-allowOther": true };
const name = { type: "string", title: "Your name?" };

function mount(req: PendingElicitation, onResolve = vi.fn()) {
  const view = render(<ElicitationPrompt threadId="t-1" request={req} onResolve={onResolve} />);
  return { ...view, onResolve };
}

const click = (text: string) => fireEvent.click(screen.getByText(text));
const press = (key: string, init: KeyboardEventInit = {}) => fireEvent.keyDown(window, { key, ...init });
const title = () => screen.getByText(/\?$/).textContent;

describe("stepping through questions", () => {
  it("shows one question at a time, Back keeps answers, Continue sends them all", () => {
    const { onResolve } = mount(request({ name, color }));
    expect(title()).toBe("Your name?");
    expect(screen.getByText("1 of 2")).toBeTruthy();
    expect(screen.queryByText("Back")).toBeNull();

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Ada" } });
    click("Next");
    expect(title()).toBe("Which colour?");
    expect(screen.getByText("2 of 2")).toBeTruthy();

    click("Back");
    expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe("Ada");
    click("Next");
    click("Green");
    expect(onResolve).not.toHaveBeenCalled();
    click("Continue");

    expect(onResolve).toHaveBeenCalledWith("accept", { name: "Ada", color: "Green" });
  });

  it("advances on Enter in a non-last step rather than submitting", () => {
    const { onResolve, container } = mount(request({ name, color }));
    fireEvent.submit(container.querySelector("form")!);
    expect(title()).toBe("Which colour?");
    expect(onResolve).not.toHaveBeenCalled();
  });

  it("declines from any step without answers", () => {
    const { onResolve } = mount(request({ name, color }));
    click("Next");
    click("Decline");
    expect(onResolve).toHaveBeenCalledWith("decline");
  });
});

describe("single-select", () => {
  it("renders radio rows and auto-advances after a pick", () => {
    vi.useFakeTimers();
    mount(request({ color, name }));
    expect(screen.getAllByRole("radio")).toHaveLength(3);
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);

    click("Red");
    expect(screen.getByRole("radio", { name: /Red/ }).getAttribute("aria-checked")).toBe("true");
    act(() => void vi.advanceTimersByTime(179));
    expect(title()).toBe("Which colour?");
    act(() => void vi.advanceTimersByTime(1));
    expect(title()).toBe("Your name?");
  });

  it("does not advance when the human moves first", () => {
    vi.useFakeTimers();
    mount(request({ color, name, other: { ...name, title: "Third?" } }));
    click("Red");
    click("Next");
    act(() => void vi.advanceTimersByTime(500));
    expect(title()).toBe("Your name?");
  });

  it("sends trimmed Other text, and choosing Other does not advance", () => {
    vi.useFakeTimers();
    const { onResolve } = mount(request({ color, name }));
    click("Red");
    click("Other");
    act(() => void vi.advanceTimersByTime(500));
    expect(title()).toBe("Which colour?");
    expect(screen.getByRole("radio", { name: /Red/ }).getAttribute("aria-checked")).toBe("false");

    fireEvent.change(screen.getByLabelText("Other answer"), { target: { value: "  Teal " } });
    click("Next");
    click("Continue");
    expect(onResolve).toHaveBeenCalledWith("accept", { color: "Teal", name: "" });
  });
});

describe("multi-select", () => {
  it("collects several checkbox answers as an array, toggling off", () => {
    const { onResolve } = mount(request({ features }));
    expect(screen.queryAllByRole("radio")).toHaveLength(0);
    click("Caching");
    click("Tracing");
    click("Metrics");
    click("Metrics");
    click("Continue");
    expect(onResolve).toHaveBeenCalledWith("accept", { features: ["Caching", "Tracing"] });
  });

  it("appends a trimmed free-text Other answer last", () => {
    const { onResolve } = mount(request({ features }));
    click("Other");
    click("Caching");
    fireEvent.change(screen.getByLabelText("Other answer"), { target: { value: " Batching " } });
    click("Continue");
    expect(onResolve).toHaveBeenCalledWith("accept", { features: ["Caching", "Batching"] });
  });

  it("shows each option's description", () => {
    mount(request({ features }));
    expect(screen.getByRole("checkbox", { name: /Caching/ }).textContent).toContain("faster reads");
  });
});

describe("digit keys", () => {
  it("pick option N, and the one after the last picks Other", () => {
    const { onResolve } = mount(request({ features }));
    press("3");
    press("4");
    expect(screen.getByLabelText("Other answer")).toBeTruthy();
    press("9"); // no such row
    click("Continue");
    expect(onResolve).toHaveBeenCalledWith("accept", { features: ["Tracing"] });
  });

  it("answer two questions when typed faster than the auto-advance", () => {
    vi.useFakeTimers();
    const { onResolve } = mount(request({ color, shape: { ...color, title: "Which shape?", enum: ["Round", "Square"] } }));
    press("1");
    press("2");
    act(() => void vi.runAllTimers());
    click("Continue");
    expect(onResolve).toHaveBeenCalledWith("accept", { color: "Red", shape: "Square" });
  });

  it("reach the rows from an empty composer, and type into one with text in it", () => {
    mount(request({ features }));
    const composer = document.createElement("textarea");
    composer.setAttribute("data-composer-input", "");
    document.body.append(composer);
    composer.focus();

    press("1");
    composer.value = "hello";
    press("2");
    composer.remove();

    expect(screen.getByRole("checkbox", { name: /Caching/ }).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("checkbox", { name: /Metrics/ }).getAttribute("aria-checked")).toBe("false");
  });

  it.each([
    ["a modifier is held", () => press("1", { ctrlKey: true })],
    ["focus is in a text box", () => {
      const input = document.createElement("input");
      document.body.append(input);
      input.focus();
      press("1");
      input.remove();
    }],
    ["a dialog is open", () => {
      const dialog = document.createElement("div");
      dialog.setAttribute("role", "dialog");
      document.body.append(dialog);
      press("1");
      dialog.remove();
    }],
  ])("are ignored when %s", (_, setup) => {
    mount(request({ features }));
    setup();
    expect(screen.getByRole("checkbox", { name: /Caching/ }).getAttribute("aria-checked")).toBe("false");
  });
});

describe("drafts", () => {
  it("survive a remount, including after an answer that has not landed", () => {
    const req = request({ name, color });
    const first = mount(req);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Ada" } });
    click("Next");
    click("Green");
    click("Continue"); // the request is still pending: it may not have landed
    first.unmount();

    const second = mount(req);
    expect(title()).toBe("Which colour?");
    expect(screen.getByRole("radio", { name: /Green/ }).getAttribute("aria-checked")).toBe("true");
    click("Continue");
    expect(second.onResolve).toHaveBeenCalledWith("accept", { name: "Ada", color: "Green" });
  });

  it("start over once the thread stops listing the request", () => {
    const req = request({ name, color });
    const first = mount(req);
    click("Next");
    first.unmount();

    elicitationDrafts.keepOnly("t-1", []);
    mount(req);
    expect(title()).toBe("Your name?");
  });
});
