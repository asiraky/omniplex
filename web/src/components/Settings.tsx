import { useMemo, useState } from "react";

import { ModelPicker } from "~/components/ModelPicker";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { Separator } from "~/components/ui/separator";
import { Textarea } from "~/components/ui/textarea";
import { pickerInstances } from "~/lib/models";
import { LEVELS } from "~/lib/permissions";
import { cn } from "~/lib/utils";
import type { HarnessMeta, Issue, PermissionLevel, UserConfig } from "~/protocol";
import { makeFormatter } from "./WorkspacePicker";

// A stand-in issue, so the preview shows a real answer rather than describing one.
const sampleIssue: Issue = {
  number: 482,
  title: "Token refresh 500s after 24h",
  url: "",
  labels: [{ name: "bug" }],
};

// Radix rejects "" as a Select value.
const UNSET = "__omniplex_unset__";

function BranchFormatField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const preview = useMemo(() => {
    const { format, error } = makeFormatter(value);
    if (error) return { text: error, bad: true };
    const out = format(sampleIssue);
    return out
      ? { text: out, bad: false }
      : { text: "function returned nothing for the sample issue", bad: true };
  }, [value]);

  return (
    <div className="space-y-1.5">
      <Label htmlFor="settings-branch-format">Branch names from issues</Label>
      <p className="text-muted-foreground text-[11px]">
        A JavaScript function, issue in and branch name out. It names the copies started from your
        open GitHub issues.
      </p>
      <Textarea
        id="settings-branch-format"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
        rows={4}
        className="scroll-thin font-mono md:text-[11px]"
      />
      <p
        className={cn(
          "font-mono text-[11px] break-all",
          preview.bad ? "text-attention-foreground" : "text-muted-foreground",
        )}
      >
        #{sampleIssue.number} → {preview.text}
      </p>
    </div>
  );
}

/**
 * The operator's own settings, kept in ~/.omniplex/config.json on the machine
 * running Omniplex. A project's own choices win over the defaults here; these
 * only decide what a project's first thread starts on.
 */
export function Settings({
  userConfig,
  harnesses,
  onSave,
  onClose,
}: {
  userConfig: UserConfig;
  harnesses: HarnessMeta[];
  onSave: (cfg: UserConfig) => Promise<void>;
  onClose: () => void;
}) {
  const [cfg, setCfg] = useState<UserConfig>(userConfig);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const patch = (p: Partial<UserConfig>) => setCfg((c) => ({ ...c, ...p }));

  const defaultInstance = pickerInstances(harnesses).find((i) => i.id === cfg.defaultInstance);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSave(cfg);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent
        fullscreenOnMobile
        className="flex max-h-[min(90dvh,44rem)] flex-col gap-0 p-0 md:max-w-lg"
      >
        <DialogHeader className="border-b px-6 py-4 pt-[calc(1rem+env(safe-area-inset-top))] pr-16 text-left md:pt-4 md:pr-6">
          <DialogTitle>Settings</DialogTitle>
          <DialogDescription>Yours, on every project. A project's own choices win.</DialogDescription>
        </DialogHeader>

        <div className="scroll-thin min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-5">
          <div className="space-y-1.5">
            <Label htmlFor="settings-projects-dir">Projects folder</Label>
            <Input
              id="settings-projects-dir"
              value={cfg.projectsDir ?? ""}
              onChange={(e) => patch({ projectsDir: e.target.value })}
              placeholder="~/Omniplex"
              spellCheck={false}
              className="font-mono md:text-[12px]"
            />
            <p className="text-muted-foreground text-[11px]">
              Where a new project gets its folder when you give it only a name.
            </p>
          </div>

          <Separator />

          <div className="space-y-1.5">
            <Label htmlFor="settings-default-model">Default model</Label>
            <div className="flex items-center gap-2">
              <ModelPicker
                id="settings-default-model"
                harnesses={harnesses}
                value={{
                  harness: defaultInstance?.driver ?? "",
                  instance: cfg.defaultInstance ?? "",
                  model: cfg.defaultModel ?? "",
                }}
                onChange={(next) => patch({ defaultInstance: next.instance, defaultModel: next.model })}
                className="min-w-0 flex-1"
              />
              {cfg.defaultInstance && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => patch({ defaultInstance: "", defaultModel: "" })}
                >
                  Clear
                </Button>
              )}
            </div>
            <p className="text-muted-foreground text-[11px]">
              {cfg.defaultInstance
                ? "What a project's first thread starts on."
                : "Not set. A project's first thread starts on the first account that is signed in."}
            </p>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="settings-default-level">Default permissions</Label>
            <Select
              value={cfg.defaultLevel || UNSET}
              onValueChange={(v) =>
                patch({ defaultLevel: v === UNSET ? "" : (v as PermissionLevel) })
              }
            >
              <SelectTrigger id="settings-default-level" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={UNSET}>The harness's own default</SelectItem>
                {LEVELS.map((l) => (
                  <SelectItem key={l.id} value={l.id}>
                    {l.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <Separator />

          <BranchFormatField
            value={cfg.branchFormat ?? ""}
            onChange={(v) => patch({ branchFormat: v })}
          />
        </div>

        {/* Outside the scroll, so a refusal is seen without scrolling for it. */}
        {error && (
          <Alert variant="destructive" className="mx-6 mb-3 w-auto">
            <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
          </Alert>
        )}

        <DialogFooter className="border-t px-6 py-4 pb-[calc(1rem+env(safe-area-inset-bottom))] md:pb-4">
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button disabled={busy} onClick={save}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
