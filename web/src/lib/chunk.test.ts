import { describe, expect, it, vi } from "vitest";

import { chunk } from "./chunk";

describe("chunk", () => {
  it("imports once and keeps what arrived", async () => {
    const importer = vi.fn(async () => "code");
    const c = chunk(importer);
    expect(c.get()).toBeUndefined();
    const [a, b] = await Promise.all([c.load(), c.load()]);
    expect([a, b, c.get()]).toEqual(["code", "code", "code"]);
    await c.load();
    expect(importer).toHaveBeenCalledTimes(1);
  });

  it("forgets a failed import, so the next load tries again", async () => {
    const importer = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce("code");
    const c = chunk(importer);
    expect(await c.load()).toBeUndefined();
    expect(c.get()).toBeUndefined();
    expect(await c.load()).toBe("code");
    expect(importer).toHaveBeenCalledTimes(2);
  });
});
