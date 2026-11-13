/**
 * `body` as a Markdown code block, with `info` after the opening fence (a language, say). A fence
 * closes on a backtick run at least as long as itself, so it must outrun anything the body already
 * contains: one backtick longer than its longest run, and never shorter than ```.
 */
export function fence(body: string, info = ""): string {
  let longest = 0;
  for (const m of body.matchAll(/`+/g)) longest = Math.max(longest, m[0].length);
  const f = "`".repeat(Math.max(3, longest + 1));
  return `${f}${info}\n${body}\n${f}`;
}
