# Light/dark theme + WCAG contrast audit — Teldrive v2 UI

> **Status (updated after the fixes):** this report records the state *before* the fix, and every
> defect below has since been addressed. `next-themes` now writes both `class` and `data-theme`
> (`ui/src/main.tsx`), so light mode applies for real; light `--muted` is `oklch(0.505 0 0)`,
> `--danger` is `oklch(0.58 0.208 25)` in both themes, and the glass panels are token-driven with
> light overrides. Re-audit result: contrast violations **light 16 → 0, dark 16 → 0,
> forced-light 56 → 0** (raw JSON: `tools/ui-light-report.json`, harness: `tools/ui-audit-theme.mjs`).

**Verdict (pre-fix): the light theme does not work at all.** Choosing 浅色 (Light) is recorded, persisted
and reflected in `class`, `color-scheme` and `localStorage`, but **100% of the UI keeps rendering
with the dark palette** on every route, viewport and reload. The secondary text layer also sits
just below WCAG AA once light actually renders, and a destructive-button colour fails AA in *both*
themes.

Read-only audit. Nothing under `src/` was modified.

| | |
|---|---|
| App under test | `http://127.0.0.1:8080` |
| Harness | [tools/ui-audit-theme.mjs](tools/ui-audit-theme.mjs) |
| Raw JSON | [tools/ui-light-report.json](tools/ui-light-report.json), [tools/ui-theme-leftovers.json](tools/ui-theme-leftovers.json) |
| Coverage | 16 routes × 2 viewports × 2 themes = **64 page loads**, plus 5 forced-light pages (69 total) |
| Text nodes measured | **808** per theme (light/dark); **185** on the 5 forced-light pages |
| Locale | zh-CN (labels in the evidence below are the rendered labels) |

### Which build was measured

The sweep ran against the dist build timestamped **11:32:47**. While this audit was running the
concurrent i18n agent rebuilt the UI (**11:59**) and restarted the server (**12:01:47**), so the
defect was **re-verified live on the new build at 12:02** (switcher test + computed styles) and in
the new bundle's static CSS. Root cause and numbers are identical on both builds — see
"Still present after the rebuild" at the end.

---

## (a) Does light theme apply correctly? No — not at all

Requested theme `light`, measured on **32/32 pages** (16 routes × desktop 1440×900 + mobile 390×844):

| Signal | Requested `light` | Requested `dark` | Correct? |
|---|---|---|---|
| `documentElement.className` | `light` | `dark` | ✅ |
| `documentElement.getAttribute("data-theme")` | **`dark`** | `dark` | ❌ never cleared |
| computed `color-scheme` | `light` | `dark` | ✅ |
| resolved `--background` | **`oklch(13% .005 70)`** (dark token) | `oklch(13% .005 70)` | ❌ |
| resolved `--foreground` | **`oklch(95.5% 0 0)`** (dark token) | `oklch(95.5% 0 0)` | ❌ |
| `body` computed `background-color` | **`rgb(8, 7, 6)`**, luminance **0.0022** | `rgb(8, 7, 6)` | ❌ |
| `localStorage.theme` | `light` | `dark` | ✅ |

The full CSS-variable snapshot (16 tokens) is **byte-identical between the two runs**
(`light.tokensHash === dark.tokensHash → true`). The requested light theme changes nothing.

**Root cause (confirmed in source and in the built bundle).** `ui/index.html` hardcodes the theme
on the root element:

```html
<html lang="en" translate="no" class="dark" data-theme="dark" style="color-scheme: dark;">
```

