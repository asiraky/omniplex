import { ArrowUpIcon, FolderGit2Icon, FolderIcon, LockIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Spinner } from "~/components/ui/spinner";
import type { GitHubRepo } from "~/protocol";

interface Listing {
  path: string;
  parent: string;
  dirs: string[];
}

/**
 * Folders on the machine running Omniplex, one level at a time. On a phone
 * that machine is somewhere else, so this is the only way to see its disk.
 */
export function FolderBrowser({
  onChoose,
  busy,
  action = "Use this folder",
}: {
  onChoose: (path: string) => void;
  busy?: boolean;
  action?: string;
}) {
  const [listing, setListing] = useState<Listing | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Only the latest tap counts. Two quick taps on a slow link can answer out
  // of order, and the older answer landing last would leave you in the wrong
  // folder with the path saying so.
  const latest = useRef(0);
  const load = async (path: string) => {
    const mine = ++latest.current;
    setError(null);
    try {
      const r = await fetch(`/api/fs?path=${encodeURIComponent(path)}`);
      const body = r.ok ? ((await r.json()) as Listing) : (await r.text()).trim();
      if (mine !== latest.current) return;
      if (typeof body === "string") setError(body || `Could not open that folder (${r.status})`);
      else setListing(body);
    } catch (e) {
      if (mine === latest.current) setError(e instanceof Error ? e.message : String(e));
    }
  };
  useEffect(() => {
    void load("~");
    // Nothing that lands after unmount should set state.
    return () => {
      latest.current++;
    };
  }, []);

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Button
          variant="outline"
          size="icon"
          aria-label="Up one folder"
          disabled={!listing || listing.parent === listing.path}
          onClick={() => listing && void load(listing.parent)}
          className="size-11 shrink-0 md:size-8"
        >
          <ArrowUpIcon />
        </Button>
        <p className="min-w-0 flex-1 truncate font-mono text-[12px]" dir="rtl">
          {/* rtl keeps the end of a long path, the part that says where you are. */}
          <bdi>{listing?.path ?? "…"}</bdi>
        </p>
      </div>
      <div className="scroll-thin h-56 overflow-y-auto rounded-lg border">
        {listing?.dirs.length === 0 && (
          <p className="text-muted-foreground px-3 py-2 text-[12px]">No folders in here.</p>
        )}
        {listing?.dirs.map((d) => (
          <button
            key={d}
            type="button"
            onClick={() => void load(`${listing.path.replace(/\/$/, "")}/${d}`)}
            className="hover:bg-accent flex min-h-11 w-full items-center gap-2 px-3 text-left font-mono text-[12px] md:min-h-8"
          >
            <FolderIcon className="text-muted-foreground size-3.5 shrink-0" />
            <span className="truncate">{d}</span>
          </button>
        ))}
      </div>
      {error && <p className="text-destructive text-[11px]">{error}</p>}
      <Button
        className="w-full"
        disabled={!listing || busy}
        onClick={() => listing && onChoose(listing.path)}
      >
        {busy && <Spinner aria-hidden className="size-4" />}
        {action}
      </Button>
    </div>
  );
}

/**
 * A repository to clone: pasted, or picked from what `gh repo list` knows
 * about. The list is fetched when this opens, not before, since most people
 * adding a project never look at it.
 */
export function GitHubPicker({
  listRepos,
  onChoose,
  busy,
}: {
  listRepos: () => Promise<GitHubRepo[]>;
  onChoose: (url: string) => void;
  busy?: boolean;
}) {
  const [url, setUrl] = useState("");
  const [repos, setRepos] = useState<GitHubRepo[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    listRepos().then(
      (r) => live && setRepos(r),
      (e: unknown) => live && setListError(e instanceof Error ? e.message : String(e)),
    );
    return () => {
      live = false;
    };
  }, [listRepos]);

  // The box doubles as a filter: typing part of a name narrows the list, and
  // anything that is not in it is cloned as typed.
  const shown = useMemo(() => {
    const q = url.trim().toLowerCase();
    return (repos ?? []).filter((r) => !q || r.name.toLowerCase().includes(q)).slice(0, 50);
  }, [repos, url]);

  return (
    <div className="space-y-2">
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (url.trim()) onChoose(url.trim());
        }}
      >
        <Input
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="owner/repo or a URL"
          aria-label="Repository"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          className="min-w-0 flex-1 font-mono md:text-[12px]"
        />
        <Button type="submit" disabled={!url.trim() || busy}>
          {busy && <Spinner aria-hidden className="size-4" />}
          {busy ? "Cloning…" : "Clone"}
        </Button>
      </form>
      <div className="scroll-thin h-56 overflow-y-auto rounded-lg border">
        {!repos && !listError && (
          <p className="text-muted-foreground flex items-center gap-2 px-3 py-2 text-[12px]">
            <Spinner aria-hidden className="size-3.5" /> Asking GitHub…
          </p>
        )}
        {listError && <p className="text-muted-foreground px-3 py-2 text-[12px]">{listError}</p>}
        {repos && shown.length === 0 && (
          <p className="text-muted-foreground px-3 py-2 text-[12px]">
            None of your repositories match. Clone clones it as typed.
          </p>
        )}
        {shown.map((r) => (
          <button
            key={r.name}
            type="button"
            disabled={busy}
            onClick={() => onChoose(r.name)}
            className="hover:bg-accent flex min-h-11 w-full flex-col justify-center px-3 py-1.5 text-left md:min-h-8"
          >
            <span className="flex items-center gap-1.5 font-mono text-[12px]">
              <FolderGit2Icon className="text-muted-foreground size-3.5 shrink-0" />
              <span className="truncate">{r.name}</span>
              {r.private && <LockIcon aria-label="Private" className="text-muted-foreground size-3 shrink-0" />}
            </span>
            {r.description && (
              <span className="text-muted-foreground truncate text-[11px]">{r.description}</span>
            )}
          </button>
        ))}
      </div>
    </div>
  );
}
