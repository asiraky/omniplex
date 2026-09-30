// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import type { HarnessMeta, UserConfig } from "~/protocol";
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

function open(userConfig: UserConfig, onSave = vi.fn(async (_cfg: UserConfig) => {})) {
  render(<GeneralSettings userConfig={userConfig} harnesses={harnesses} onSave={onSave} />);
  return { onSave };
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