`next-themes` is configured `attribute="class"` ([main.tsx:48](src/teldrive-2/ui/src/main.tsx#L48)),
so it only rewrites the **class** attribute. Nothing in the app ever writes or removes
`data-theme` (a search for `data-theme` across `ui/src/**/*.tsx` returns **no matches**), so the
literal `data-theme="dark"` survives every navigation. `globals.css` then defines light tokens in
`:root` and dark tokens in `.dark, [data-theme="dark"]`
([globals.css:50-136](src/teldrive-2/ui/src/styles/globals.css#L50-L136)) — **equal specificity,
dark block later in the stylesheet**, so the dark block wins whenever the attribute is present.
Verified in the built CSS: light `:root` block at byte offset 39153, dark block at 40064
(`.dark,[data-theme=dark]{…}`). HeroUI's own token layer uses the same
`.dark,[data-theme=dark],:host(.dark),:host([data-theme=dark])` selector, so it is broken the same way.

### Quantitative proof that light renders as dark

Comparing the light-theme and dark-theme screenshots of the same route+viewport:

- **mean 0.10% of pixels differ; max 1.82%; 0 of 32 pairs differ by more than 5%.**
- The only changed region is the **15×15 px theme-toggle icon** in the header, box
  `(1394,24)–(1409,39)` — the icon flips because `resolvedTheme` *is* `"light"`.
- Control: the forced-light pass (same pages, stale attribute removed at runtime, DOM only)
  differs from the light run by **99.98–100% of pixels**.

So "light applied" moves 0.1% of the screen; a real light theme moves ~100% of it.

### The switcher applies and persists — but the screen never changes

Verified on `/settings/appearance` (seed `theme=dark`, then reload, then click 浅色):

| Step | `class` | `data-theme` | `color-scheme` | `body` background | `localStorage.theme` |
|---|---|---|---|---|---|
| initial | `dark` | `dark` | `dark` | `oklch(0.13 0.005 70)` | `dark` |
| after click 浅色 (no reload) | `light` | **`dark`** | `light` | **`oklch(0.13 0.005 70)`** | `light` |
| after reload | `light` | **`dark`** | `light` | **`oklch(0.13 0.005 70)`** | `light` |
| after clicking 深色 again | `dark` | `dark` | `dark` | `oklch(0.13 0.005 70)` | `dark` |

Instant application ✅, persistence across reload ✅, `color-scheme` update ✅ — **visible result ❌**.
Worse, the UI *asserts* light is active while staying dark: the header icon switches to the Moon
and the appearance page styles 浅色 as the selected `primary` pill. This is visible in
[ui-light/settings_appearance_desktop.png](tools/ui-light/settings_appearance_desktop.png) (cream
"selected" 浅色 pill on an all-dark page) versus
[ui-light-forced/settings_appearance_desktop.png](tools/ui-light-forced/settings_appearance_desktop.png)
(what light should look like).

---

## (b) Contrast defects

WCAG 2.1 AA: 4.5:1 normal text, 3:1 large (≥24px, or bold ≥18.66px).
Colour maths were validated against Chrome's own canvas rasteriser on every page
(`converterCheck.agrees = true`, max deviation **0.7/255** across 12 samples including the exact
token values). This Chrome returns `oklch()`/`oklab()` verbatim from `getComputedStyle`, so
OKLab→sRGB conversion is explicit.

| Theme run | Pages | Nodes measured | AA violations | Share | Worst |
|---|---|---|---|---|---|
| `light` (as shipped: renders dark) | 32 | 808 | **16** | 2.0% | 3.66:1 |
| `dark` (as shipped) | 32 | 808 | **16** | 2.0% | 3.66:1 |
| `light-forced` (intended light palette) | 5 | 185 | **56** | **30.3%** | 3.76:1 |

### D1 — Danger buttons fail AA in **both** themes (real, shipping, theme-independent)

`oklch(0.99 0 0)` = **`rgb(252, 252, 252)`** on `--danger` **`rgb(239, 68, 69)`** = **3.66:1**
(needs 4.5:1), 14px/500. **16 text nodes per theme (32 across the two runs)** — the only distinct
violation signature in the shipping UI, present identically in the light and dark runs because
`--danger: oklch(0.637 0.208 25)` is declared the same in both token blocks.

| Selector | Text | Routes |
|---|---|---|
| `button.button--sm.button--danger` | `清理回收站` | `/trash` desktop+mobile |
| `button.button--sm.button--danger` | `永久删除` (×6 + ×6) | `/trash` desktop+mobile |
| `button.button--sm.button--danger` | `禁用` | `/settings/users` desktop+mobile |

These are destructive-action buttons; 3.66:1 is below AA but not unreadable.

### D2–D15 — Defects that appear the moment light actually renders (measured, latent)

Measured by removing the stale `data-theme` attribute at runtime (DOM-only; no source change), so
these are the **intended** light palette's real numbers. `light-forced` = 56/185 nodes failing.
**Every one of the 13 signatures has a single root cause: the light `--muted` token
`oklch(0.556 0 0)` → `rgb(115, 115, 115)`.**

| # | Ratio | Need | Colour on background | Size/weight | Selector (example) | Text | Routes |
|---|---|---|---|---|---|---|---|
| D2 | **3.76:1** | 4.5 | `rgb(115,115,115)` on `rgb(229,229,229)` | 14px/500 | `kbd.kbd` | `Ctrl K` | files, storage, settings-appearance, tasks |
| D3 | **3.76:1** | 4.5 | `rgb(115,115,115)` on `rgb(229,229,229)` | 14px/500 | `div.tabs__tab` | `QR 码` | login |
| D4 | **4.28:1** | 4.5 | `rgb(115,115,115)` on `rgb(243,243,243)` | 12px/400 | `span.text-xs.tabular-nums.text-muted` | `1` | tasks |
| D5 | 4.49:1 | 4.5 | `rgb(115,115,115)` on `rgb(249,249,249)` | 14px/500 | `span.overflow-hidden.whitespace-nowrap…` | sidebar nav `文件` | files, storage, settings-appearance, tasks (22 nodes) |
| D6 | 4.49:1 | 4.5 | same | 12px/500 | `p.truncate.text-xs.text-muted` | `@fixture` | files, storage, settings-appearance, tasks |
| D7 | 4.49:1 | 4.5 | same | 12px/400 | `span.hidden.text-xs.text-muted` | `4.6 MB` | files |
| D8 | 4.49:1 | 4.5 | same | 12px/400 | `span.hidden.min-w-0.text-xs` | `2026/7/22 20:00:00` | files |
| D9 | 4.49:1 | 4.5 | same | 10px/400 | `p.text-[10px].uppercase.tracking-[0.16em]` | `云盘` | files, storage, settings-appearance, tasks |
| D10 | 4.49:1 | 4.5 | same | 10.9px/600 | `p.px-2.text-[0.68rem].font-semibold` | `账户` | settings-appearance (5 nodes) |
| D11 | 4.49:1 | 4.5 | same | 14px/500 | `span` | `概览` | settings-appearance (8 nodes) |
| D12 | 4.49:1 | 4.5 | same | 14px/400 | `p.mt-4.max-w-lg.text-sm` | login blurb | login |
| D13 | 4.49:1 | 4.5 | same | 14px/400 | `p.mt-1.text-sm.text-muted` | storage description | storage, tasks |
| D14 | **4.12:1** | 4.5 | `rgb(43,43,43)` on `rgb(116,114,112)` | — | `.glass-panel` | panel text | file-preview dialog (not in sweep; probed directly) |
| D15 | **3.09:1** | 4.5 | `rgb(43,43,43)` on `rgb(97,94,91)` | — | `.glass-panel-lg` | panel text | file-preview dialog (not in sweep; probed directly) |

**Is 4.49:1 a real failure or rounding?** It is a genuine knife-edge fail. The token resolves to
`oklch(0.556 0 0)`; `0.556³ = 0.171880`, and the light background `oklch(0.982 0 0)` gives
`0.982³ = 0.946966`, so the ratio is `0.996966 / 0.221880 = **4.4933:1**` — below 4.5 by 0.007.
`--muted` is calibrated to sit *exactly* on the AA boundary for text directly on `--background`,
and therefore fails on every surface on the other side of it (D2–D4 on `--default`/raised
surfaces fail by 0.74 and 0.22 respectively, which are unambiguous). Actionable threshold: to clear
4.5:1 on the `--default` chip (`rgb(229,229,229)`) as well, light `--muted` needs OKLab
`L ≤ 0.513`, i.e. roughly `oklch(0.51 0 0)` or darker (currently `0.556`).

**D14/D15 detail** (measured live via
[tools/ui-audit-theme-leftovers.mjs](tools/ui-audit-theme-leftovers.mjs), since these classes only
appear in the file-preview dialog, which the route sweep does not open). `globals.css` defines
`.glass-panel { background-color: oklch(21% .008 70 / .6) }` and
`.glass-panel-lg { … oklch(23% .01 70 / .7) }` with **no light-theme override**
([globals.css:141-159](src/teldrive-2/ui/src/styles/globals.css#L141-L159)). Composited over the
light page (`rgb(249,249,249)`, luminance 0.947) they become mid-grey panels (`rgb(116,114,112)`,
`rgb(97,94,91)`) carrying the light theme's *dark* foreground — 4.12:1 and 3.09:1. In dark mode the
same panels are fine (16.51:1, 15.83:1).

### Still present after the rebuild

On the 11:59 build / 12:01 server, re-verified at 12:02: the switcher trace is **identical**
(`class=light`, `data-theme=dark`, `body` `oklch(0.13 0.005 70)`, `storedTheme=light`), and the new
bundle still emits `<html … class="dark" data-theme="dark" style="color-scheme: dark;">`, still
places the globals dark block after the light `:root` block, and still contains the hardcoded
`.glass-panel{background-color:oklch(21% .008 70/.6)}`.

---

## (c) Real user-visible problems vs. acceptable

**Real, user-visible (fix):**

1. **Light theme is a no-op** (D-A). A user who selects 浅色 — including anyone who needs a light
   UI, e.g. for low vision or bright-room use — gets a fully dark app, and the UI *tells them* light
   is selected. Highest impact finding in this audit; also the reason the light palette went
   unexercised.
2. **`--muted` secondary text fails AA in light mode** (D2–D13): 30.3% of measured text nodes on the
   5 forced-light pages, worst 3.76:1. Sidebar navigation labels, file sizes/dates, descriptions,
   `Ctrl K` hint, login tabs. D2–D4 (3.76/4.28) are plainly visible; D5–D13 (4.49) are knife-edge.
3. **Danger buttons at 3.66:1 in both themes** (D1) — 16 nodes, destructive actions.
4. **`.glass-panel` / `.glass-panel-lg` have no light override** (D14/D15): 4.12:1 and 3.09:1, plus
   a dark panel on a light page. Currently only reachable inside the file-preview dialog.

**Acceptable / correctly excluded (not counted as violations):**

- **38 dark-brown `rgb(138,56,25)` surfaces** — this is the light theme's `--accent`
  (`oklch(0.45 0.12 40)`) primary button. A dark accent button on a light page is intentional
  design, not an unthemed leftover; white on it is **7.66:1**. My wrong-side-background heuristic
  flagged these 11 times on the forced-light pages, and I excluded them after identifying them as
  the accent token — they are **false positives**.
- **8 disabled-control nodes per theme at 3.34:1** (`rgb(125,106,79)` on `rgb(247,199,144)`, a
  `disabled` primary button; plus 1 at 3.11:1 in light). WCAG-exempt as inactive controls; reported
  separately, never counted.
- **`.warm-gradient`** is defined in `globals.css` but referenced by **no component** (dead CSS) —
  no user impact, despite its dark end-stop.
- **Sonner toasts stay dark in light mode** — `<Toaster theme="dark">` with a fixed
  `oklch(0.21 0.008 70 / 0.85)` background ([main.tsx:51-63](src/teldrive-2/ui/src/main.tsx#L51-L63)).
  Over a light page it composites to `rgb(60,57,55)` with white text = **10.0:1**, so it is
  *readable*; it is a visual-consistency wart (a dark toast in a light app), not a contrast defect.

**Measurement blind spots — none material.** `transparentText` = 0, text over gradients/images = 0,
unparseable colours = **0** (after adding `oklab()` support; 65 nodes were initially unmeasured),
`.sr-only`/`aria-hidden` nodes skipped by design.

---

## (d) Screenshots

Light theme: [tools/ui-light/](tools/ui-light) · dark theme: [tools/ui-dark/](tools/ui-dark) ·
forced-light diagnostic: [tools/ui-light-forced/](tools/ui-light-forced) (32 + 32 + 5 PNGs).

Worst / most informative pages:

| What it shows | Path |
|---|---|
| **The headline bug**: 浅色 marked as the selected primary pill on an all-dark page | [ui-light/settings_appearance_desktop.png](tools/ui-light/settings_appearance_desktop.png) |
| Same page in the *intended* light palette (correct rendering, and the 4.49:1 muted text) | [ui-light-forced/settings_appearance_desktop.png](tools/ui-light-forced/settings_appearance_desktop.png) |
| D1 — 3.66:1 white-on-red destructive buttons (`永久删除`) | [ui-dark/trash_desktop.png](tools/ui-dark/trash_desktop.png) |
| Forced-light file browser — muted file sizes/dates at 4.49:1 | [ui-light-forced/files_desktop.png](tools/ui-light-forced/files_desktop.png) |
| Forced-light login — `QR 码` tab at 3.76:1 | [ui-light-forced/login_desktop.png](tools/ui-light-forced/login_desktop.png) |
| Forced-light storage/tasks — muted descriptions at 4.49:1 | [ui-light-forced/storage_desktop.png](tools/ui-light-forced/storage_desktop.png) |

**Mobile:** captured for every route in both themes
(e.g. [ui-light/files_mobile.png](tools/ui-light/files_mobile.png),
[ui-dark/trash_mobile.png](tools/ui-dark/trash_mobile.png)); the same defects reproduce at 390×844.

---

## Confidence and limits

- **High confidence** on the light-theme failure: 32/32 pages, identical token hashes, a direct
  source explanation, a byte-offset check in the built CSS, an instant/persisted-but-invisible
  switcher trace, and a 0.10% vs ~100% pixel-difference contrast between "light applied" and "a real
  light theme". Root cause is reproduced live on the post-rebuild build.
- **High confidence** on the contrast numbers: the OKLab→sRGB conversion was cross-checked against
  Chrome's own rasteriser on every page (max deviation 0.7/255), and the 4.4933:1 knife-edge was
  confirmed by hand.
- **Alpha compositing** is exact for solid `background-color` chains (Porter-Duff "over"), including
  the 60%/70%/85% translucent panels. No page in the sweep needed the white-canvas fallback
  (`incompleteBg` never set).
- **Limits:** (i) the shipping-UI violation count of 16 is *only* the danger-button issue because the
  light palette never renders — the 56/185 forced-light figure comes from **5 desktop routes**, not
  all 16, and would likely be higher across the full set. (ii) `light-forced` is a DOM-level
  simulation of the fix (attribute removed at runtime); it proves the palette's numbers but cannot
  prove that removing the attribute in `index.html` leaves nothing else broken (e.g. HeroUI
  first-paint/hydration, or the `color-scheme` inline style). (iii) D14/D15 were probed with the real
  CSS classes on a synthetic node inside the live page rather than by opening the real dialog.
  (iv) The file browser renders slightly different rows between runs, so the exact per-route node
  counts in D5–D13 vary by a few; the worst ratios (3.76, 4.12, 4.28, 4.49) were stable across runs.
  (v) Hover/focus/active states, modal contents, the PDF/e-book readers and error toasts were not
  exercised.
- **Not attempted:** fixes. Per the task, this audit is read-only in `src/`.

### Suggested fix direction (not applied)

Delete the hardcoded `class="dark" data-theme="dark" style="color-scheme: dark"` from
[ui/index.html](src/teldrive-2/ui/index.html) so `next-themes` owns the root class and
`color-scheme`; or drop the `[data-theme="dark"]` half of the selectors in
[globals.css:99-136](src/teldrive-2/ui/src/styles/globals.css#L99-L136). Also worth doing while in
there: add light overrides for `.glass-panel`/`.glass-panel-lg`, darken the light `--muted` toward
`oklch(0.51 0 0)`, make light `--danger`/`--danger-foreground` clear 4.5:1, and give `<Toaster>` a
theme-aware background. The `<meta name="theme-color" content="#1a1a1a">` in `index.html` is also
dark-only.
