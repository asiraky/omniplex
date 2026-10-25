import { CheckIcon, ChevronDownIcon, PlayIcon, SquareIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "~/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "~/components/ui/command";
import { Input } from "~/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "~/components/ui/sheet";
import { Spinner } from "~/components/ui/spinner";
import { player, usePlayback } from "~/lib/ttsPlayer";
import { cn } from "~/lib/utils";
import { retryVoices, useVoices, voiceTraits } from "~/lib/voices";
import { useIsDesktop } from "~/useMediaQuery";
import type { Voice } from "~/protocol";

// Previews share the app's one player, so starting one stops the last (and
// any message being read); the prefix is how closing the picker finds its own.
const PREVIEW = "voice-preview:";

function stopPreview() {
  if (player.getState().playingKey?.startsWith(PREVIEW)) player.stop();
}

/**
 * Chooses an ElevenLabs voice: the account's voices, searchable, each with a
 * preview. `defaultVoice` set (even to "") adds a first row that clears the
 * choice back to it, for a project deferring to the user's own default.
 *
 * The list is only the account's own voices, so a voice ID can be pasted into
 * the search instead: anything without spaces that isn't a listed ID can be
 * used as one.
 */
export function VoicePicker({
  id,
  value,
  onChange,
  defaultVoice,
}: {
  id?: string;
  value: string;
  onChange: (voice: string) => void;
  defaultVoice?: string;
}) {
  const { voices, error } = useVoices();
  const [open, setOpenState] = useState(false);
  const [search, setSearch] = useState("");
  const isDesktop = useIsDesktop();
  const setOpen = (next: boolean) => {
    if (!next) {
      stopPreview();
      setSearch("");
    }
    setOpenState(next);
  };
  useEffect(() => stopPreview, []);

  const inherits = defaultVoice !== undefined;
  // A saved voice the list no longer has (deleted on elevenlabs.io, or the
  // list failed) still shows, by id, rather than reading as unset.
  const nameOf = (voice: string) => voices?.find((v) => v.id === voice)?.name ?? voice;
  const defaultLabel = `Use default (${defaultVoice ? nameOf(defaultVoice) : "none chosen"})`;
  const choose = (voice: string) => {
    setOpen(false);
    onChange(voice);
  };

  const triggerLabel = () => {
    if (!voices) return "Loading voices…";
    if (value) return nameOf(value);
    return inherits ? defaultLabel : "Choose a voice";
  };

  // Pasted IDs come with stray whitespace; one inside means it's a search.
  const pasted = search.trim();
  const pastable = pasted !== "" && !/\s/.test(pasted) && !voices?.some((v) => v.id === pasted);

  if (error) return <VoiceListError id={id} error={error} value={value} onChange={onChange} />;

  const trigger = (
    <Button
      id={id}
      variant="outline"
      role="combobox"
      aria-expanded={open}
      disabled={!voices}
      className="h-auto min-h-11 w-full justify-start gap-2 px-3 py-2 md:min-h-9"
    >
      {!voices && <Spinner className="size-4" aria-hidden role={undefined} />}
      <span className="min-w-0 flex-1 truncate text-left text-[13px]">{triggerLabel()}</span>
      <ChevronDownIcon className="text-muted-foreground size-4 shrink-0" />
    </Button>
  );

  const body = voices && (
    <Command className="bg-transparent">
      <CommandInput
        placeholder="Search voices or paste a voice ID…"
        value={search}
        onValueChange={setSearch}
      />
      <CommandList className="max-h-[min(60dvh,22rem)] flex-1">
        {!pastable && <CommandEmpty>No voice matches that.</CommandEmpty>}
        {inherits && (
          <CommandGroup>
            <CommandItem
              value="__default__"
              keywords={["default"]}
              onSelect={() => choose("")}
              className="min-h-11 gap-2 md:min-h-0"
            >
              <CheckIcon className={cn("size-4 shrink-0", value && "opacity-0")} />
              <span className="truncate text-[13px]">{defaultLabel}</span>
            </CommandItem>
          </CommandGroup>
        )}
        <CommandGroup>
          {voices.map((v) => (
            <VoiceRow key={v.id} voice={v} selected={v.id === value} onChoose={choose} />
          ))}
        </CommandGroup>
        {pastable && (
          <PastedVoiceRow voice={pasted} selected={pasted === value} onChoose={choose} />
        )}
      </CommandList>
    </Command>
  );

  if (!isDesktop) {
    return (
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger asChild>{trigger}</SheetTrigger>
        <SheetContent
          side="bottom"
          className="flex h-[85dvh] flex-col p-0 pb-[env(safe-area-inset-bottom)]"
        >
          <SheetHeader className="pb-0">
            <SheetTitle>Voice</SheetTitle>
          </SheetHeader>
          {body}
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent align="start" className="w-[min(26rem,calc(100vw-2rem))] p-0">
        {body}
      </PopoverContent>
    </Popover>
  );
}

/** A pasted voice ID offered as a choice. */
function PastedVoiceRow({
  voice,
  selected,
  onChoose,
}: {
  voice: string;
  selected: boolean;
  onChoose: (voice: string) => void;
}) {
  return (
    // Forced, so the search filter can't hide it; cmdk puts it last.
    <CommandGroup forceMount>
      <CommandItem
        forceMount
        value={`__id__:${voice}`}
        onSelect={() => onChoose(voice)}
        className="min-h-11 gap-2 md:min-h-0"
      >
        <CheckIcon className={cn("size-4 shrink-0", !selected && "opacity-0")} />
        <span className="min-w-0 flex-1 truncate text-[13px]">
          Use voice ID <span className="font-mono">{voice}</span>
        </span>
      </CommandItem>
    </CommandGroup>
  );
}

/** The list failed: say why, offer a retry, and take a voice ID typed in. */
function VoiceListError({
  id,
  error,
  value,
  onChange,
}: {
  id?: string;
  error: string;
  value: string;
  onChange: (voice: string) => void;
}) {
  return (
    <div className="space-y-2">
      <div className="flex min-h-11 items-center gap-2 md:min-h-9">
        <p className="text-attention-foreground min-w-0 flex-1 text-[12px] break-words">{error}</p>
        <Button variant="outline" size="sm" onClick={retryVoices}>
          Retry
        </Button>
      </div>
      {/* Keyed on the saved value, so a save from elsewhere replaces the draft. */}
      <VoiceIdInput key={value} id={id} initialValue={value} onChange={onChange} />
    </div>
  );
}

/**
 * With no list to pick from, a voice ID typed in. Saved on blur or Enter, and
 * only if it changed; cleared, it goes back to unset (or the default).
 */
function VoiceIdInput({
  id,
  initialValue,
  onChange,
}: {
  id?: string;
  initialValue: string;
  onChange: (voice: string) => void;
}) {
  const [draft, setDraft] = useState(initialValue);
  const commit = () => {
    const next = draft.trim();
    if (next !== initialValue) onChange(next);
  };
  return (
    <Input
      id={id}
      placeholder="Or paste a voice ID"
      autoComplete="off"
      spellCheck={false}
      className="font-mono"
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          commit();
        }
      }}
    />
  );
}

