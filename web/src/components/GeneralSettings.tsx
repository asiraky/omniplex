import { useMemo, useState } from "react";

import { ModelPicker } from "~/components/ModelPicker";
import { SettingsPane } from "~/components/SettingsPane";
import { Button } from "~/components/ui/button";
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
import { branchTemplate, DEFAULT_BRANCH_TEMPLATE } from "~/lib/branchTemplate";
import { pickerInstances } from "~/lib/models";
import { LEVELS } from "~/lib/permissions";
import { cn } from "~/lib/utils";
import type { HarnessMeta, Issue, PermissionLevel, UserConfig } from "~/protocol";

// A stand-in issue, so the preview shows a real answer rather than describing one.
const sampleIssue: Issue = {
  number: 482,
  title: "Token refresh 500s after 24h",
  url: "",
  labels: [{ name: "bug" }],
};

// Radix rejects "" as a Select value.
const UNSET = "__omniplex_unset__";

type BranchPreview = { text: string; bad: boolean };

// What the template makes of the sample issue, or why it cannot be saved.
function branchPreview(value: string): BranchPreview {
  const { format, error } = branchTemplate(value);
  if (error) return { text: error, bad: true };
  const out = format(sampleIssue);
  return out
    ? { text: out, bad: false }
    : { text: "the template makes nothing for the sample issue", bad: true };
}

function BranchFormatField({
  value,
  preview,
  onChange,
}: {
  value: string;
  preview: BranchPreview;
  onChange: (v: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor="settings-branch-format">Branch names from issues</Label>
      <p className="text-muted-foreground text-[11px]">
        Names the copies started from your open GitHub issues. {"{number}"} is the issue number
        and {"{title}"} its title, lowercased with dashes. Leave it empty for the default.
      </p>
      <Input
        id="settings-branch-format"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={DEFAULT_BRANCH_TEMPLATE}
        spellCheck={false}
        className="font-mono md:text-[12px]"
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
export function GeneralSettings({
  userConfig,
  harnesses,
  onSave,
  onBack,
}: {
  userConfig: UserConfig;
  harnesses: HarnessMeta[];
  onSave: (cfg: UserConfig) => Promise<void>;
  onBack?: () => void;
}) {
  const [cfg, setCfg] = useState<UserConfig>(userConfig);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const patch = (p: Partial<UserConfig>) => {
    setSaved(false);
    setCfg((c) => ({ ...c, ...p }));
  };

  const branch = useMemo(() => branchPreview(cfg.branchFormat ?? ""), [cfg.branchFormat]);
  const defaultInstance = pickerInstances(harnesses).find((i) => i.id === cfg.defaultInstance);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSave(cfg);
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsPane
      title="General"
      description="Yours, on every project. A project's own choices win."
      onBack={onBack}
      error={error}
      footer={
        <Button disabled={busy || saved || branch.bad} onClick={save}>
          {busy ? "Saving…" : saved ? "Saved" : "Save"}
        </Button>
      }
    >
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
          onValueChange={(v) => patch({ defaultLevel: v === UNSET ? "" : (v as PermissionLevel) })}
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
        preview={branch}
        onChange={(v) => patch({ branchFormat: v })}
      />
    </SettingsPane>
  );
}
