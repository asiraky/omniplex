// Invoked by TestSchedulesBrowser against an isolated server and database.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright";
const [url, threadId, limitId, cancelId] = process.argv.slice(2);
const browser = await chromium.launch({
  headless: true,
  ...(process.env.CHROME_BIN ? { executablePath: process.env.CHROME_BIN } : {}),
});
const errors = [];
const artifacts =
  process.env.OMNIPLEX_SCREENSHOTS || ".omniplex/schedule-screenshots";
await mkdir(artifacts, { recursive: true });
async function until(check, timeout = 15_000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await check()) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error("Timed out waiting for expected state");
}
async function open(width, id = threadId) {
  const context = await browser.newContext({
    viewport: { width, height: width < 600 ? 844 : 1000 },
    timezoneId: "Australia/Brisbane",
  });
  await context.addInitScript(
    (id) => localStorage.setItem("omniplex.lastThread", id),
    id,
  );
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(url);
  await page.getByRole("textbox", { name: "Message", exact: true }).waitFor();
  return { context, page };
}
async function state(id = threadId) {
  const r = await fetch(`${url}/api/threads/${id}`);
  assert.equal(r.status, 200);
  return r.json();
}
async function deliveries() {
  return (await fetch(`${url}/__test/deliveries`)).json();
}
async function shot(page, name) {
  await page.screenshot({
    animations: "disabled",
    path: `${artifacts}/${name}.png`,
  });
}
async function fits(page) {
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
    true,
    "mobile page overflows",
  );
}
const chip = (page) =>
  page.locator("div.rounded-full").filter({ hasText: /^Continuing/ });
const heldLabel = (page, until = "the limit resets") =>
  page.getByText(`held until ${until}`, { exact: true });
