import { useEffect, useState } from "react";

import { ErrorLine, Loading } from "~/components/tools/parts";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "~/components/ui/select";
import type { AgentApi } from "~/lib/agentApi";
import { errorText } from "~/lib/utils";
import type { Project } from "~/protocol";
import { useLatest } from "~/useLatest";
import { BodyText } from "./parts";

// Controls the New agent form and the agent page both draw.

/** The fixed preamble Omniplex puts in front of an agent's prompt, fetched the first time it opens. */
export function PreambleDialog({
  api,
  open,
  onOpenChange,
}: {
  api: AgentApi;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const apiRef = useLatest(api);
  useEffect(() => {
    if (!open || text !== null) return;
    let live = true;
    apiRef.current.preamble().then(
      (t) => live && setText(t),
      (e: unknown) => live && setError(errorText(e)),
    );
    return () => {
      live = false;
    };
  }, [open, text, apiRef]);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[90dvh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Agent preamble</DialogTitle>
          <DialogDescription>
            Fixed text written by Omniplex. It goes between the Omniplex instructions and your prompt.
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <ErrorLine message={error} />
        ) : text === null ? (
          <Loading>Loading the preamble</Loading>
        ) : (
          <BodyText className="scroll-thin min-h-0 overflow-y-auto">{text}</BodyText>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** Radix's Select takes no empty value; this stands for the whole project. */
const WHOLE = "__whole";

/** Which folder of a project an agent works in; "" is the whole project. */
export function ScopeSelect({
  folders,
  value,
  onChange,
}: {
  folders: Project["folders"];
  value: string;
  onChange: (folderId: string) => void;
}) {
  return (
    <Select value={value || WHOLE} onValueChange={(v) => onChange(v === WHOLE ? "" : v)}>
      <SelectTrigger aria-label="Scope" className="h-11 w-full md:h-9">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={WHOLE}>Everything</SelectItem>
        {folders.map((f) => (
          <SelectItem key={f.id} value={f.id}>
            {f.path}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