function VoiceRow({
  voice,
  selected,
  onChoose,
}: {
  voice: Voice;
  selected: boolean;
  onChoose: (voice: string) => void;
}) {
  const traits = voiceTraits(voice);
  return (
    <CommandItem
      value={voice.id}
      keywords={[voice.name, ...traits, voice.description ?? ""]}
      onSelect={() => onChoose(voice.id)}
      className="min-h-11 gap-2 py-1 md:min-h-0"
    >
      <CheckIcon className={cn("size-4 shrink-0", !selected && "opacity-0")} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px]">{voice.name}</span>
        {traits.length > 0 && (
          <span className="text-muted-foreground block truncate text-[11px]">
            {traits.join(" · ")}
          </span>
        )}
      </span>
      {voice.previewUrl && <PreviewButton voice={voice} url={voice.previewUrl} />}
    </CommandItem>
  );
}

function PreviewButton({ voice, url }: { voice: Voice; url: string }) {
  const key = PREVIEW + voice.id;
  const playback = usePlayback(key);
  return (
    <button
      type="button"
      aria-label={playback ? `Stop the ${voice.name} preview` : `Preview ${voice.name}`}
      // The row selects on click; hearing a voice is not choosing it.
      onClick={(e) => {
        e.stopPropagation();
        if (playback) player.stop();
        else player.playURL(key, url);
      }}
      className="text-muted-foreground hover:text-foreground hover:bg-background focus-visible:ring-ring grid size-9 shrink-0 place-items-center rounded-md outline-none focus-visible:ring-2 md:size-7"
    >
      {playback === "loading" ? (
        <Spinner className="size-3.5" aria-hidden role={undefined} />
      ) : playback === "playing" ? (
        <SquareIcon className="size-3 fill-current" />
      ) : (
        <PlayIcon className="size-3.5" />
      )}
    </button>
  );
}
