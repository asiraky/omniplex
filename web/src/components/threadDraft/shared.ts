/** Work in the folder, on a new copy of it, or on a copy that already exists. */
export type WorkspaceKind = "main" | "branch" | "attach";

export function folderName(path: string) {
  return path.replace(/\/+$/, "").split("/").pop() || path;
}

// Borderless and muted like the model picker, so the options read as part
// of the composer rather than a second row of buttons stacked on it.
export const chipClass =
  "text-muted-foreground hover:text-foreground h-9 max-w-full shrink-0 gap-1.5 px-2 text-[12px] font-normal md:h-7 [&_svg]:size-3.5";
export const toolClass = "h-11 text-[13px] md:h-8";
