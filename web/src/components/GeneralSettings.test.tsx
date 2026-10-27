// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { forgetVoices } from "~/lib/voices";
import type { HarnessMeta, UserConfig, Voice } from "~/protocol";
import { render } from "~/test/harness";
import { GeneralSettings } from "./GeneralSettings";

beforeAll(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  Element.prototype.scrollIntoView = () => {};
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
});

afterEach(cleanup);

const voices: Voice[] = [
  { id: "v1", name: "Alice", category: "premade", labels: { accent: "british" } },
  { id: "v2", name: "Bryn", category: "cloned" },
];

beforeEach(() => {
  forgetVoices();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ voices }), { status: 200 })),
  );
});
afterEach(() => vi.unstubAllGlobals());

const ready = { state: "ready" } as const;
const harnesses = ["claude", "codex"].map((id) => ({
  id,
  name: id,
  availability: ready,
  permissionModes: [],
  instances: [
    {
      id,
      driver: id,
      displayName: id,
      enabled: true,
      availability: ready,
      models: [
        { id: `${id}-basic`, label: `${id} Basic`, default: true },
        { id: `${id}-advanced`, label: `${id} Advanced` },
      ],
    },
  ],
})) as unknown as HarnessMeta[];

function open(
  userConfig: UserConfig,
  onSave = vi.fn(async (_cfg: UserConfig) => {}),
  { hasElevenLabsKey = false, onSetElevenLabsKey = vi.fn(async (_key: string) => {}) } = {},
) {
  const view = render(
    <GeneralSettings
      userConfig={userConfig}
      harnesses={harnesses}
      onSave={onSave}
      hasElevenLabsKey={hasElevenLabsKey}
      onSetElevenLabsKey={onSetElevenLabsKey}
    />,
  );
  return { onSave, onSetElevenLabsKey, view };
}

async function pickLevel(name: string | RegExp) {
  const trigger = screen.getByLabelText("Default permissions");
  trigger.focus();
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  fireEvent.click(await screen.findByRole("option", { name }));
}

async function pickModel(label: string) {
  fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
  fireEvent.change(screen.getByPlaceholderText(/Search models and accounts/), {
    target: { value: label },
  });
  fireEvent.click(await screen.findByRole("option", { name: new RegExp(label) }));
}

const save = () => fireEvent.click(screen.getByRole("button", { name: "Save" }));

