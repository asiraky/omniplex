import { describe, expect, it } from "vitest";

import { emptyState } from "./apply";
import { ThreadCache } from "./threadCache";

const state = (id: string, seq = 1) => ({ ...emptyState(id), seq });

describe("ThreadCache", () => {
  it("evicts the least recently used entry past capacity", () => {
    const cache = new ThreadCache(2);
    cache.put(state("a"));
    cache.put(state("b"));
    cache.put(state("c"));
    expect(cache.get("a")).toBeUndefined();
    expect(cache.get("b")).toBeDefined();
    expect(cache.get("c")).toBeDefined();
  });

  it("counts a read as a use", () => {
    const cache = new ThreadCache(2);
    cache.put(state("a"));
    cache.put(state("b"));
    cache.get("a");
    cache.put(state("c"));
    expect(cache.get("b")).toBeUndefined();
    expect(cache.get("a")).toBeDefined();
  });

  it("replaces an entry put again, refreshing its place", () => {
    const cache = new ThreadCache(2);
    cache.put(state("a", 1));
    cache.put(state("b"));
    cache.put(state("a", 9));
    cache.put(state("c"));
    expect(cache.get("b")).toBeUndefined();
    expect(cache.get("a")?.seq).toBe(9);
  });
});
