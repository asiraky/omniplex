import { useState } from "react";

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
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import { skillDescriptionError, skillNameError, type Skill } from "~/lib/skills";
import { cn } from "~/lib/utils";

import { ErrorLine, errorText, type SkillsCommand } from "./parts";

type CreateScope = "project" | "user";

const SCOPES: { id: CreateScope; label: string; hint: string }[] = [
  { id: "project", label: "Project", hint: ".agents/skills in this project, linked into .claude/skills" },
  { id: "user", label: "Personal", hint: "~/.agents/skills, linked into Claude's skills dir" },
];

/** The form is only as fresh as the mount: key it per opening so each one
    starts blank. */
export function NewSkillDialog({
  open,
  onOpenChange,
  command,
  scopeArgs,
  projectAvailable,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  projectAvailable: boolean;
  onCreated: (skill: Skill) => void;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [scope, setScope] = useState<CreateScope>(projectAvailable ? "project" : "user");
  const [touched, setTouched] = useState({ name: false, description: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const nameError = skillNameError(name);
  const descriptionError = skillDescriptionError(description);
  const valid = !nameError && !descriptionError;

  const submit = async () => {
    setTouched({ name: true, description: true });
    if (!valid || busy) return;
    setBusy(true);
    setError("");
    try {
      const skill = await command<Skill>("create_skill", {
        ...scopeArgs,
        scope,
        name,
        description: description.trim(),
      });
      onCreated(skill);
      onOpenChange(false);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  // Shown as soon as there is something typed: the rules are narrow enough
  // that finding out on submit means retyping.
  const showNameError = nameError && (touched.name || name.length > 0);
  const showDescriptionError = descriptionError && touched.description;

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent fullscreenOnMobile className="max-md:grid-rows-[auto_minmax(0,1fr)_auto] max-md:pt-[calc(1.5rem+env(safe-area-inset-top))]">
        <DialogHeader>
          <DialogTitle className="text-[15px]">New skill</DialogTitle>
          <DialogDescription className="text-[12px]">
            Codex and pi read it from .agents/skills; Claude gets a symlink.
          </DialogDescription>
        </DialogHeader>

        <form
          id="new-skill-form"
          className="scroll-thin min-h-0 space-y-4 overflow-y-auto"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <div className="space-y-1.5">
            <Label htmlFor="new-skill-name">Name</Label>
            <Input
              id="new-skill-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onBlur={() => setTouched((t) => ({ ...t, name: true }))}
              placeholder="review-migrations"
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              aria-invalid={Boolean(showNameError)}
              aria-describedby="new-skill-name-hint"
              className="font-mono"
              autoFocus
            />
            <p
              id="new-skill-name-hint"
              className={cn("text-[11px]", showNameError ? "text-destructive" : "text-muted-foreground")}
            >
              {showNameError ? nameError : "Lowercase letters, digits and single hyphens; up to 64."}
            </p>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="new-skill-description">Description</Label>
            <Textarea
              id="new-skill-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              onBlur={() => setTouched((t) => ({ ...t, description: true }))}
              placeholder="What it does and when an agent should use it."
              aria-invalid={Boolean(showDescriptionError)}
              aria-describedby="new-skill-description-hint"
              className="max-h-40 min-h-20"
            />
            <p
              id="new-skill-description-hint"
              className={cn("text-[11px]", showDescriptionError ? "text-destructive" : "text-muted-foreground")}
            >
              {showDescriptionError
                ? descriptionError
                : `The harness reads this to decide when to load the skill. ${description.trim().length}/1024`}
            </p>
          </div>

          <fieldset className="space-y-1.5">
            <legend className="text-foreground mb-1.5 text-sm leading-none font-medium">Where</legend>
            <div role="radiogroup" aria-label="Where" className="flex flex-col gap-1.5">
              {SCOPES.map((s) => {
                const disabled = s.id === "project" && !projectAvailable;
                const picked = scope === s.id;
                return (
                  <button
                    key={s.id}
                    type="button"
                    role="radio"
                    aria-checked={picked}
                    disabled={disabled}
                    onClick={() => setScope(s.id)}
                    className={cn(
                      "focus-visible:ring-ring flex min-h-11 flex-col justify-center gap-0.5 rounded-lg border px-3 py-2 text-left transition-colors outline-none focus-visible:ring-2 disabled:opacity-50",
                      picked ? "border-primary/60 bg-primary/10" : "hover:bg-accent/50",
                    )}
                  >
                    <span className="text-[13px] leading-tight">{s.label}</span>
                    <span className="text-muted-foreground truncate text-[11px] leading-tight">
                      {disabled ? "Open from a thread or project to create one here" : s.hint}
                    </span>
                  </button>
                );
              })}
            </div>
          </fieldset>

          {error && <ErrorLine message={error} />}
        </form>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="new-skill-form" disabled={busy || !valid}>
            {busy && <Spinner className="size-3.5" />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
