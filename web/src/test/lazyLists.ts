import { commandMenuList, modelList } from "~/components/lazyLists";

/**
 * Loads the composer's on-demand lists, so a test can open a picker and find
 * its rows on the same render, as a user does once the idle prefetch is in.
 * Use as `beforeAll(preloadLazyLists)`: it resolves to nothing, because
 * vitest runs a function returned from `beforeAll` as teardown.
 */
export async function preloadLazyLists() {
  await Promise.all([commandMenuList.load(), modelList.load()]);
}
