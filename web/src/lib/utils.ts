import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * Joins class names and lets a later Tailwind utility win over an earlier one
 * in the same group, so a caller's `className` can override a component's
 * defaults without fighting specificity.
 *
 * Lives outside the component modules so that a file exporting components
 * exports only components — Fast Refresh cannot preserve component state in a
 * module that also exports plain values.
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** What a failed command says, for putting on the page. */
export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
