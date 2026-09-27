// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { SessionSummaryPanel } from "./SessionSummary";
import type { SessionSummary, UserConfig } from "~/protocol";

const summary: SessionSummary = {
  text: "**Request** — fix the login redirect.\n\n**Follow-ups** — None.",
  harness: "Claude",
  model: "haiku",
  seq: 42,
  generatedAt: 1_700_000_000_000,
};

const config: UserConfig = { version: 1, summaryPrompt: "Summarise it." };

function panel(props: Partial<React.ComponentProps<typeof SessionSummaryPanel>> = {}) {
  return (
    <SessionSummaryPanel
      summary={summary}
      loading={false}
      error={null}
      stale={false}
      userConfig={config}
      onRegenerate={vi.fn()}
      onSavePrompt={vi.fn().mockResolvedValue(undefined)}
      onClose={vi.fn()}
      {...props}
    />
  );
}

describe("the session summary panel", () => {
  it("renders the summary as markdown and says what wrote it", () => {
    render(panel());

    expect(screen.getByText("Request")).toBeTruthy(); // ** ** became <strong>
    expect(screen.getByText(/Claude · haiku/)).toBeTruthy();
  });

  // The wait is a cold harness start, so silence would read as a broken button.
  it("shows progress instead of a stale answer while it works", () => {
    render(panel({ loading: true, summary: null }));

    expect(screen.getByText("Reading the transcript…")).toBeTruthy();
  });

  it("reports a failure in place rather than an empty panel", () => {
    render(panel({ summary: null, error: "Claude Code is not installed on this machine" }));

    expect(screen.getByText(/not installed on this machine/)).toBeTruthy();
  });

  // A summary written before the last few turns is still useful; claiming to
  // be current is what would mislead.
  it("marks a summary the session has moved past", () => {
    render(panel({ stale: true }));

    expect(screen.getByText(/moved on since this was written/)).toBeTruthy();
  });

  it("does not mark a summary that is up to date", () => {
    render(panel());

    expect(screen.queryByText(/moved on since this was written/)).toBeNull();
  });

  it("asks again on demand", () => {
    const onRegenerate = vi.fn();
    render(panel({ onRegenerate }));

    fireEvent.click(screen.getByText("Summarise again"));
    expect(onRegenerate).toHaveBeenCalled();
  });

  describe("the prompt editor", () => {
    it("stays out of the way until it is opened", () => {
      render(panel());

      expect(screen.queryByLabelText("Summarisation prompt")).toBeNull();
      expect(screen.getByText("Summarisation prompt")).toBeTruthy(); // the trigger
    });

    it("saves an edited prompt and re-runs the summary", async () => {
      const onSavePrompt = vi.fn().mockResolvedValue(undefined);
      const onRegenerate = vi.fn();
      render(panel({ onSavePrompt, onRegenerate }));

      fireEvent.click(screen.getByText("Summarisation prompt"));
      const box = screen.getByLabelText("Summarisation prompt");
      fireEvent.change(box, { target: { value: "Be terse." } });
      fireEvent.click(screen.getByText("Save and summarise again"));

      await waitFor(() => expect(onSavePrompt).toHaveBeenCalledWith("Be terse."));
      await waitFor(() => expect(onRegenerate).toHaveBeenCalled());
    });

    it("cannot save a prompt nobody edited", () => {
      render(panel());

      fireEvent.click(screen.getByText("Summarisation prompt"));
      expect(screen.getByText("Save and summarise again").closest("button")?.disabled).toBe(true);
    });

    // A save that fails must not look like one that worked: reporting the
    // failure and not re-summarising is the whole difference.
    it("reports a save that failed instead of summarising again", async () => {
      const onSavePrompt = vi.fn().mockRejectedValue(new Error("settings are still loading"));
      const onRegenerate = vi.fn();
      render(panel({ onSavePrompt, onRegenerate }));

      fireEvent.click(screen.getByText("Summarisation prompt"));
      fireEvent.change(screen.getByLabelText("Summarisation prompt"), { target: { value: "Be terse." } });
      fireEvent.click(screen.getByText("Save and summarise again"));

      await waitFor(() => expect(screen.getByText(/settings are still loading/)).toBeTruthy());
      expect(onRegenerate).not.toHaveBeenCalled();
    });

    // Clearing the field is the reset: the server substitutes its own default
    // for an empty value, so the default text lives in exactly one place.
    it("resets by saving an empty prompt", async () => {
      const onSavePrompt = vi.fn().mockResolvedValue(undefined);
      render(panel({ onSavePrompt }));

      fireEvent.click(screen.getByText("Summarisation prompt"));
      fireEvent.click(screen.getByText("Reset to default"));

      await waitFor(() => expect(onSavePrompt).toHaveBeenCalledWith(""));
    });
  });
});
