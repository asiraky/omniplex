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
import { errorText, harnessLabel, type Link, type Setup, type SkillHarness } from "~/lib/skills";

import { ErrorLine, SectionHeading, type SkillsCommand } from "./parts";

function linkText(link: Link): string {
  const name = harnessLabel(link.harness);
  switch (link.state) {
    case "direct":
      return `${name} reads the library itself.`;
    case "per-skill":
      return `${name} has its own skills folder, so each skill is linked into it one at a time.`;
    default:
      return `${name} has no skills folder yet, so it sees nothing in the library.`;
  }
}

/**
 * Where skills are kept and how each harness gets to them. Keyed per opening
 * by the caller, so the fields start from what the server last reported.
 */
export function SkillsSetupDialog({
  open,
  onOpenChange,
  command,
  scopeArgs,
  setup,
  onSetup,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  /** Absent when the server does not report setup. */
  setup?: Setup;
  /** The setup the server returned after a save or a link. */
  onSetup: (setup: Setup) => void;
}) {
  const [library, setLibrary] = useState(setup?.library ?? "");
  const [projectLibrary, setProjectLibrary] = useState(setup?.projectLibrary ?? "");
  const [cliVersion, setCliVersion] = useState(setup?.cliVersion ?? "");
  // What is in flight: the save, or a harness being linked.
  const [busy, setBusy] = useState<"" | "save" | SkillHarness>("");
  const [error, setError] = useState("");

  const dirty =
    setup !== undefined &&
    (library.trim() !== setup.library ||
      projectLibrary.trim() !== setup.projectLibrary ||
      cliVersion.trim() !== setup.cliVersion);

  const run = async (what: "save" | SkillHarness, name: string, args: Record<string, unknown>) => {
    if (busy) return;
    setBusy(what);
    setError("");
    try {
      const res = await command<{ setup: Setup }>(name, { ...scopeArgs, ...args });
      // An emptied field means "the default", and the server answers with what
      // that is; show it rather than the blank.
      setLibrary(res.setup.library);
      setProjectLibrary(res.setup.projectLibrary);
      setCliVersion(res.setup.cliVersion);
      onSetup(res.setup);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy("");
    }
  };

  const save = () =>
    void run("save", "save_skills_setup", {
      library: library.trim(),
      projectLibrary: projectLibrary.trim(),
      cliVersion: cliVersion.trim(),
    });

  const fieldProps = {
    autoComplete: "off",
    autoCapitalize: "off",
    autoCorrect: "off",
    spellCheck: false,
    className: "font-mono",
  } as const;

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent
        fullscreenOnMobile
        // The dialog, not the first field: focusing a field pops a phone's
        // keyboard over the page before there is anything to type.
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          (e.currentTarget as HTMLElement).focus();
        }}
        className="max-md:grid-rows-[auto_minmax(0,1fr)_auto] max-md:pt-[calc(1.5rem+env(safe-area-inset-top))] md:max-h-[90dvh] md:max-w-lg md:grid-rows-[auto_minmax(0,1fr)_auto]"
      >
        <DialogHeader>
          <DialogTitle className="text-[15px]">Skills setup</DialogTitle>
          <DialogDescription className="text-[12px]">
            Omniplex writes skills into one library folder. Each harness reads it directly or through links.
          </DialogDescription>
        </DialogHeader>

        {!setup ? (
          <p className="text-muted-foreground text-[13px]">This server does not report its skills setup.</p>
        ) : (
          <form
            id="skills-setup-form"
            className="scroll-thin -mx-1 min-h-0 space-y-5 overflow-y-auto px-1"
            onSubmit={(e) => {
              e.preventDefault();
              if (dirty) save();
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="skills-setup-library">Library</Label>
              <Input
                id="skills-setup-library"
                value={library}
                onChange={(e) => setLibrary(e.target.value)}
                aria-describedby="skills-setup-library-hint"
                {...fieldProps}
              />
              <p id="skills-setup-library-hint" className="text-muted-foreground text-[12px] leading-snug break-words">
                Your personal skills. Leave it empty for the default.{" "}
                {setup.exists ? (
                  setup.libraryDir !== setup.library && (
                    <>
                      On disk: <span className="font-mono text-[11.5px] break-all">{setup.libraryDir}</span>
                    </>
                  )
                ) : (
                  <span className="text-attention-foreground">This folder does not exist yet.</span>
                )}
              </p>
              <p className="text-muted-foreground text-[12px] leading-snug break-words">
                {setup.git ? (
                  <>
                    Inside the git repo <span className="font-mono text-[11.5px] break-all">{setup.git.root}</span>
                    {setup.git.branch && (
                      <>
                        {" "}
                        on <span className="font-mono text-[11.5px]">{setup.git.branch}</span>
                      </>
                    )}
                    .
                  </>
                ) : (
                  "Not inside a git repo."
                )}
              </p>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="skills-setup-project">Project library</Label>
              <Input
                id="skills-setup-project"
                value={projectLibrary}
                onChange={(e) => setProjectLibrary(e.target.value)}
                aria-describedby="skills-setup-project-hint"
                {...fieldProps}
              />
              <p id="skills-setup-project-hint" className="text-muted-foreground text-[12px] leading-snug">
                A folder inside each project, relative to its root. Leave it empty for the default.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="skills-setup-cli">Skills CLI version</Label>
              <Input
                id="skills-setup-cli"
                value={cliVersion}
                onChange={(e) => setCliVersion(e.target.value)}
                aria-describedby="skills-setup-cli-hint"
                {...fieldProps}
              />
              <p id="skills-setup-cli-hint" className="text-muted-foreground text-[12px] leading-snug">
                {setup.npx
                  ? "npx was found, so installs fetch with this version of the skills CLI."
                  : "npx was not found, so installs fetch with git instead."}
              </p>
            </div>

            <section aria-label="Harness links">
              <SectionHeading>How each harness reaches the library</SectionHeading>
              <ul className="divide-y rounded-lg border">
                {(setup.links ?? []).map((link) => (
                  <li key={link.harness} className="flex items-center gap-3 px-3 py-2">
                    <span className="min-w-0 flex-1">
                      <span className="block text-[12.5px] leading-snug">{linkText(link)}</span>
                      <span className="text-muted-foreground block font-mono text-[11.5px] break-all">{link.dir}</span>
                    </span>
                    {link.state === "none" && (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="h-11 shrink-0 text-[12.5px] md:h-8 md:text-[12px]"
                        disabled={busy !== ""}
                        onClick={() => void run(link.harness, "link_library", { harness: link.harness })}
                      >
                        {busy === link.harness && <Spinner className="size-3.5" />}
                        Link {harnessLabel(link.harness)}
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
            </section>

            {error && <ErrorLine message={error} />}
          </form>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy !== ""}>
            {dirty ? "Cancel" : "Close"}
          </Button>
          {setup && (
            <Button type="submit" form="skills-setup-form" disabled={busy !== "" || !dirty}>
              {busy === "save" && <Spinner className="size-3.5" />}
              Save
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