describe("GeneralSettings", () => {
  it("saves what was changed and keeps what this screen does not show", async () => {
    const { onSave } = open({ version: 1, suggestIssues: true });
    fireEvent.change(screen.getByLabelText("Projects folder"), { target: { value: "~/work" } });
    await pickLevel("Do everything");
    fireEvent.change(screen.getByLabelText("Branch names from issues"), {
      target: { value: "fix/{number}" },
    });
    save();
    await screen.findByRole("button", { name: "Saved" });
    expect(onSave).toHaveBeenCalledWith({
      version: 1,
      suggestIssues: true,
      projectsDir: "~/work",
      defaultLevel: "all",
      branchFormat: "fix/{number}",
    });
  });

  it("reads an unset pin default as on, and saves turning it off", async () => {
    const { onSave } = open({ version: 1 });
    const pin = screen.getByRole("switch", { name: "Pin new threads" });
    expect(pin.getAttribute("aria-checked")).toBe("true");
    fireEvent.click(pin);
    save();
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].pinNewThreads).toBe(false);
  });

  it("goes back to the harness's own permissions", async () => {
    const { onSave } = open({ version: 1, defaultLevel: "edits" });
    await pickLevel(/own default/);
    save();
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].defaultLevel).toBe("");
  });

  it("picks a default model and clears it again", async () => {
    const { onSave } = open({ version: 1 });
    expect(screen.queryByRole("button", { name: "Clear" })).toBeNull();
    await pickModel("codex Advanced");
    fireEvent.click(await screen.findByRole("button", { name: "Clear" }));
    expect(screen.queryByRole("button", { name: "Clear" })).toBeNull();
    await pickModel("claude Advanced");
    save();
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toMatchObject({
      defaultInstance: "claude",
      defaultModel: "claude-advanced",
    });
  });

  it("shows why the server refused and stays open to fix it", async () => {
    const onSave = vi.fn(async () => {
      throw new Error("projects folder must be an absolute path or start with ~");
    });
    open({ version: 1 }, onSave);
    fireEvent.change(screen.getByLabelText("Projects folder"), { target: { value: "work" } });
    save();
    expect(await screen.findByText(/must be an absolute path/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("will not save a broken branch template until it is fixed", async () => {
    const { onSave } = open({ version: 1 });
    const branch = screen.getByLabelText("Branch names from issues");
    const saveButton = () => screen.getByRole("button", { name: "Save" }) as HTMLButtonElement;

    fireEvent.change(branch, { target: { value: "fix/{foo}-{number}" } });
    expect(saveButton().disabled).toBe(true);
    save();
    expect(onSave).not.toHaveBeenCalled();

    fireEvent.change(branch, { target: { value: "fix/{title}-{number}" } });
    expect(saveButton().disabled).toBe(false);
    save();
    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith({ version: 1, branchFormat: "fix/{title}-{number}" }),
    );
  });

  it("previews the branch a template makes, and says when it is broken", () => {
    open({ version: 1, branchFormat: "fix/{number}-{title}" });
    expect(screen.getByText(/#482 → fix\/482-token-refresh-500s-after-24h/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Branch names from issues"), {
      target: { value: "fix/{nmber}" },
    });
    expect(screen.getByText(/#482 → unknown placeholder \{nmber\}/)).toBeTruthy();
  });
});

describe("read aloud", () => {
  it("stores a key, then offers to remove it rather than showing it", async () => {
    const onSetElevenLabsKey = vi.fn(async (_key: string) => {});
    const { view } = open({ version: 1 }, undefined, { onSetElevenLabsKey });
    fireEvent.change(screen.getByLabelText("ElevenLabs API key"), {
      target: { value: "  sk_secret  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save key" }));
    await waitFor(() => expect(onSetElevenLabsKey).toHaveBeenCalledWith("sk_secret"));

    view.rerender(
      <GeneralSettings
        userConfig={{ version: 1 }}
        harnesses={harnesses}
        onSave={vi.fn()}
        hasElevenLabsKey
        onSetElevenLabsKey={onSetElevenLabsKey}
      />,
    );
    expect(screen.queryByDisplayValue(/sk_secret/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(onSetElevenLabsKey).toHaveBeenLastCalledWith(""));
  });

  it("shows why a key was refused and keeps it to fix", async () => {
    const onSetElevenLabsKey = vi.fn(async () => {
      throw new Error("ElevenLabs rejected that key: Invalid API key");
    });
    open({ version: 1 }, undefined, { onSetElevenLabsKey });
    fireEvent.change(screen.getByLabelText("ElevenLabs API key"), { target: { value: "sk_bad" } });
    fireEvent.click(screen.getByRole("button", { name: "Save key" }));
    expect(await screen.findByText(/Invalid API key/)).toBeTruthy();
    expect(screen.getByDisplayValue("sk_bad")).toBeTruthy();
  });

  it("has no voice to pick, and fetches none, without a key", () => {
    open({ version: 1, voice: "v1" });
    expect(screen.queryByRole("combobox", { name: "Default voice" })).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("sends the voice back with every save, so saving something else keeps it", async () => {
    const { onSave } = open({ version: 1, voice: "v1" }, undefined, { hasElevenLabsKey: true });
    fireEvent.change(screen.getByLabelText("Projects folder"), { target: { value: "~/work" } });
    save();
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toMatchObject({ voice: "v1", projectsDir: "~/work" });
  });

  it("saves the voice picked", async () => {
    const { onSave } = open({ version: 1, voice: "v1" }, undefined, { hasElevenLabsKey: true });
    const picker = await screen.findByRole("combobox", { name: "Default voice" });
    await waitFor(() => expect((picker as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(picker);
    fireEvent.click(await screen.findByRole("option", { name: /Bryn/ }));
    save();
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].voice).toBe("v2");
  });

  async function searchVoices(text: string) {
    const picker = await screen.findByRole("combobox", { name: "Default voice" });
    await waitFor(() => expect((picker as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(picker);
    const search = await screen.findByPlaceholderText(/Search voices/);
    fireEvent.change(search, { target: { value: text } });
  }

  it("saves a pasted voice ID the list doesn't have", async () => {
    const { onSave } = open({ version: 1, voice: "v1" }, undefined, { hasElevenLabsKey: true });
    await searchVoices("  pAsTeD123  ");
    fireEvent.click(await screen.findByRole("option", { name: /Use voice ID pAsTeD123/ }));
    // Not in the list, so it shows by ID.
    const picker = screen.getByRole("combobox", { name: "Default voice" });
    expect(picker.textContent).toContain("pAsTeD123");
    save();
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].voice).toBe("pAsTeD123");
  });

  it("offers no pasted ID for a listed voice's ID, or for a search with spaces", async () => {
    open({ version: 1 }, undefined, { hasElevenLabsKey: true });
    await searchVoices("v2");
    expect(await screen.findByRole("option", { name: /Bryn/ })).toBeTruthy();
    expect(screen.queryByRole("option", { name: /Use voice ID/ })).toBeNull();
    const search = screen.getByPlaceholderText(/Search voices/);
    fireEvent.change(search, { target: { value: "no such voice" } });
    expect(await screen.findByText("No voice matches that.")).toBeTruthy();
    expect(screen.queryByRole("option", { name: /Use voice ID/ })).toBeNull();
  });

  it("takes a typed voice ID when the list won't load", async () => {
    const down = JSON.stringify({ error: "ElevenLabs is down" });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(down, { status: 502 })));
    const { onSave } = open({ version: 1, voice: "v1" }, undefined, { hasElevenLabsKey: true });
    expect(await screen.findByText("ElevenLabs is down")).toBeTruthy();
    const input = screen.getByLabelText("Default voice") as HTMLInputElement;
    expect(input.value).toBe("v1");
    fireEvent.change(input, { target: { value: " typed42 " } });
    fireEvent.blur(input);
    save();
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].voice).toBe("typed42");
  });
});