// Runs a turn that stops at a usage limit resetting in a second, with a
// prompt queued behind it; the limit card on every page shows the chip.
async function hitLimit(pages, id, prompt, queued) {
  const box = pages[0].getByRole("textbox", { name: "Message", exact: true });
  await box.fill(prompt);
  await box.press("Enter");
  await until(async () => (await deliveries())?.includes(prompt));
  await box.fill(queued);
  await box.press("Enter");
  await until(async () => (await state(id)).queuedPrompts?.length === 1);
  const released = Date.now();
  const r = await fetch(
    `${url}/__test/limit?in=1000&prompt=${encodeURIComponent(prompt)}`,
    { method: "POST" },
  );
  assert.equal(r.status, 204);
  for (const page of pages) {
    await chip(page).waitFor();
    await heldLabel(page).waitFor();
    // The continuation is the card's alone, not a row in the schedule list.
    assert.equal(await page.locator("[data-schedule-id]").count(), 0);
  }
  const armed = (await state(id)).scheduledPrompts.filter(
    (p) => p.kind === "limit",
  );
  assert.equal(armed.length, 1);
  assert.equal(armed[0].status, "pending");
  // A minute past the reset, which was a second after release.
  assert.ok(armed[0].dueAt >= released + 61_000, "continuation armed early");
  assert.ok(armed[0].dueAt <= Date.now() + 61_000, "continuation armed late");
  assert.ok(!(await deliveries()).includes(queued), "held queue was sent");
  return armed[0];
}
// Scheduling sits in the send button's menu.
async function scheduleSend(page) {
  await page
    .getByRole("button", { name: "More send options", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "Schedule send…" }).click();
}
async function create(page, text, minutes = "60") {
  await page.getByRole("textbox", { name: "Message", exact: true }).fill(text);
  await scheduleSend(page);
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Hours", { exact: true }).fill("0");
  await dialog.getByLabel("Minutes", { exact: true }).fill(minutes);
  await dialog
    .getByRole("button", { name: "Schedule message", exact: true })
    .click();
  await dialog.waitFor({ state: "hidden" });
  const card = page.locator("[data-schedule-id]").filter({ hasText: text });
  await card.waitFor();
  return card;
}
try {
  let { context, page } = await open(1440);
  let card = await create(
    page,
    "Continue the feature, run the tests and open the PR.",
  );
  assert.equal(
    await page
      .getByRole("textbox", { name: "Message", exact: true })
      .inputValue(),
    "",
  );
  assert.deepEqual(await deliveries(), null);
  await card.getByRole("button", { name: "Edit", exact: true }).click();
  let dialog = page.getByRole("dialog");
  await dialog
    .getByRole("textbox", { name: "Scheduled message", exact: true })
    .fill("Edited overnight work");
  await dialog.getByRole("button", { name: "In…", exact: true }).click();
  await dialog.getByLabel("Hours", { exact: true }).fill("25");
  assert.equal(
    await dialog.getByRole("button", { name: "Save changes" }).isDisabled(),
    true,
  );
  await dialog.getByLabel("Hours", { exact: true }).fill("2");
  await dialog.getByLabel("Timezone", { exact: true }).fill("America/New_York");
  await dialog.getByRole("button", { name: "Save changes" }).click();
  await dialog.waitFor({ state: "hidden" });
  card = page
    .locator("[data-schedule-id]")
    .filter({ hasText: "Edited overnight work" });
  await card.waitFor();
  await page.reload();
  await card.waitFor();
  await page.screenshot({
    animations: "disabled",
    path: `${artifacts}/scheduled-prompts-desktop.png`,
  });
  await card.getByRole("button", { name: "Cancel", exact: true }).click();
  await card.waitFor({ state: "hidden" });
  card = await create(page, "Send this now");
  await card.getByRole("button", { name: "Send now", exact: true }).click();
  await until(async () => (await deliveries())?.includes("Send this now"));
  await card.waitFor({ state: "hidden" });
  // An absolute time entered in a different timezone resolves to the same instant.
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Absolute timezone check");
  await scheduleSend(page);
  dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "At a time", exact: true }).click();
  await dialog.getByLabel("Timezone", { exact: true }).fill("UTC");
  const expected = new Date(Date.now() + 7_200_000);
  expected.setUTCSeconds(0, 0);
  await dialog
    .getByLabel("Date and time", { exact: true })
    .fill(expected.toISOString().slice(0, 16));
  await dialog
    .getByRole("button", { name: "Schedule message", exact: true })
    .click();
  await dialog.waitFor({ state: "hidden" });
  const absolute = (await state()).scheduledPrompts.find(
    (p) => p.prompt === "Absolute timezone check",
  );
  assert.equal(absolute.dueAt, expected.getTime());
  card = page
    .locator("[data-schedule-id]")
    .filter({ hasText: "Absolute timezone check" });
  await card.getByRole("button", { name: "Cancel", exact: true }).click();
  await card.waitFor({ state: "hidden" });
  await context.close();
  ({ context, page } = await open(390));
  await page.locator('input[type="file"]').setInputFiles({
    name: "reference.png",
    mimeType: "image/png",
    buffer: Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a1ZkAAAAASUVORK5CYII=",
      "base64",
    ),
  });
  card = await create(
    page,
    "Continue implementing the feature. Run all tests and prepare the PR before morning.",
    "1",
  );
  const scheduledAt = Date.now();
  const due = (await state()).scheduledPrompts.find((p) =>
    p.prompt.startsWith("Continue implementing"),
  );
  assert.equal(due.images.length, 1, "scheduled attachment missing");
  assert.ok(due.dueAt > scheduledAt && due.dueAt - scheduledAt <= 60_000);
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
    true,
    "mobile page overflows",
  );
  await page.screenshot({
    animations: "disabled",
    path: `${artifacts}/scheduled-prompts-mobile.png`,
  });
  await card.getByRole("button", { name: "Edit", exact: true }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "In…", exact: true }).click();
  await page.screenshot({
    animations: "disabled",
    path: `${artifacts}/schedule-dialog-mobile.png`,
  });
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  card = await create(page, "FAIL scheduled delivery");
  await card.getByRole("button", { name: "Send now", exact: true }).click();
  await card.getByText("test provider is out of tokens").waitFor();
  await card.getByRole("button", { name: "Cancel", exact: true }).click();
  await card.waitFor({ state: "hidden" });
  await context.close();
  // A usage limit arms a continuation and holds the queue behind it.
  const continueQueued = "Queued behind the limit, continue flow";
  const cancelQueued = "Queued behind the limit, cancel flow";
  let desktop = await open(1440, limitId);
  let mobile = await open(390, limitId);
  const continuation = await hitLimit(
    [desktop.page, mobile.page],
    limitId,
    "LIMIT continue flow",
    continueQueued,
  );
  await fits(mobile.page);
  await shot(desktop.page, "10-limit-chip-desktop");
  await shot(mobile.page, "11-limit-chip-mobile");
  await desktop.context.close();
  await mobile.context.close();
  desktop = await open(1440, cancelId);
  mobile = await open(390, cancelId);
  const cancelled = await hitLimit(
    [mobile.page, desktop.page],
    cancelId,
    "LIMIT cancel flow",
    cancelQueued,
  );
  // Close up: the limit card down to the held prompt behind it.
  for (const [page, name] of [
    [desktop.page, "12-limit-held-queue-desktop"],
    [mobile.page, "13-limit-held-queue-mobile"],
  ]) {
    const top = await page
      .getByText("hit its usage limit.", { exact: false })
      .boundingBox();
    const bottom = await page.locator("[data-queue-id]").boundingBox();
    const width = page.viewportSize().width;
    const x = width < 600 ? 0 : bottom.x + bottom.width - 800;
    await page.screenshot({
      animations: "disabled",
      path: `${artifacts}/${name}.png`,
      clip: {
        x,
        y: top.y - 24,
        width: width < 600 ? width : 820,
        height: bottom.y + bottom.height - top.y + 40,
      },
    });
  }
  // Cancel from the phone; the desktop follows.
  await chip(mobile.page)
    .getByRole("button", { name: "Cancel", exact: true })
    .click();
  for (const page of [mobile.page, desktop.page]) {
    await chip(page).waitFor({ state: "hidden" });
    // Nothing is armed now, so the queue still waits for a human.
    await heldLabel(page, "you send").waitFor();
  }
  assert.equal(
    (await state(cancelId)).scheduledPrompts.find((p) => p.id === cancelled.id)
      .status,
    "cancelled",
  );
  await fits(mobile.page);
  await shot(desktop.page, "14-limit-cancelled-desktop");
  await shot(mobile.page, "15-limit-cancelled-mobile");
  await desktop.context.close();
  await mobile.context.close();
  // No browser exists while the host restarts and the timer fires.
  assert.equal(
    (await fetch(`${url}/__test/restart`, { method: "POST" })).status,
    204,
  );
  console.log(
    "Desktop and 390px mobile flows passed. Browser closed; waiting for real scheduled delivery after server restart.",
  );
  await until(async () => {
    const sent = await deliveries();
    return [due.prompt, continuation.prompt].every((p) => sent?.includes(p));
  }, 90_000);
  assert.ok(Date.now() >= due.dueAt, "delivered early");
  assert.equal((await deliveries()).filter((p) => p === due.prompt).length, 1);
  assert.equal(
    (await deliveries()).filter((p) => p === "FAIL scheduled delivery").length,
    1,
    "provider failure retried automatically",
  );
  ({ context, page } = await open(390));
  await page.getByText(`Completed: ${due.prompt}`, { exact: true }).waitFor();
  assert.equal(await page.locator("[data-schedule-id]").count(), 0);
  await context.close();
  // The continuation went out after the reset, through the restart, and
  // jumped the held queue. The restart held that queue for a human, so it
  // waits for Resume queue and then drains behind the continuation.
  assert.ok(Date.now() >= continuation.dueAt, "continued early");
  desktop = await open(1440, limitId);
  mobile = await open(390, limitId);
  await until(async () => (await state(limitId)).phase === "idle", 30_000);
  assert.ok(
    !(await deliveries()).includes(continueQueued),
    "queue held by the restart ran by itself",
  );
  await mobile.page.getByRole("button", { name: "Resume queue" }).click();
  await until(async () => (await deliveries()).includes(continueQueued));
  let sent = await deliveries();
  assert.ok(
    sent.indexOf(continuation.prompt) < sent.indexOf(continueQueued),
    "held queue ran before the continuation",
  );
  for (const page of [desktop.page, mobile.page]) {
    await page
      .getByText(`Completed: ${continueQueued}`, { exact: true })
      .waitFor();
    assert.equal(await chip(page).count(), 0);
    assert.equal(await page.getByText(/^held until/).count(), 0);
    // The server wrote that prompt, so it is not shown as the reader's own.
    await page
      .getByText(
        "Usage limit reset — the agent was asked to pick the work back up",
        { exact: true },
      )
      .waitFor();
  }
  await fits(mobile.page);
  await shot(desktop.page, "16-limit-continued-desktop");
  await shot(mobile.page, "17-limit-continued-mobile");
  await desktop.context.close();
  await mobile.context.close();
  // The cancelled continuation stays cancelled once its time has passed.
  await until(async () => Date.now() > cancelled.dueAt + 3_000, 30_000);
  sent = await deliveries();
  assert.equal(sent.filter((p) => p === continuation.prompt).length, 1);
  assert.ok(!sent.includes(cancelQueued), "cancelled thread's queue ran");
  const after = await state(cancelId);
  assert.equal(after.queuedPrompts.length, 1);
  assert.equal(
    after.scheduledPrompts.find((p) => p.id === cancelled.id).status,
    "cancelled",
  );
  assert.deepEqual(errors, []);
  console.log(
    "PASS: durable delayed delivery with browser closed, restart, reconnect, relative/absolute timezone, edit, cancel, send now, failure, usage-limit continuation and its cancel, desktop and mobile.",
  );
} catch (e) {
  console.error(e);
  for (const context of browser.contexts()) {
    for (const page of context.pages()) {
      await page
        .screenshot({
          animations: "disabled",
          path: `${artifacts}/failure.png`,
        })
        .catch(() => {});
      console.error((await page.locator("body").innerText()).slice(-5000));
    }
  }
  process.exitCode = 1;
} finally {
  await browser.close();
}
