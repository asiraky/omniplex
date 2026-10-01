import { useId, useState } from "react";

import { DetailHeading, ErrorLine, type PageCommand } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Label } from "~/components/ui/label";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import {
  emptyServerForm,
  formFromDraft,
  harnessesFor,
  serverSaveArgs,
  type ServerForm,
} from "~/lib/connections";
import { errorText } from "~/lib/utils";
import type { McpDraft, McpHarness, McpServer } from "~/protocol";

import { AgentSwitches, ServerFields } from "./parts";

/**
 * Add a server: paste what its docs give you, check what was read out of it,
 * save. Or skip the paste and fill the form in by hand. Keyed per opening by
 * the caller, so each one starts at the paste box.
 */
export function AddServerSheet({
  open,
  onOpenChange,
  command,
  harnesses,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  command: PageCommand;
  harnesses: McpHarness[];
  onSaved: (s: McpServer) => void;
}) {
  const formId = useId();
  const pasteId = useId();
  const [text, setText] = useState("");
  const [form, setForm] = useState<ServerForm | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const close = () => {
    if (!busy) onOpenChange(false);
  };

  const parse = async () => {
    if (!text.trim() || busy) return;
    setBusy(true);
    setError("");
    try {
      const res = await command<{ draft?: McpDraft }>("parse_mcp_server", { text });
      if (!res?.draft) throw new Error("Nothing in that looked like a server.");
      setForm(formFromDraft(res.draft));
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!form || busy) return;
    const built = serverSaveArgs(form);
    if ("error" in built) {
      setError(built.error);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const res = await command<{ server?: McpServer }>("save_mcp_server", built.args);
      if (!res?.server) throw new Error("The server did not answer with the saved server.");
      onSaved(res.server);
      onOpenChange(false);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  let body;
  let footer;
  if (!form) {
    body = (
      <form
        id={formId}
        className="scroll-thin -mx-1 min-h-0 space-y-2 overflow-y-auto px-1"
        onSubmit={(e) => {
          e.preventDefault();
          void parse();
        }}
      >
        <Label htmlFor={pasteId} className="leading-snug font-normal">
          Paste a URL, a claude mcp add command, or the JSON from the server's docs
        </Label>
        <Textarea
          id={pasteId}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="https://mcp.example.com/mcp"
          rows={3}
          autoComplete="off"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          className="max-h-60 min-h-20 font-mono break-all"
          autoFocus
        />
        {error && <ErrorLine message={error} />}
        <button
          type="button"
          onClick={() => {
            setError("");
            setForm(emptyServerForm());
          }}
          className="text-primary focus-visible:ring-ring -mx-1 flex min-h-11 items-center rounded-md px-1 text-[13px] underline-offset-2 outline-none hover:underline focus-visible:ring-2 md:min-h-8"
        >
          or fill one in by hand
        </button>
      </form>
    );
    footer = (
      <>
        <Button variant="outline" onClick={close} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" form={formId} disabled={busy || !text.trim()}>
          {busy && <Spinner className="size-3.5" />}
          Continue
        </Button>
      </>
    );
  } else {
    body = (
      <form
        id={formId}
        className="scroll-thin -mx-1 min-h-0 space-y-5 overflow-y-auto px-1"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <ServerFields form={form} onChange={(patch) => setForm((f) => f && { ...f, ...patch })} />
        <section aria-label="Agents that get it" className="space-y-1.5">
          <DetailHeading>Agents that get it</DetailHeading>
          <AgentSwitches
            harnesses={harnessesFor(harnesses, form.kind)}
            off={form.off}
            onChange={(off) => setForm((f) => f && { ...f, off })}
          />
        </section>
        {error && <ErrorLine message={error} />}
      </form>
    );
    footer = (
      <>
        <Button
          variant="outline"
          onClick={() => {
            setError("");
            setForm(null);
          }}
          disabled={busy}
        >
          Back
        </Button>
        <Button type="submit" form={formId} disabled={busy}>
          {busy && <Spinner className="size-3.5" />}
          Add server
        </Button>
      </>
    );
  }

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent
        fullscreenOnMobile
        aria-describedby={undefined}
        className="max-md:grid-rows-[auto_minmax(0,1fr)_auto] max-md:pt-[calc(1.5rem+env(safe-area-inset-top))] md:max-h-[90dvh] md:max-w-xl md:grid-rows-[auto_minmax(0,1fr)_auto]"
      >
        <DialogHeader>
          <DialogTitle className="text-[15px]">Add an MCP server</DialogTitle>
        </DialogHeader>
        {body}
        <DialogFooter>{footer}</DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
