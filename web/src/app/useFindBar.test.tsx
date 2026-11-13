// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { useFindBar } from "./useFindBar";

function mount(activeId: string | null = "a") {
  return renderHook(({ id }) => useFindBar(id), { initialProps: { id: activeId } });
}

describe("useFindBar", () => {
  it("opens on show, bumping the nonce on every ask, and closes", () => {
    const { result } = mount();
    expect(result.current.open).toBe(false);

    act(() => result.current.show());
    expect(result.current.open).toBe(true);
    const first = result.current.nonce;

    act(() => result.current.show());
    expect(result.current.open).toBe(true);
    expect(result.current.nonce).toBeGreaterThan(first);

    act(() => result.current.close());
    expect(result.current.open).toBe(false);
  });

  it("closes on a switch to another thread, and stays closed on the way back", () => {
    const hook = mount("a");
    act(() => hook.result.current.show());

    hook.rerender({ id: "b" });
    expect(hook.result.current.open).toBe(false);

    hook.rerender({ id: "a" });
    expect(hook.result.current.open).toBe(false);

    act(() => hook.result.current.show());
    expect(hook.result.current.open).toBe(true);
  });

  it("keeps show and close stable across renders", () => {
    const hook = mount();
    const { show, close } = hook.result.current;
    act(() => show());
    hook.rerender({ id: "a" });
    expect(hook.result.current.show).toBe(show);
    expect(hook.result.current.close).toBe(close);
  });
});
