// Where a rendered list item sits in its list, for whatever turns rendered
// markdown back into text: read aloud, and quoted into a reply. Apart from
// `~/lib/speech` so that quoting does not load the speech converter.

/** The number an ordered list's first item has. */
export function listStart(list: Element): number {
  const start = Number.parseInt(list.getAttribute("start") ?? "", 10);
  return Number.isNaN(start) ? 1 : start;
}

/** An ordered list item's number, or null when its list is not ordered. */
export function listNumber(li: Element): number | null {
  const list = li.parentElement;
  if (list?.tagName !== "OL") return null;
  let n = listStart(list);
  for (let sib = li.previousElementSibling; sib; sib = sib.previousElementSibling)
    if (sib.tagName === "LI") n++;
  return n;
}
