// Invoked by TestSchedulesBrowser against an isolated server and database.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright";
const [url, sessionId] = process.argv.slice(2);
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
async function open(width) {
  const context = await browser.newContext({
    viewport: { width, height: width < 600 ? 844 : 1000 },
    timezoneId: "Australia/Brisbane",
  });
  await context.addInitScript(
    (id) => localStorage.setItem("omniplex.lastSession", id),
    sessionId,
  );
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(url);
  await page.getByRole("textbox", { name: "Message", exact: true }).waitFor();
  return { context, page };
}
async function state() {
  const r = await fetch(`${url}/api/sessions/${sessionId}`);
  assert.equal(r.status, 200);
  return r.json();
}
async function deliveries() {
  return (await fetch(`${url}/__test/deliveries`)).json();
}
async function create(page, text, minutes = "60") {
  await page.getByRole("textbox", { name: "Message", exact: true }).fill(text);
  await page
    .getByRole("button", { name: "Schedule send", exact: true })
    .click();
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
  await page
    .getByRole("button", { name: "Schedule send", exact: true })
    .click();
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
  await page
    .locator('input[type="file"]')
    .setInputFiles({
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
  // No browser exists while the host restarts and the timer fires.
  assert.equal(
    (await fetch(`${url}/__test/restart`, { method: "POST" })).status,
    204,
  );
  console.log(
    "Desktop and 390px mobile flows passed. Browser closed; waiting for real scheduled delivery after server restart.",
  );
  await until(async () => (await deliveries())?.includes(due.prompt), 75_000);
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
  assert.deepEqual(errors, []);
  console.log(
    "PASS: durable delayed delivery with browser closed, restart, reconnect, relative/absolute timezone, edit, cancel, send now, failure, desktop and mobile.",
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
