// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { ModelSettingsSection } from "./ModelSettingsSection";
import type { ModelSettingsSchema, ProviderInstanceMeta } from "~/protocol";

// jsdom has no layout, and cmdk/radix reach for both of these on open.
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

const schema: ModelSettingsSchema = {
  label: "Provider routing",
  description: "Pin this model to one provider.",
  placeholder: '{"only": ["amazon-bedrock"]}',
  prefix: "openrouter/",
};

const instance = {
  id: "pi",
  driver: "pi",
  displayName: "Pi",
  availability: { state: "ready" },
  models: [
    { id: "openrouter/anthropic/claude-sonnet-4", label: "Claude Sonnet 4" },
    // Not an OpenRouter model: the setting does not apply to it.
    { id: "anthropic/claude-opus-4", label: "Claude Opus 4" },
  ],
} as unknown as ProviderInstanceMeta;

function mount(stored: Record<string, string> = {}) {
  const command = vi.fn(async (name: string, args: any) => {
    if (name === "provider_model_settings") return { modelSettings: stored };
    if (name === "set_model_setting") {
      if (args.value === "") delete stored[args.modelId];
      else stored[args.modelId] = args.value;
      return { modelSettings: { ...stored } };
    }
    throw new Error(`unexpected command ${name}`);
  });
  render(
    <ModelSettingsSection
      wires={{ command, subscribe: () => () => {} }}
      instance={instance}
      schema={schema}
    />,
  );
  return command;
}

describe("per-model harness settings", () => {
  it("saves the pasted JSON against the chosen model", async () => {
    const command = mount();
    fireEvent.click(await screen.findByLabelText("Choose a model"));
    fireEvent.click(await screen.findByText("Claude Sonnet 4"));

    const box = await screen.findByLabelText("Provider routing JSON");
    fireEvent.change(box, { target: { value: '{"only":["amazon-bedrock"]}' } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("set_model_setting", {
        instanceId: "pi",
        modelId: "openrouter/anthropic/claude-sonnet-4",
        value: '{"only":["amazon-bedrock"]}',
      }),
    );
  });

  // The models the setting cannot apply to must not be offered, or a user
  // pastes routing against a model the harness will never route.
  it("only offers models the setting applies to", async () => {
    mount();
    fireEvent.click(await screen.findByLabelText("Choose a model"));
    await screen.findByText("Claude Sonnet 4");
    expect(screen.queryByText("Claude Opus 4")).toBeNull();
  });

  it("shows what is already stored and can clear it", async () => {
    const command = mount({ "openrouter/anthropic/claude-sonnet-4": '{"only":["anthropic"]}' });
    // Stored values are listed without having to hunt for the model again.
    fireEvent.click(await screen.findByText("anthropic/claude-sonnet-4"));
    const box = (await screen.findByLabelText("Provider routing JSON")) as HTMLTextAreaElement;
    expect(box.value).toBe('{"only":["anthropic"]}');

    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("set_model_setting", {
        instanceId: "pi",
        modelId: "openrouter/anthropic/claude-sonnet-4",
        value: "",
      }),
    );
  });

  it("says so when no model takes the setting", async () => {
    render(
      <ModelSettingsSection
        wires={{ command: async () => ({ modelSettings: {} }), subscribe: () => () => {} }}
        instance={{ ...instance, models: [] } as unknown as ProviderInstanceMeta}
        schema={schema}
      />,
    );
    expect(await screen.findByText(/No models here take this setting/)).toBeTruthy();
  });
});
