import { describe, expect, it } from "vitest";

import type { ProviderConfigField } from "~/protocol";

import { buildInstanceSpec, deriveInstanceId, initialFieldValues } from "./providerSpec";

describe("deriveInstanceId", () => {
  it("slugs the display name", () => {
    expect(deriveInstanceId("Work Account", [])).toBe("work-account");
  });

  it("folds accents instead of dropping the letters", () => {
    expect(deriveInstanceId("Café Ünïverse", [])).toBe("cafe-universe");
  });

  it("collapses runs of punctuation and trims dashes", () => {
    expect(deriveInstanceId("  --Pi (beta)!  ", [])).toBe("pi-beta");
  });

  it("falls back when nothing slugs", () => {
    expect(deriveInstanceId("!!!", [])).toBe("instance");
    expect(deriveInstanceId("", [])).toBe("instance");
  });

  it("suffixes past taken ids instead of colliding", () => {
    expect(deriveInstanceId("Work", ["work"])).toBe("work-2");
    expect(deriveInstanceId("Work", ["work", "work-2"])).toBe("work-3");
  });
});

describe("buildInstanceSpec", () => {
  const secret: ProviderConfigField = { env: "PI_API_KEY", label: "API key", kind: "secret" };
  const path: ProviderConfigField = { env: "PI_HOME", label: "Home", kind: "path" };

  const base = {
    id: "pi-work",
    driver: "pi",
    displayName: "Pi (work)",
    enabled: true,
  };

  it("sends a typed secret with its value, marked sensitive", () => {
    const spec = buildInstanceSpec({
      ...base,
      fields: [secret],
      values: { PI_API_KEY: "sk-123" },
    });
    expect(spec.env).toEqual([{ name: "PI_API_KEY", value: "sk-123", sensitive: true }]);
  });

  it("keeps a stored secret with a valueless marker when not retyped", () => {
    const spec = buildInstanceSpec({
      ...base,
      fields: [secret],
      values: { PI_API_KEY: "" },
      stored: [{ name: "PI_API_KEY", sensitive: true }],
    });
    expect(spec.env).toEqual([{ name: "PI_API_KEY", sensitive: true }]);
  });

  it("omits an untouched secret nothing is stored for", () => {
    const spec = buildInstanceSpec({
      ...base,
      fields: [secret],
      values: {},
    });
    expect(spec.env).toEqual([]);
  });

  it("includes non-secret fields only when they hold text", () => {
    const spec = buildInstanceSpec({
      ...base,
      fields: [path],
      values: { PI_HOME: "  /srv/pi  " },
    });
    expect(spec.env).toEqual([{ name: "PI_HOME", value: "/srv/pi" }]);

    // The form prefills from the echoed env, so a blank field is the user
    // clearing it — the stored value must not sneak back in.
    const cleared = buildInstanceSpec({
      ...base,
      fields: [path],
      values: { PI_HOME: "" },
      stored: [{ name: "PI_HOME", value: "/old/home" }],
    });
    expect(cleared.env).toEqual([]);
  });

  it("carries stored vars the schema does not know, so hand-authored config survives", () => {
    const spec = buildInstanceSpec({
      ...base,
      fields: [secret],
      values: {},
      stored: [
        { name: "PI_API_KEY", sensitive: true },
        { name: "HTTPS_PROXY", value: "http://proxy:8080" },
        { name: "EXTRA_TOKEN", sensitive: true },
      ],
    });
    expect(spec.env).toEqual([
      { name: "PI_API_KEY", sensitive: true },
      { name: "HTTPS_PROXY", value: "http://proxy:8080" },
      { name: "EXTRA_TOKEN", sensitive: true },
    ]);
  });

  it("prefers the form's edit over the echoed value", () => {
    const spec = buildInstanceSpec({
      ...base,
      fields: [path],
      values: { PI_HOME: "/new/home" },
      stored: [{ name: "PI_HOME", value: "/old/home" }],
    });
    expect(spec.env).toEqual([{ name: "PI_HOME", value: "/new/home" }]);
  });

  it("trims the display name and falls back to the id", () => {
    const spec = buildInstanceSpec({
      ...base,
      displayName: "   ",
      fields: [],
      values: {},
    });
    expect(spec.displayName).toBe("pi-work");
    expect(spec.driver).toBe("pi");
    expect(spec.enabled).toBe(true);
  });
});

describe("initialFieldValues", () => {
  it("prefills plain fields from the echoed env, never secrets", () => {
    const fields: ProviderConfigField[] = [
      { env: "PI_HOME", label: "Home", kind: "path" },
      { env: "PI_API_KEY", label: "API key", kind: "secret" },
    ];
    const values = initialFieldValues(fields, [
      { name: "PI_HOME", value: "/srv/pi" },
      { name: "PI_API_KEY", sensitive: true },
    ]);
    expect(values).toEqual({ PI_HOME: "/srv/pi" });
  });

  it("returns nothing for an instance with no echoed env", () => {
    expect(initialFieldValues([{ env: "PI_HOME", label: "Home", kind: "path" }], undefined)).toEqual({});
  });
});
