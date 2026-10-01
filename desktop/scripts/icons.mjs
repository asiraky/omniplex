// Renders the desktop icons from the brand mark in web/public/favicon.svg.
// Run by hand (`npm run icons`) when the mark changes; the output is committed.
// Uses the Playwright Chromium the repo already installs for browser tests.
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { chromium } from "playwright";

const here = path.dirname(fileURLToPath(import.meta.url));
const desktop = path.resolve(here, "..");
const favicon = await readFile(path.resolve(desktop, "../web/public/favicon.svg"), "utf8");

// The tile, full bleed. Windows and Linux icons use it as is.
const tile = favicon.replace(/<!--[\s\S]*?-->/g, "");

// macOS app icons sit on Apple's grid: an 824px body inside a 1024px canvas,
// with a shadow, so the icon lines up with every other app in the Dock.
const macIcon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024">
  <defs><filter id="s" x="-20%" y="-20%" width="140%" height="140%">
    <feDropShadow dx="0" dy="10" stdDeviation="14" flood-opacity="0.28"/>
  </filter></defs>
  <g filter="url(#s)"><svg x="100" y="100" width="824" height="824" viewBox="0 0 100 100">
    ${tile.replace(/^<svg[^>]*>|<\/svg>\s*$/g, "")}
  </svg></g>
</svg>`;

// The menu bar wants a template image: black on transparent, the system tints
// it. The canvas is wider than the glyph so the menu bar item is easy to hit.
// The bars keep the tile's tall proportion but fill the 18pt height.
function trayTemplate(scale) {
  const w = 14 * scale, h = 18 * scale;
  const bw = 3.75 * scale, bh = 5 * scale, gap = 1.25 * scale, r = 1.4 * scale;
  const top = (h - (3 * bh + 2 * gap)) / 2, x = (w - bw) / 2;
  const bars = [0, 1, 2]
    .map((i) => `<rect x="${x}" y="${top + i * (bh + gap)}" width="${bw}" height="${bh}" rx="${r}" fill="#000"/>`)
    .join("");
  return { svg: `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}">${bars}</svg>`, w, h };
}

const outputs = [
  { file: "build/icon.png", svg: macIcon, w: 1024, h: 1024 },
  { file: "build/icon-win.png", svg: tile, w: 512, h: 512 },
  { file: "resources/icons/window.png", svg: tile, w: 256, h: 256 },
  { file: "resources/icons/tray.png", svg: tile, w: 16, h: 16 },
  { file: "resources/icons/tray@2x.png", svg: tile, w: 32, h: 32 },
  { file: "resources/icons/trayTemplate.png", ...trayTemplate(1) },
  { file: "resources/icons/trayTemplate@2x.png", ...trayTemplate(2) },
];

const browser = await chromium.launch({ channel: "chromium" });
try {
  const page = await browser.newPage();
  for (const { file, svg, w, h } of outputs) {
    await page.setViewportSize({ width: w, height: h });
    const sized = svg.replace(/^<svg /, `<svg width="${w}" height="${h}" `);
    await page.setContent(
      `<html><body style="margin:0;background:transparent">${sized}</body></html>`,
    );
    await page.locator("svg").first().screenshot({ path: path.join(desktop, file), omitBackground: true });
    console.log(`wrote ${file} (${w}x${h})`);
  }
} finally {
  await browser.close();
}
