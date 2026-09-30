import type { Issue } from "~/protocol";

export const DEFAULT_BRANCH_TEMPLATE = "issue/{number}-{title}";

// Kept short and dash-separated so it survives as a branch and a folder name.
function slug(title: string): string {
  return title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40)
    .replace(/-+$/, "");
}

const placeholders: Record<string, (issue: Issue) => string> = {
  number: (issue) => String(issue.number),
  title: (issue) => slug(issue.title),
};

const PLACEHOLDER = /\{([^{}]*)\}/g;

function fill(template: string, issue: Issue): string {
  return template.replace(PLACEHOLDER, (_, name: string) => placeholders[name](issue));
}

/**
 * Turn the branch-name template from the user's settings into a formatter. An
 * unknown placeholder is reported rather than left in the name, and while it is
 * there the suggestions still get a plain `issue/<number>` so the picker keeps
 * working.
 */
export function branchTemplate(template: string): {
  format: (issue: Issue) => string;
  error: string | null;
} {
  const source = template.trim() || DEFAULT_BRANCH_TEMPLATE;
  const unknown = [...source.matchAll(PLACEHOLDER)].find((m) => !Object.hasOwn(placeholders, m[1]));
  if (unknown) {
    const known = Object.keys(placeholders)
      .map((k) => `{${k}}`)
      .join(" and ");
    return {
      format: (issue) => fill("issue/{number}", issue),
      error: `unknown placeholder ${unknown[0]}; the ones that exist are ${known}`,
    };
  }
  return { format: (issue) => fill(source, issue), error: null };
}
