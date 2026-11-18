import { HatGlassesIcon, SparklesIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";

import { useDraft, type DraftStore } from "~/app/draftStore";
import { NEW_THREAD } from "~/app/threadKeys";
import type { DraftPreset } from "~/app/useThreadSelection";
import type { ConnectionStatus } from "~/client";
import { Composer, type ComposerHandle } from "~/components/Composer";
import { Alert, AlertDescription } from "~/components/ui/alert";
import type { Attachment } from "~/lib/attachments";
import type { HarnessMeta, UserConfig } from "~/protocol";
import type { NewThreadInput } from "./ThreadDraft";
import { AgentTools } from "./threadDraft/AgentTools";
import { InstanceAlerts } from "./threadDraft/InstanceAlerts";
import { ProviderLogo } from "./ProviderLogo";
import { toolClass } from "./threadDraft/shared";
import { useAgentChoice } from "./threadDraft/useAgentChoice";
import { useRememberedChoices } from "./threadDraft/useRememberedChoices";

/** Where an assistant chat's model picks are remembered, beside the projects'. */
export const ASSISTANT_PREFS = "__assistant__";

// No thread exists yet; the server transcribes without one.
const NO_THREAD = {};

function blockerFor(status: ConnectionStatus, ready: boolean, starting: boolean) {
  if (status !== "online") return "Reconnecting…";
  if (!ready) return "Sign in to a model to start";
  if (starting) return "Starting…";
  return "";
}

/**
 * An assistant chat before it exists: no project, no folder, no branch, just
 * the composer and the model. A private one is kept in the server's memory
 * only and goes for good once closed.
 */
export function AssistantDraft({
  harnesses,
  userConfig,
  status,
  drafts,
  onStart,
  attachments,
  onAttachFiles,
  onRemoveAttachment,
  onRecheck,
  onLogin,
  onManageProviders,
  onAddAccount,
  preset,
  composerRef,
  dictate = false,
}: {
  harnesses: HarnessMeta[];
  userConfig: UserConfig | null;
  status: ConnectionStatus;
  drafts: DraftStore;
  onStart: (input: NewThreadInput, schedule?: boolean) => Promise<void>;
  attachments?: Attachment[];
  onAttachFiles?: (files: File[]) => void;
  onRemoveAttachment?: (key: string) => void;
  onRecheck: () => void;
  onLogin?: (instanceId: string) => void;
  onManageProviders?: () => void;
  onAddAccount?: (harnessId: string | null) => void;
  /** Read on mount only: the app keys the draft per request. */
  preset?: DraftPreset;
  composerRef?: RefObject<ComposerHandle | null>;
  dictate?: boolean;
}) {
  const draft = useDraft(drafts, NEW_THREAD);
  const onDraftChange = useCallback((text: string) => drafts.set(NEW_THREAD, text), [drafts]);
  const remembered = useRememberedChoices();
  const agent = useAgentChoice(
    {
      project: undefined,
      remembered: remembered.preferences[ASSISTANT_PREFS],
      harnesses,
      userConfig,
      prefsKey: ASSISTANT_PREFS,
    },
    remembered,
  );
  // Chosen from the sidebar before the draft opens, not toggled in it.
  const isPrivate = !!preset?.private;
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const ownComposer = useRef<ComposerHandle | null>(null);
  const composer = composerRef ?? ownComposer;
  const focusPending = useRef(!!preset?.focus);
  useEffect(() => {
    if (!focusPending.current || !composer.current) return;
    focusPending.current = false;
    composer.current.focusEnd();
  }, [composer]);

  const blocker = blockerFor(status, agent.instance?.availability?.state === "ready", starting);
  const modelLabel = agent.instance?.models.find((m) => m.id === agent.model)?.label ?? "";

  const start = async (text: string, schedule = false) => {
    if (blocker) return;
    setStarting(true);
    setError(null);
    const input: NewThreadInput = {
      assistant: true,
      private: isPrivate,
      projectId: "",
      folderId: "",
      harness: agent.harnessId,
      instance: agent.instance?.id ?? "",
      model: agent.effectiveModel,
      mode: agent.mode,
      effort: agent.effort,
      serviceTier: agent.serviceTier,
      agentSettingsExplicit: true,
      branch: "",
      workspace: "",
      workspacePath: "",
      baseRef: "",
      text,
      pinned: false,
    };
    try {
      await onStart(input, schedule);
      remembered.recordStart(ASSISTANT_PREFS, { harness: agent.harnessId, folderId: "" });
    } catch (e) {
      onDraftChange(text);
      setError(e instanceof Error ? e.message : String(e));
      setStarting(false);
    }
  };

  const Icon = isPrivate ? HatGlassesIcon : SparklesIcon;
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-1 flex-col items-center justify-center gap-2 px-4 pb-6 text-center [@media(max-height:560px)]:hidden">
        <Icon className="text-muted-foreground size-7" aria-hidden />
        <h2 className="text-[15px] font-medium">{isPrivate ? "Private chat" : "Assistant"}</h2>
        <p className="text-muted-foreground max-w-xs text-[12px]">
          {isPrivate
            ? "Nothing is written to disk. Closing it deletes it, and so does a server restart."
            : "A chat outside any project, on your assistant prompt."}
        </p>
        {modelLabel && (
          <p className="text-muted-foreground flex items-center gap-1.5 text-[12px]">
            <ProviderLogo provider={agent.harnessId} className="size-3" />
            <span className="truncate">{modelLabel}</span>
          </p>
        )}
      </div>

      <div className="mx-auto w-full max-w-3xl space-y-2 px-4 md:px-5">
        <InstanceAlerts
          harnesses={harnesses}
          instances={agent.instances}
          onLogin={onLogin}
          onRecheck={onRecheck}
          onManageProviders={onManageProviders}
          onAddAccount={onAddAccount}
        />
        {error && (
          <Alert variant="destructive">
            <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
          </Alert>
        )}
      </div>

      <div>
        <Composer
          key="new-assistant"
          ref={composer}
          tools={(place) => (
            <AgentTools
              harness={agent.selected}
              modes={agent.modes}
              modeId={agent.displayModeId}
              supports1m={agent.supports1m}
              want1m={agent.want1m}
              onPickMode={(mode) => agent.remember({ mode })}
              onToggle1m={() => agent.remember({ want1m: !agent.want1m })}
              className={place === "row" ? toolClass : undefined}
            />
          )}
          draft={draft}
          onDraftChange={onDraftChange}
          disabled={false}
          sendDisabled={!!blocker}
          disabledPlaceholder={blocker}
          busy={false}
          onSend={(text) => void start(text)}
          onSchedule={() => void start(draft, true)}
          attachments={attachments}
          onAttachFiles={onAttachFiles}
          onRemoveAttachment={onRemoveAttachment}
          onCancel={() => {}}
          dictation={dictate ? NO_THREAD : undefined}
          harnesses={harnesses}
          anyHarness
          harness={agent.harnessId}
          instance={agent.instance?.id ?? ""}
          model={agent.model}
          effort={agent.effort}
          serviceTier={agent.serviceTier}
          onSwitchModel={agent.switchModel}
          onSwitchEffort={(effort) => agent.remember({ effort })}
          onSwitchServiceTier={(tier) => agent.remember({ fast: tier !== "" })}
          onPickInstance={agent.pickInstance}
          onSwitchAccount={agent.switchAccount}
        />
      </div>
    </div>
  );
}
