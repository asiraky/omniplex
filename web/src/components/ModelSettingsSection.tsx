import { ChevronsUpDownIcon, ExternalLinkIcon } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import type { AuthWires } from "~/components/AuthFlowDialog";
import { Button } from "~/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "~/components/ui/command";
import { Label } from "~/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { Textarea } from "~/components/ui/textarea";
import type { ModelMeta, ModelSettingsSchema, ProviderInstanceMeta } from "~/protocol";

/** The part of a model id worth reading once the picker has already said which
    provider it belongs to: "openrouter/anthropic/claude" → "anthropic/claude". */
function shortId(id: string, prefix?: string) {
  return prefix && id.startsWith(prefix) ? id.slice(prefix.length) : id;
}

function takesSetting(id: string, schema: ModelSettingsSchema) {
  return !schema.prefix || id.startsWith(schema.prefix);
}

/**
 * A per-model setting the harness reads from its own config file — today, Pi's
 * OpenRouter provider routing. Omniplex does not interpret the value: it is
 * the harness's JSON, pasted from the harness's own docs, stored beside the
 * account it applies to so it does not have to be hand-edited into a file.
 *
 * Model catalogues run to hundreds of entries, so the surface is a picker plus
 * one box, not a box per model; the models that already have a value are
 * listed separately, because those are the ones a user comes back to change.
 */
export function ModelSettingsSection({
  wires,
  instance,
  schema,
}: {
  wires: AuthWires;
  instance: ProviderInstanceMeta;
  schema: ModelSettingsSchema;
}) {
  const [values, setValues] = useState<Record<string, string> | null>(null);
  const [selected, setSelected] = useState<string>("");
  const [draft, setDraft] = useState("");
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    setError(null);
    wires
      .command("provider_model_settings", { instanceId: instance.id })
      .then((r: { modelSettings?: Record<string, string> }) => setValues(r?.modelSettings ?? {}))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [wires, instance.id]);

  useEffect(() => {
    setValues(null);
    setSelected("");
    load();
  }, [load]);

  const models: ModelMeta[] = useMemo(
    () => (instance.models ?? []).filter((m) => takesSetting(m.id, schema)),
    [instance.models, schema],
  );
  // A model with a value stays listed even if the catalogue no longer offers
  // it — otherwise a setting could sit in the harness's config with no way to
  // see or clear it from here.
  const configured = useMemo(() => Object.keys(values ?? {}).sort(), [values]);

  const choose = (id: string) => {
    setSelected(id);
    setDraft(values?.[id] ?? "");
    setOpen(false);
    setError(null);
  };

  const store = async (value: string) => {
    setBusy(true);
    setError(null);
    try {
      const r: { modelSettings?: Record<string, string> } = await wires.command(
        "set_model_setting",
        { instanceId: instance.id, modelId: selected, value },
      );
      setValues(r?.modelSettings ?? {});
      setDraft(value);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const selectedLabel = models.find((m) => m.id === selected)?.label;

  return (
    <div className="space-y-3">
      <div>
        <h3 className="text-[12px] font-medium">{schema.label}</h3>
        {schema.description && (
          <p className="text-muted-foreground text-[11px]">
            {schema.description}
            {schema.docsUrl && (
              <>
                {" "}
                <a
                  href={schema.docsUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-0.5 underline underline-offset-2"
                >
                  Options
                  <ExternalLinkIcon aria-hidden className="size-3" />
                </a>
              </>
            )}
          </p>
        )}
      </div>

      {values === null ? (
        <p className="text-muted-foreground text-[12px]">Loading…</p>
      ) : models.length === 0 && configured.length === 0 ? (
        <p className="text-muted-foreground text-[12px]">
          No models here take this setting yet. Sign in
          {schema.prefix && <> to {schema.prefix.replace(/\/$/, "")}</>} and its models appear here.
        </p>
      ) : (
        <>
          {configured.length > 0 && (
            <div className="space-y-1">
              {configured.map((id) => (
                <button
                  key={id}
                  type="button"
                  onClick={() => choose(id)}
                  className="hover:bg-accent flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left"
                >
                  <span className="font-mono text-[11px]">{shortId(id, schema.prefix)}</span>
                  <span className="text-muted-foreground truncate font-mono text-[10px]">
                    {(values[id] ?? "").replace(/\s+/g, " ")}
                  </span>
                </button>
              ))}
            </div>
          )}

          <div className="space-y-1.5">
            <Label>Model</Label>
            <Popover open={open} onOpenChange={setOpen}>
              <PopoverTrigger asChild>
                <Button
                  variant="outline"
                  size="sm"
                  className="w-full justify-between font-normal"
                  aria-label="Choose a model"
                >
                  <span className="truncate">
                    {selected ? (selectedLabel ?? shortId(selected, schema.prefix)) : "Choose a model…"}
                  </span>
                  <ChevronsUpDownIcon aria-hidden className="size-3.5 opacity-50" />
                </Button>
              </PopoverTrigger>
              <PopoverContent align="start" className="w-[min(28rem,calc(100vw-2rem))] p-0">
                <Command>
                  <CommandInput placeholder="Search models…" />
                  <CommandList className="max-h-[min(50dvh,18rem)]">
                    <CommandEmpty>No model matches that.</CommandEmpty>
                    <CommandGroup>
                      {models.map((m) => (
                        <CommandItem key={m.id} value={`${m.id} ${m.label}`} onSelect={() => choose(m.id)}>
                          <span className="flex min-w-0 flex-col">
                            <span className="truncate">{m.label}</span>
                            <span className="text-muted-foreground truncate font-mono text-[10px]">
                              {shortId(m.id, schema.prefix)}
                            </span>
                          </span>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  </CommandList>
                </Command>
              </PopoverContent>
            </Popover>
          </div>

          {selected && (
            <div className="space-y-1.5">
              <Label htmlFor="model-setting-value">{schema.label} JSON</Label>
              <Textarea
                id="model-setting-value"
                value={draft}
                spellCheck={false}
                rows={4}
                placeholder={schema.placeholder}
                className="font-mono text-[11px]"
                onChange={(e) => setDraft(e.target.value)}
              />
              <div className="flex justify-end gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy || !values[selected]}
                  onClick={() => void store("")}
                >
                  Clear
                </Button>
                <Button size="sm" disabled={busy} onClick={() => void store(draft)}>
                  {busy ? "Saving…" : "Save"}
                </Button>
              </div>
            </div>
          )}
        </>
      )}

      {error && <p className="text-attention-foreground text-[11px]">{error}</p>}
    </div>
  );
}
