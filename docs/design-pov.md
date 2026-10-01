# Design POV and system: benpriddy.com

Researched 2026-10-01 by driving headless Chromium (GPU on) through recent Awwwards
winners at 1440x900 and 390x844@2x. Screenshots live in
`/tmp/claude-1000/7f16cd7b-787f-4549-be6d-f24a92e7e874/scratchpad/research/`
(scratch, not committed). Filenames: `<site>-d-1.png` is the desktop first viewport
after load, `-d-scroll1/2` is after scrolling about 1 and 2.5 screens,
`-m-1.png` is the phone first viewport, `-motion-<ms>.png` is a load-sequence frame,
`-hover-*` and `-click-*` are interactions. Easing values were read from computed
`transition-timing-function` on the live pages.

Everything below is principle, not copying. No assets, code or layouts are lifted
from any site.

---

## 1. Research log

| # | Site | Award | URL |
|---|---|---|---|
| 1 | Jesper Landberg (portfolio) | SOTD, 29 Sep 2026 | http://jesperlandberg.com/ |
| 2 | Gil Huybrecht (portfolio) | SOTD, 21 Sep 2026 | https://gilhuybrecht.com/ |
| 3 | Léo Parpeix (portfolio) | SOTD, 14 Sep 2026 | https://leoparpeix.com/ |
| 4 | ERA Residence (real estate) | SOTD 31 Aug 2026, on the Sites of the Month list | https://www.era-residence.com/ |
| 5 | Lama Lama (studio) | SOTD 20 Jul 2026, on the Sites of the Month list | https://lamalama.com/ |
| 6 | Floema (product) | SOTD 13 May 2026, on the Sites of the Month list | https://www.floema.com/en |
| 7 | Lando Norris (personal/athlete) | SOTD 17 Nov 2025, **Site of the Year 2025** (latest SOTY) | https://landonorris.com/ |
| 8 | Igloo Inc. (WebGL product) | **Site of the Year 2024** | https://www.igloo.inc/ |
| 9 | Anime.js (dev tool/library) | SOTD 6 May 2025, on the Sites of the Month list | https://animejs.com/ |
| 10 | Immersive Garden (studio) | SOTD 7 Jan 2025, on the Sites of the Month list | https://immersive-g.com/ |

The award details came from each site's Awwwards page. All carry the DEV
(Developer) badge in the listings. Other current SOTDs I saw but did not visit:
Colonia Zacamil, Cominvi, Butter, Realevate, The Tie Break, Pensatori Irrazionali,
Boc.Studio, Edoardo Lunardi, Jordan Gilroy. For reference, the
current site is at `benpriddy-d-1.png` and `benpriddy-m-1.png`.

### 1. Jesper Landberg: SOTD
Shots: `jesper-d-load0.png`, `jesper-d-1.png`, `jesper-d-scroll1.png`, `jesper-hover-b.png`, `jesper-m-1.png`, `jesper-motion-*.png`
- **Type:** one grotesk at tiny sizes (13px body, 27px H1). The chrome is 11px uppercase with slightly tight tracking ("JESPER LANDBERG", "PROFILE", "FEATURED / FULL", "NEWSLETTER"). The name is not a headline. The work is the headline.
- **Layout:** the four corners hold text and the centre holds the work. Generous margins of about 77px. On the phone the corners stay and the work becomes a stack of rounded cards.
- **Color:** pure black. Colour comes only from the project imagery.
- **WebGL:** a perspective grid floor receding to a horizon, and project cards bent on a cylinder that you drag (the cursor is `grab`). Loading starts with three white pills on black and resolves into the carousel.
- **Motion:** about 2s from load to an intro camera move. Drag has inertia. Easing is `cubic-bezier(0,0,.2,1)` at 300 to 500ms for UI.
- **Premium because:** the restraint in the chrome makes the 3D feel like an object, not a gimmick. "FEATURED / FULL" is a two-state toggle written as text, not a button.

### 2. Gil Huybrecht: SOTD
Shots: `gil-d-1.png`, `gil-d-scroll1.png`, `gil-d-scroll2.png`, `gil-motion-*.png`, `gil-m-1.png`
- **Type:** **one family, one size, one weight** (about 16px, weight 500) for everything: name, nav, services, awards list, project titles. Hierarchy comes only from position and grouping. This is the most radical and most transferable idea in the set.
- **Layout:** a strict 7-column grid with a 12px margin. The top row is a 6-column info strip: Name / Availability / Services (an indented list) / Recognition (an indented list) / Profile / Email. Thumbnails sit on the grid but are vertically staggered like a cascade. Every project has a numbered set of frames (1, 2, 3, 4) with small right-aligned numerals.
- **Color:** black with white text. Colour comes only from the screenshots.
- **Micro-details:** bottom-corner segmented toggles ("Grid | Gallery", "Normal | Star Wars"), with the inactive option at 40% opacity in a dark pill with a 4px radius. Load is a centred black screen with a "%" counter (`gil-hover-b.png` shows 83% at about 5s, which is too long).
- **Motion:** `cubic-bezier(.23,1,.32,1)` (quint-out) at 200ms is used on 84 elements. It is one curve, used consistently.
- **Phone:** the same single size scaled up to about 28px, two-up thumbnails, and huge vertical gaps. It still feels like the same site.

### 3. Léo Parpeix: SOTD
Shots: `leo-d-1.png`, `leo-click-b.png`, `leo-click-scroll-mid.png`, `leo-click-scroll.png`, `leo-m-1.png`
- **Type:** a neo-grotesk "text" face at 14px for everything except a huge uppercase condensed "title" face (162px, line-height 0.9) for section words ("FRENCH", "INTERACTIVE"). The scale contrast is about 11 to 1.
- **Layout:** the loader *is* the layout. "Léo Parpeix" sits at x=40, "Art director, Interactive designer" at x=257 and "World building" at x=960, all on the vertical centre line. After entering, the same strings slide to the top and become the header. That is a perfect continuity trick. The hero statement ("Driven by detail. Obsessed with seamless motion.") sits bottom-left at about 24px and the rest of the viewport is empty light grey (#F6F6F6).
- **Color:** off-white, near-black green-tinted text, and a **single acid yellow** pill ("CLICK — TO ENABLE SOUND") that follows the cursor.
- **Motion:** `cubic-bezier(.16,1,.1,1)` at 350ms, 500ms, 1s and 1.4s, applied to 250+ elements. It is an expo-out family with a duration ladder.
- **Premium because:** there is nothing on screen that doesn't need to be there, and the cursor-follower carries the one colour.

### 4. ERA Residence: SOTD and Sites of the Month list
Shots: `era-d-1.png`, `era-d-scroll1.png`, `era-motion-*.png`, `era-m-1.png`
- **Type:** three voices. An ultra-condensed high-contrast didone in uppercase for the wordmark (173px, tracking -0.024em, line-height 0.87). A flourished script for one word ("Estepona"), also used as a giant 4% ghost watermark behind everything. A wide extended sans at 10px uppercase with +0.05em tracking for labels. "C O S T A ··· D E L  S O L" is spaced across the viewport.
- **Layout:** strictly centred and symmetrical. There is a **hairline frame inset 20px with chamfered corners** around the whole viewport, on both desktop and phone. A thin vertical rule acts as the scroll cue.
- **Color:** a single deep aubergine (#330D22) with bone text. After scrolling, full-bleed photography takes over.
- **Motion:** the screen holds plain colour for about 1.5s, then the type is drawn in. Easing is `cubic-bezier(.25,1,.5,1)` at 800ms for reveals and `cubic-bezier(.76,0,.24,1)` at 800ms (in-out) for panels.
- **Premium because:** of the confidence of one colour and one typographic gesture per screen. Even the cookie card is set in the script face.

### 5. Lama Lama: SOTD and Sites of the Month list
Shots: `lamalama-d-1.png`, `lama-motion-*.png`, `lamalama-d-scroll1.png`, `lamalama-d-scroll2.png`, `lamalama-m-1.png`
- **Type:** a grotesk (Suisse-like) for content, with uppercase bold display words at 72 to 80px and line-height 0.8. A **monospace for all metadata**: tags, nav labels, "( + )" toggles and "[ DIGITAL ]" bracketed section heads.
- **Layout:** the project index is a full-width list. Each row has a name (left), tag chips (a black chip with mono caps), a "( + )" expander, and a horizontal strip of thumbnails running off the right edge. Rows are separated by hairline rules at about 168px pitch. A floating centred nav capsule ("OUR GREATEST HITS" with a hamburger) changes its label per section.
- **Color:** a dark charcoal (#1A1C1C, not black) with warm off-white (#F9F4EB) text. Colour comes from the imagery.
- **Motion and texture:** the loader is a pixel-mosaic dissolve that resolves to a pixel logo (`lama-motion-7000.png`). A halftone/dot-matrix treatment is applied to video behind text. The phone shows a liquid macro video with **glitching mono strings** as a footer.
- **Premium because:** of the system feel. Mono labels, chips and brackets make it read like an instrument.

### 6. Floema: SOTD and Sites of the Month list
Shots: `floema-d-1.png`, `floema-d-scroll1.png`, `floema-d-scroll2.png`, `floema-m-1.png`
- **Type:** one rounded geometric grotesk at 57px, weight 400, tracking -0.04em, line-height 1.05 for headlines. Sentence case. It is calm.
- **Layout:** a centred headline in a WebGL field of product photos floating at different depths. Further down, sections are numbered ("02 / Made to Last") with a hairline rule that stops mid-screen.
- **Color:** warm paper (#F2EFEA) with a near-black aubergine ink (#241F21). The accents come from the product (terracotta, sage).
- **Nav:** pill buttons. They are generic and are the weakest part of the site.
- **Lesson:** WebGL depth plus warm paper reads premium. Pills read like a SaaS template.

### 7. Lando Norris: Site of the Year 2025
Shots: `lando-d-1.png`, `lando-d-scroll1.png`, `lando-d-scroll2.png`, `lando-hover-*.png`, `lando-m-1.png`
- **Type:** the wordmark mixes a sharp serif ("LANDO") with a heavy grotesk ("NORRIS"). Body is a variable grotesk (weights 200 to 900). Quotes are set in a **wide-tracked serif with mixed opacity per word**, so emphasis is done with tone, not weight. Captions are 9px uppercase ("MIAMI GP, 2024").
- **Layout:** the hero is a portrait with a 3D helmet visor that the cursor slices through (WebGL). Scrolling gives a loose, scattered photo collage on an olive-grey ground with topographic contour lines drawn across everything.
- **Color:** paper white turning to olive grey, with **one acid lime** (#D2FF00) used only on the Store button and the "tap to lock" control.
- **Motion:** `cubic-bezier(.65,.05,0,1)` at 750ms on 100 elements (a dramatic, slow-out-and-snap feel) and `cubic-bezier(.19,1,.22,1)` (expo-out) at 600ms. There are 21 canvases.
- **Premium because:** the WebGL is about the subject (his helmet, his face), and one loud accent sits against a muted, organic palette.

### 8. Igloo Inc.: Site of the Year 2024
Shots: `igloo-d-1.png` (wireframe intro), `igloo-d-scroll1.png`, `igloo-m-1.png`
- **Type:** almost none on the first screens. The scene is the message.
- **WebGL:** it opens as a glowing white wireframe igloo with numbered vertices and construction lines on cool grey, then resolves into a photoreal igloo on a sculpted ice field with drifting snow and floating wire boxes. It is fully art-directed and monochrome (blue-grey).
- **Lesson:** a **blueprint-to-real reveal** is a great load sequence. It uses the same object in two fidelities, so loading becomes narrative. It is also fully monochrome with zero UI colour.

### 9. Anime.js: SOTD and Sites of the Month list
Shots: `animejs-d-1.png`, `animejs-d-scroll1.png`, `animejs-d-scroll2.png`, `animejs-m-1.png`
- **Type:** a DIN-like grotesk at 64px, weight 700, line-height 0.86 for the headline, with a mono for nav and code ("DOCS", "EASINGS", "npm i animejs"). Mono is the developer voice.
- **Layout:** a classic left headline and right hero object. The object is a live instrument dial (ticks, rainbow ring, dot trail) that *demonstrates* the product. Scrolling gives an exploded 3D engine with a timeline scrubber (a tick ruler with one red playhead) bottom-right.
- **Color:** warm charcoal (#232120), not black. One red runs through as the brand accent, plus the rainbow ring as a deliberate one-off.
- **Lesson for Ben (a creative technologist):** *show the capability live* rather than describing it. The tick-ruler scrubber is a great micro-detail.

### 10. Immersive Garden: SOTD and Sites of the Month list
Shots: `immersive-d-load0.png`, `immersive-d-1.png`, `immersive-d-scroll1.png`, `immersive-d-scroll2.png`, `immersive-m-1.png`
- **Type:** a Times-like text serif at 56px, weight 400, line-height 1.1, centred, sentence case, with a small Helvetica for UI ("Menu", "See all projects", "Scroll down"). The logo uses spaced small caps.
- **Layout:** the loader is a single centred line, "logo · *Innovative digital experiences studio* · 100", that becomes the hero after load. This is the same continuity idea as Léo's. Then come centred statements over the background.
- **WebGL:** a white-on-white **bas-relief plaster sculpture** of birds and flowers on a mottled grey ground that shifts with scroll. It is monochrome and tactile, like paper grain made 3D.
- **Premium because:** the serif statement sits on a near-invisible but rich texture. Nothing competes with the sentence.

### Easing observed (computed styles)
| Site | Dominant curve | Durations |
|---|---|---|
| Léo Parpeix | `cubic-bezier(.16,1,.1,1)` | .35s, .5s, 1s, 1.4s |
| Gil Huybrecht | `cubic-bezier(.23,1,.32,1)` | .2s |
| ERA Residence | `cubic-bezier(.25,1,.5,1)` plus in-out `cubic-bezier(.76,0,.24,1)` | .4s, .8s |
| Lando Norris (SOTY) | `cubic-bezier(.65,.05,0,1)` plus `cubic-bezier(.19,1,.22,1)` | .6s, .75s |
| Jesper Landberg | `cubic-bezier(0,0,.2,1)` | .3s, .5s |

None of them uses `ease` or `linear` for UI. Each uses **one family of strong ease-outs** with a ladder of durations.

---

## 2. Point of view

What an award-level site looks and feels like in late 2026, as I observed it:

1. **One dominant gesture per view.** Each screen has exactly one loud thing: a giant word (ERA), a 3D object (Igloo), a portrait (Lando) or a sentence (Immersive Garden). Everything else is small and quiet. If two things shout, neither wins.

2. **Extreme scale contrast, few sizes.** Winners use two or three sizes, far apart. Léo goes from 14px to 162px. Gil uses literally one size. The middle sizes (20 to 32px) that template sites live in are mostly absent. Small type is *really* small (10 to 14px) and confident.

3. **Typography is the brand; imagery is content.** Chrome is typographic: names, labels and toggles are set as text, not as buttons or icons. Distinctive display faces (didone, condensed, text serif) are paired with a neutral grotesk, and a mono is used for metadata. Fewer than three families in total.

4. **Restraint in colour: a ground, an ink and one accent.** The ground is never pure #000 or #FFF on the editorial sites: charcoal #1A1C1C, warm charcoal #232120, paper #F2EFEA, aubergine #330D22. The accent appears on one or two elements only (Lando's lime Store button, Léo's yellow cursor pill, Anime's red playhead).

5. **The grid is visible through alignment, not boxes.** Strict columns (Gil's 7, Lama Lama's rows) are expressed by text left edges and hairline rules, never by cards with shadows. Asymmetry within the grid: content hugs the bottom-left or one column while the rest is empty.

6. **Whitespace is a material.** Léo leaves 80% of the first screen empty. Gil leaves 150px or more between project groups on the phone. Emptiness signals confidence and makes the one gesture land.

7. **Continuity is the transition.** The best load sequences move the *same* elements into place. Léo's centred line becomes the header. Immersive Garden's loader line becomes the hero. Igloo's wireframe becomes the real model. Nothing pops in from nowhere.

8. **Motion has one curve family and a duration ladder.** Strong expo/quint ease-outs (`(.16,1,.3,1)`-ish) for entrances, a crisp in-out for panels, and durations of 200, 350, 500, 800 and 1200ms. Motion is fast to start and slow to settle. It never uses `ease` or linear.

9. **WebGL depicts the subject; it is not decoration.** Lando's visor, Igloo's igloo, Anime's engine and Jesper's gallery of his own work. The 3D *is* the content or demonstrates the capability. It is monochrome or near-monochrome so type can sit on it.

10. **Systematic micro-details.** Numbering (01, 02, frame numerals), bracketed mono labels ("[ DIGITAL ]", "( + )"), hairline rules that stop short, a viewport frame, a tick-ruler scrubber, and segmented text toggles ("Grid | Gallery"). These signal that a designer made decisions, and they carry the identity on the phone when the WebGL can't.

11. **The phone is a recomposition, not a squeeze.** Gil scales the single size up, Era keeps the frame, Jesper turns the cylinder into a card stack. The identity survives at 390px because it lives in type, colour and rhythm.

12. **Texture over flatness, used sparingly.** Bas-relief plaster, halftone video, topographic lines and ghost script watermarks at 4% opacity. One texture per site, low contrast, never competing with text.

## 3. Anti-patterns: what reads as generic, template or AI-made

- A centred hero with "Hi, I'm X 👋" plus a gradient blob or aurora, and a CTA pair (filled plus outline).
- Inter, Poppins or Montserrat everywhere, or the "trendy pair" (Instrument Serif plus Inter, or Fraunces plus Inter) used without intent.
- Purple-to-blue or teal-to-violet gradients, neon glows, glassmorphism cards with `rounded-2xl` and drop shadows.
- Uniform 12 to 16px radius on everything; pill buttons as the default nav (Floema's one weak spot).
- Three-column "feature cards" with icons; emoji as icons; Lucide icons next to every label.
- Every block doing the same `fade-up 20px, 0.3s ease` on scroll; staggered children all at 100ms; `transition: all`.
- Fake "%" preloaders lasting more than 2s (even Gil's runs long); scroll-jacking that fights trackpads and phones.
- Laggy custom cursor blobs that hide the real cursor; magnetic buttons on everything.
- Pure #000 with pure #FFF, or low-contrast mid-grey body text (#888 on #111).
- A middle-of-the-road type scale (16, 20, 24, 32, 40) with no size dominant.
- Everything uppercase and letter-spaced, or nothing ever is.
- Several accent colours; an accent on headings, links, buttons and borders all at once.
- Filler copy ("Crafting digital experiences", "Passionate about…"), lorem, stock 3D blobs and generic "AI" orbs.
- WebGL as wallpaper (particles, noise fields) unrelated to the content, with text fighting it for contrast.
- Generic footers ("Made with ❤️", social icon rows) and cookie banners in the system font.

---

## 4. Design system

**Name:** *Instrument*. Ben is a creative technologist who heads AI at a creative
agency. The site should feel like a precise editorial object that also behaves like
a tool: a serif voice for what he says, a grotesk for how it works, and a mono for
the machine. Dark is the default (WebGPU experiments read better on dark), with a
real light mode.

### 4.1 Typefaces (self-hosted WOFF2; see section 8)
| Role | Family | Use |
|---|---|---|
| Display / voice | **Newsreader** (variable: `opsz` 6–72, `wght` 200–800, italic) | Name, page titles, ledes, experiment titles. Use the display optical size (`opsz` 72) at large sizes for hairline contrast, and italic for at most one emphasised word per view. |
| Text / UI | **Instrument Sans** (variable: `wght` 400–700, `wdth` 75–100) | Body, nav, buttons, admin. Use `wdth` 85 for tight UI labels. |
| Machine / meta | **Fragment Mono** (400, italic) | Indices (01, 02), dates, tags, streaming logs, status, keyboard hints. |

Why these: Newsreader is a working text serif from Production Type with a real
optical-size axis. At `opsz` 72 and weight 300 it becomes a sharp, high-contrast
display face (the ERA and Immersive Garden register) without being one of the
overused AI-era serifs. Instrument Sans is a neo-grotesk with slightly condensed
proportions and a width axis, so one file covers body and compressed labels.
Fragment Mono is a Helvetica-derived monospace, so it sits with the grotesk instead
of looking like a code editor. All three are OFL.

```css
--font-display: "Newsreader", "Iowan Old Style", "Times New Roman", serif;
--font-text: "Instrument Sans", ui-sans-serif, system-ui, sans-serif;
--font-mono: "Fragment Mono", ui-monospace, "SF Mono", Menlo, monospace;
```
Preload only the Latin `Newsreader` roman and `Instrument Sans` files
(`<link rel="preload" as="font" type="font/woff2" crossorigin>`). Use
`font-display: swap` with a metric-matched fallback (`size-adjust`,
`ascent-override`) for the serif so the name doesn't reflow.

### 4.2 Type scale (fluid, 390px to 1440px)
| Token | Value | Family / settings |
|---|---|---|
| `--t-mega` | `clamp(4.25rem, 1.2rem + 12.5vw, 13.5rem)` | Newsreader `opsz` 72, `wght` 300, `lh` .86, tracking `-0.045em`. **Name only.** |
| `--t-display` | `clamp(2.75rem, 1.6rem + 4.6vw, 6rem)` | Newsreader `opsz` 72, `wght` 320, `lh` .95, `-0.035em`. Page titles. |
| `--t-title` | `clamp(1.75rem, 1.35rem + 1.7vw, 2.875rem)` | Newsreader `opsz` 48, `wght` 360, `lh` 1.0, `-0.02em`. Experiment titles. |
| `--t-lede` | `clamp(1.375rem, 1.15rem + .95vw, 2rem)` | Newsreader `opsz` 24, `wght` 380, `lh` 1.22, `-0.01em`. Bio paragraphs. |
| `--t-body` | `clamp(1rem, .96rem + .18vw, 1.125rem)` | Instrument Sans `wght` 420, `lh` 1.5, tracking 0. |
| `--t-ui` | `0.875rem` | Instrument Sans `wght` 500, `wdth` 90, `lh` 1.2, `+0.005em`. |
| `--t-meta` | `0.6875rem` (11px) | Fragment Mono, uppercase, `lh` 1.3, `+0.06em`, `font-variant-numeric: tabular-nums`. |

Rules:
- At most **three** sizes are visible on any screen: mega or display, lede or body, and meta.
- `text-wrap: balance` on titles and `text-wrap: pretty` on paragraphs. The measure is 34ch for lede and 62ch for body.
- Uppercase only in mono meta. Never uppercase the serif.
- Weights: the serif lives at 300 to 400 and is never bold. The sans uses 420 for body and 500 for UI. Use 600 only in the admin.

### 4.3 Colour
Dark is the default (`color-scheme: dark light`). Light mode follows
`prefers-color-scheme` and can be overridden with `[data-theme]`.

| Token | Dark ("Ink") | Light ("Paper") | Use |
|---|---|---|---|
| `--bg` | `#0F0F0D` | `#F1EEE7` | page ground (warm, never pure) |
| `--bg-raised` | `#171714` | `#E8E4DB` | modal, sheets |
| `--fg` | `#ECE8DF` | `#151513` | primary text (contrast at least 15:1) |
| `--fg-2` | `#A8A398` | `#55524B` | secondary text (at least 7:1) |
| `--fg-3` | `#6E6A62` | `#8A867D` | meta and disabled. Use only for 11px mono that is non-essential, or for decoration. |
| `--rule` | `rgb(236 232 223 / .14)` | `rgb(21 21 19 / .14)` | hairlines |
| `--rule-strong` | `rgb(236 232 223 / .32)` | `rgb(21 21 19 / .4)` | focused or active rules |
| `--accent` | `#FF5B1F` "Signal" | `#D9400B` | **one accent** |
| `--accent-ink` | `#0F0F0D` | `#FFFFFF` | text on accent |
| `--scrim` | `rgb(8 8 7 / .62)` | `rgb(241 238 231 / .7)` | modal backdrop |

Accent budget: the live/streaming dot, the focus ring, the active-route marker, the
draft banner's leading rule, and the text caret in the builder. Never use it for
headlines, body links or large fills. The one exception is the builder's Submit
button when it is enabled. Links are `--fg` with a 1px underline at
`text-underline-offset: .2em` and `--rule-strong` colour, and the underline turns
`--fg` on hover.

### 4.4 Space and grid
- 4px base unit: `--s-1: 4px`, `--s-2: 8px`, `--s-3: 12px`, `--s-4: 16px`, `--s-5: 24px`, `--s-6: 32px`, `--s-7: 48px`, `--s-8: 64px`, `--s-9: 96px`, `--s-10: 144px`, `--s-11: 216px`.
- Fluid section rhythm: `--gap-section: clamp(96px, 6rem + 8vw, 216px)`.
- Page margin `--m: clamp(16px, 2.8vw, 40px)`. Gutter `--g: clamp(12px, 1.4vw, 20px)`.
- **12 columns** on desktop (1024px and up), 6 on tablet, 4 on phone. Content is placed by column lines, never centred in a max-width box. The exception is the modal.

### 4.5 Shape, rules, texture
- Radii: `--r-0: 0` for layout, images and sheets. `--r-1: 3px` for inputs, thumbnails and chips. `--r-pill: 999px` only for the floating "Make your own" button and status dots. No other radius.
- Rules: 1px `--rule`. Section rules are drawn **full-bleed** (margin to margin). Row rules in lists span from the index column to the right margin, so they stop short on the left like Floema's.
- Shadows: none in the main site. The builder sheet gets one ambient shadow, `0 40px 120px -40px rgb(0 0 0 / .6)`, in dark mode only.
- Grain: one static 256px tiling noise PNG (generated in-house and self-hosted at `/static/grain.png`), applied as `body::after` with `position: fixed`, `opacity: .05` in dark and `.035` in light, `mix-blend-mode: overlay` and `pointer-events: none`. It is never animated. Turn it off under `prefers-reduced-transparency` and in the admin.

### 4.6 Motion tokens
```css
--ease-out:    cubic-bezier(.16, 1, .3, 1);   /* expo-out: entrances, reveals */
--ease-quint:  cubic-bezier(.23, 1, .32, 1);  /* hovers, small UI */
--ease-inout:  cubic-bezier(.76, 0, .24, 1);  /* panels, wipes, sheets */
--ease-in:     cubic-bezier(.5, 0, .75, 0);   /* exits only */
--d-1: 150ms;  /* press, colour */
--d-2: 250ms;  /* hover */
--d-3: 450ms;  /* UI enter/exit, route out */
--d-4: 700ms;  /* sheet, route in */
--d-5: 1100ms; /* hero lines */
--stagger: 70ms;
```
Only `transform`, `opacity`, `clip-path` and `color` are animated. Never use
`transition: all`.

### 4.7 Z-layers
```
--z-base: 0        transcript / fallback content
--z-frontend: 10   the front-end iframe
--z-chrome: 20     parent-page chrome that must sit above front ends (none today)
--z-cta: 40        "Make your own version" button
--z-banner: 50     draft banner
--z-scrim: 80      modal backdrop
--z-modal: 90      builder sheet
--z-toast: 100     transient status
--z-grain: 110     grain overlay (pointer-events: none)
```

---

## 5. Components

### 5.1 Default front end (home), desktop at 1440px and up
The composition is three bands on the 12-column grid.

1. **Meta row** (top, `--m` from the edges, `--t-meta` mono uppercase, `--fg-2`):
   - columns 1–3: `BEN PRIDDY` (links home)
   - columns 4–6: `HEAD OF AI TECHNOLOGY — ANOMALY`
   - columns 7–9: local time `NYC 14:32` (tabular numerals, ticking each minute; an honest live detail)
   - columns 10–12, right-aligned: pages nav as `01 ABOUT  02 EXPERIMENTS  03 …`. The index numbers are `--fg-3` and the labels `--fg`. The active page has a 6px accent dot before it.
2. **Name** (the one gesture): `Ben` / `Priddy` on **two lines** at `--t-mega`, left edge on column 1, baseline anchored about `--s-8` above the fold. It occupies the bottom-left of the first viewport, and the top 55% of the viewport stays empty. On the right, aligned to the baseline of "Priddy" in columns 10–12, is one line of `--t-meta`: `( SCROLL )` or `INDEX ↓`. The default WebGPU front end may put its effect (for example the particle stream) **behind and around the name, reacting to it**: the name is the subject, as in principle 9.
3. **Bio** (after `--gap-section`): a mono label `( ABOUT )` in columns 1–3, aligned to the first line of the lede. Paragraphs at `--t-lede` in columns 5–11. If the first paragraph is short (for example "Coming soon."), set it in Newsreader italic. Later paragraphs drop to `--t-body` in columns 5–9 in `--fg-2`, so the scale contrast holds even inside the bio.
4. **Experiments index** (after `--gap-section`): label `( EXPERIMENTS — 0N )` in columns 1–3. Then a full-width list where each row is:
   - columns 1–2: mono index `01` (`--fg-3`)
   - columns 3–8: title at `--t-title` (serif)
   - columns 9–11: summary at `--t-body` in `--fg-2`, two lines max on desktop
   - column 12, right-aligned: `↗` or `VIEW` in mono
   - Row padding is `--s-6` top and bottom, with a 1px `--rule` above each row and one after the last.
   - **Hover:** the rule above brightens to `--rule-strong` and a second 1px `--fg` rule draws over it left to right (`scaleX` 0 to 1, `--d-3 --ease-inout`). The title shifts `translateX(.25em)` (`--d-2 --ease-quint`). The index turns `--accent`. The rest of the list dims to 55% opacity. A live preview (a canvas or still from that experiment) may follow the cursor at 280x175, `--r-1`, lagging with lerp 0.15. Pointer devices only.
5. **Footer:** a single meta row with `© 2026`, an email (`HELLO@…`) and `THIS SITE HAS N VERSIONS — YOU'RE SEEING #K`, a quiet nod to the rotation.

### 5.2 Default front end, phone at 390px
- Margin 16px, 4 columns.
- Meta row: `BEN PRIDDY` left and `INDEX` right (a button that opens a full-screen sheet listing the pages at `--t-display`, numbered). Time and role move under the name.
- Name: two lines at `--t-mega`, which is about 68px at 390px; "Priddy" is about 300px wide. It bottom-anchors in the first viewport (`min-height: 100svh`, content justified to the end, `--s-9` bottom padding to clear the CTA).
- Bio: lede at about 22px across the full width. The label sits above it, not beside it.
- Experiments: each row stacks as index plus title on one line, then the summary below in `--fg-2`, with a rule between rows. There is no hover preview; tapping goes to the experiment.
- Vertical gaps stay huge (96px or more). Don't compress them on the phone.

### 5.3 Plain HTML fallback (no WebGPU, no JS, before the front end loads, screen readers)
This is **the same layout as 5.1 and 5.2 with the same tokens and fonts**, rendered by
Go templates from the transcript. It is the identity, and the WebGPU front end is an
enhancement layered on top. Specifics:
- The `--t-mega` name, meta row, bio and experiments list are all pure HTML/CSS. The grain is the only texture.
- The load sequence (section 6) runs on CSS animations only. It is skipped entirely when `html.fe-loading` hands off quickly, so the visitor never sees two intros.
- Because both the fallback and the default front end share the same composition, the iframe can cross-fade in over the fallback (opacity, `--d-4`) with nothing jumping. This is the continuity principle.
- Not found: `--t-display` "Not found." with a mono `← HOME` link. Nothing else.

### 5.4 "Make your own version" button (fixed bottom-right)
- Position: `right: max(var(--m), env(safe-area-inset-right))`, `bottom: max(var(--m), env(safe-area-inset-bottom))`, `z: --z-cta`.
- Shape: a pill 44px high (48px on touch) with padding `0 18px 0 14px` and a 1px `--rule-strong` border. Background `color-mix(in oklab, var(--bg) 72%, transparent)` with `backdrop-filter: blur(14px) saturate(1.2)` (falling back to solid `--bg-raised`).
- Content: an 8px **accent dot**, then the label in Instrument Sans `--t-ui`: "Make your own version". After a gap, a mono `⌘K` hint (desktop only, `--fg-3`).
- Idle: the dot "breathes" (opacity .55 to 1, 2.4s, ease-in-out, infinite). This is the only perpetual motion in the chrome, and it is off under reduced motion.
- Hover and focus: the fill wipes in from the left (`clip-path: inset(0 100% 0 0)` to `inset(0)`, `--d-3 --ease-out`) to `--fg`, and the text becomes `--bg`. The label does a **slot roll**: the line moves up 100% and a duplicate line arrives from below (`--d-3 --ease-out`, 30ms per-word stagger). Press: `scale(.97)`, `--d-1`.
- Phone: the label shortens to "Make your own". It hides while the visitor is scrolling down quickly and reappears on scroll-up or idle (translateY 140%, `--d-3`).
- Arrival: it fades and rises 8px at the end of the load sequence (+1200ms), never before the name.

### 5.5 Builder modal ("Studio") over the live site
The site stays visible and alive behind it: you are editing *this* site, not filling
in a form somewhere else.

**Desktop (1024px and up): a right-docked sheet**
- The sheet is inset 12px from the top, right and bottom. Width `clamp(440px, 36vw, 560px)`. Background `--bg-raised`, radius `--r-0` (square), border-left `1px --rule`, ambient shadow. The site behind gets `--scrim` and the iframe wrapper scales to `.985` with `transform-origin: left center`. Clicking the scrim closes the sheet.
- **Header** (56px): mono meta `STUDIO` · `DRAFT 03 / REV 7`, a status on the right (`● STREAMING` with an accent dot when the model is working, `○ IDLE` otherwise), and a close button (`ESC` in mono with an × glyph).
- **Versions strip** (directly under the header, 112px): a horizontal **contact sheet** of the visitor's front ends. Each is a 120x75 thumbnail (`--r-1`, 1px `--rule`) with a mono caption `01 · 3 REV`. The active one has a 1px `--fg` outline plus an accent dot. Selecting one reveals its revisions as a **tick ruler** beneath (a row of 1px ticks, one per revision, with the current revision as a taller accent tick). This borrows the scrubber idea, not its look. Hovering a tick shows `REV 5 · 14:02 · "make it brutalist"` in mono. A final `+ NEW` tile starts a fresh front end.
- **Transcript** (scrolling, flex 1): the visitor's prompts are set in **Newsreader at `--t-lede` × .8**, as statements, with no bubbles. The model's replies are Instrument Sans `--t-body` in `--fg-2`. Tool activity is a **mono log**, indented with a rule on its left:
  `WRITE  index.html      +142`
  `WRITE  shaders/bg.wgsl +38`
  `PREVIEW  ok · 16ms first frame`
  Each line types in (opacity and an 8px x-shift, `--d-2`, 40ms stagger). The newest line ends with a blinking accent block caret. Errors show as `ERROR` in `--fg` with an accent left rule, not red boxes.
- **Progress** (while streaming): a 1px accent line along the top edge of the sheet, indeterminate. A 30%-wide segment travels left to right in 1.4s with `--ease-inout`, looping. When a revision lands, the background iframe **cross-fades to the new revision** (`--d-4`) and the mono log prints `LIVE  rev 8 →`. The visitor watches the site change behind the panel. That is the magic moment, so design for it.
- **Composer** (pinned bottom): a borderless textarea at Newsreader `--t-lede` × .85, with italic `--fg-3` placeholder copy: *"Describe the site you want — a mood, a reference, a rule."* Underneath is a hairline, then a row: the left side shows mono hints `↵ SEND · ⇧↵ NEW LINE · ⌘K CLOSE` with limits (`4 / 20 TODAY`). The right side has two buttons:
  - `View live` (text button, `--t-ui`, underlined), which closes the sheet and shows the draft with the banner.
  - `Submit for review` (a primary rectangle, `--r-1`, 40px high, `--accent` background, `--accent-ink` text). It is disabled (outline only, `--fg-3`) until the draft has at least one revision. Confirming happens inline: the button text becomes `Submit rev 7? Confirm / Cancel`. There is no second modal.
- Empty state (first open): the transcript shows one serif line, *"This site is rebuilt by its visitors. Describe yours."*, and three mono "starter" chips (`BRUTALIST`, `MADE OF WATER`, `A 1994 GEOCITIES PAGE, BUT GOOD`). Clicking a chip fills the composer and doesn't send.

**Phone: a bottom sheet**
- It rises to 92svh and the top 8svh shows the live site dimmed. There is a 4x36px grabber. Drag down closes it (with velocity at least 0.5 or 40% travel).
- The order is header, then versions strip (horizontal scroll, snap), then transcript, then composer. The composer sticks above the keyboard (`interactive-widget=resizes-content`; use `visualViewport` to keep it docked).
- Submit and View live become a two-button row below the textarea, full width.

**Accessibility:** use `<dialog>` with `showModal()` for focus trapping and the Esc key. The heading is `aria-labelledby` on "Studio". The log is `aria-live="polite"` and only announces "revision N ready", not every token. Return focus to the CTA on close.

### 5.6 "You're viewing your draft" banner
- This is a **tape**, not a toast: fixed at the top, full width, 32px, `--z-banner`. Background `--bg` at 88% with blur, a 2px `--accent` rule along its bottom edge, and mono `--t-meta` text.
- Left: `● YOUR DRAFT — ONLY YOU CAN SEE THIS`. Centre (desktop only): `DRAFT 03 · REV 7`. Right: `BACK TO STUDIO` and `EXIT DRAFT` as underlined mono links.
- The page content is offset by 32px (`scroll-padding-top`) so it never covers the meta row.
- Phone: one line, `● YOUR DRAFT` on the left and `STUDIO · EXIT` on the right.
- Enter: it slides down from -100% (`--d-3 --ease-out`) after the draft's first frame.

### 5.7 Admin (lighter touch)
- Same tokens and fonts, light mode by default, **no grain, no WebGL, no load sequence**. Motion is limited to `--d-1` colour changes.
- Instrument Sans at 14px throughout (weight 600 allowed for table headers). Fragment Mono for ids, slugs, timestamps and token counts. Newsreader only for the page `h1` at `--t-title`, which keeps the family resemblance.
- Tables: full width, 1px `--rule` row lines, no zebra stripes, 40px rows, numeric columns right-aligned in mono.
- Review queue: a two-pane layout with the list on the left (thumbnail, mono `DRAFT 03 · REV 7 · 2026-10-01`) and on the right the preview iframe on top with the chat transcript and source tabs underneath. Approve is a `--fg`-filled button and Reject is an outline button. Neither uses the accent.
- Forms: labels in mono meta above the fields. Inputs have a bottom border only (`--rule-strong`) that turns `--fg` on focus, with the accent focus ring.

---

## 6. Motion

### Load sequence (first visit per session; about 1.4s total)
Text is in the DOM and readable from the first paint. The sequence is an enhancement
only: no preloader and no counter.

| t (ms) | What | How |
|---|---|---|
| 0 | ground colour plus grain | instant |
| 60 | meta row | opacity 0→1, `--d-3 --ease-out`, items staggered 50ms |
| 120 | name, line 1 "Ben" | each line sits in an `overflow: clip` mask and goes `translateY(105%)` → 0, `--d-5 --ease-out` |
| 200 | name, line 2 "Priddy" | same, +80ms |
| 650 | WebGPU layer (default front end) | cross-fades in over the fallback, `--d-4`. The effect *originates from the letterforms*: particles emit from the name. |
| 750 | `( SCROLL )` cue and the first rule | rule `scaleX(0→1)` from the left, `--d-4 --ease-inout` |
| 1150 | "Make your own version" button | opacity plus `translateY(8px)` → 0, `--d-3 --ease-out` |

On return visits within the session, everything shows at 0 with a single 250ms fade.

### Scroll
- Native scrolling only. No scroll-jacking and no smooth-scroll library.
- Section reveals use CSS scroll-driven animations (`animation-timeline: view()`, `animation-range: entry 0% entry 40%`), with an IntersectionObserver fallback. Labels fade, ledes rise 24px, and list rules draw left to right with a 60ms stagger per row. **Each element reveals once.**
- One parallax only: the name drifts up at 0.85× scroll speed as it leaves (transform, `will-change` only while in view).

### Hover
- Links: the underline colour goes from `--rule-strong` to `--fg`, `--d-2 --ease-quint`.
- Nav items: the index number goes from `--fg-3` to `--accent`, and the label stays put.
- Experiment rows: see 5.1.
- CTA: see 5.4.
- Hover effects are guarded with `@media (hover: hover) and (pointer: fine)`.

### Modal (Studio)
- Open: the scrim fades in 0→1 (`--d-3 --ease-out`). The iframe wrapper scales to `.985` (`--d-4 --ease-out`). The sheet does `clip-path: inset(0 0 0 100%)` → `inset(0)` together with `translateX(24px)` → 0 (`--d-4 --ease-out`). Contents (header, strip, transcript, composer) fade and rise 12px in sequence, with `--stagger` starting at +120ms. The composer is focused at +300ms.
- Close: the reverse with `--ease-in` at `--d-3`, and no stagger. Exits are always faster than entrances.
- Phone: the sheet goes `translateY(100%)` → 0 over `--d-4` with `--ease-out`. Drag follows the finger 1:1 and releases with `--ease-out` over 350ms.
- Revision lands: the iframe behind cross-fades between the old and new revision (two iframes swap opacity, `--d-4 --ease-inout`).

### Route transitions
- On the plain HTML site, cross-document **View Transitions**: `@view-transition { navigation: auto; }`. The meta row and name carry `view-transition-name` so they persist. The old main content leaves with opacity 1→0 over 200ms `--ease-in`. The new content arrives with opacity plus `translateY(16px)` → 0 over `--d-4 --ease-out`. This is CSS-only, so it is CSP-safe.
- Inside front ends, the parent calls `onRoute`. The default front end uses the same timings in its own renderer.
- Never fade the entire page to black between routes.

### Reduced motion
`@media (prefers-reduced-motion: reduce)`: all durations go to 0, except opacity
fades capped at 150ms. There is no translate, scale, clip-path wipe, parallax, slot
roll, breathing dot or cursor-follow preview. The progress line becomes a static
accent line with a mono `WORKING…` label. The WebGPU default front end renders a
static frame (or a slow ambient drift under 0.2 px/frame), and the host passes the
preference through.

---

## 7. Builder taste brief (for the front-end generator's system prompt; about 240 words)

> **Taste bar.** Whatever style the visitor asks for, execute it like an award-winning studio would. Commit fully to their direction; these are principles, not a house style, and never imitate a specific existing site.
> - **One dominant gesture per view**: one huge word, object or image; everything else small and quiet.
> - **Extreme scale contrast, few sizes**: at most three type sizes on screen, far apart (e.g. 12px, 18px, 160px). Skip the timid middle sizes.
> - **Typography carries identity.** There are no web fonts unless you write them as files, so choose system stacks deliberately (e.g. `ui-serif, Georgia` for voice; `system-ui` for text; `ui-monospace` for labels) and get character from size, weight, tracking (tight on large type), case and line-height (0.85–0.95 for display).
> - **Colour restraint**: a ground, an ink, one accent used on one or two things. Avoid pure #000/#FFF unless the concept demands it.
> - **Grid and whitespace**: align to clear columns; let space be empty; asymmetry over centring. Hairline rules and numbering (01, 02) beat cards and shadows.
> - **WebGPU depicts the content** (the name, the pages, the experiments), not generic particles behind text. Keep it monochrome enough that text stays readable.
> - **Motion**: strong ease-outs (`cubic-bezier(.16,1,.3,1)`), 200–1200ms, elements entering with purpose; nothing perpetual except one subtle detail. Honour `prefers-reduced-motion`.
> - **Avoid**: gradient blobs, glassmorphism, rounded cards with drop shadows, emoji/icons as decoration, "Hi, I'm…" heroes, fake loaders, identical fade-ups on every block.
> - The phone layout is a recomposition, not a shrink. Check it at 390px.

---

## 8. Fonts to self-host

All three are **SIL Open Font License 1.1**. Download the WOFF2 files once, commit
them under `web/static/fonts/`, and serve them from `'self'` (no runtime CDN, which
satisfies the CSP). Fetch them with curl at build or vendor time only. The URLs
below are Fontsource's builds (pinned to v5.3.0) of the upstream Google Fonts
sources. All were verified to return 200 on 2026-10-01. Keep each family's
`OFL.txt` next to the files.

### Newsreader (Production Type), OFL 1.1, display/voice
Variable, with `wght` 200–800 and `opsz` 6–72 in the "opsz" files.
- Roman (Latin): https://cdn.jsdelivr.net/npm/@fontsource-variable/newsreader@5.3.0/files/newsreader-latin-opsz-normal.woff2 (about 132 KB)
- Italic (Latin): https://cdn.jsdelivr.net/npm/@fontsource-variable/newsreader@5.3.0/files/newsreader-latin-opsz-italic.woff2 (about 147 KB)
- Roman (Latin Ext, optional): https://cdn.jsdelivr.net/npm/@fontsource-variable/newsreader@5.3.0/files/newsreader-latin-ext-opsz-normal.woff2
- Upstream source: https://github.com/google/fonts/tree/main/ofl/newsreader. License: https://raw.githubusercontent.com/google/fonts/main/ofl/newsreader/OFL.txt
- To save bytes, subset the roman to the characters actually used at display sizes. Most of the weight is the opsz axis, which is worth keeping.

### Instrument Sans (Instrument), OFL 1.1, text/UI
Variable, with `wght` 400–700 and `wdth` 75–100 in the "standard" files.
- Roman (Latin): https://cdn.jsdelivr.net/npm/@fontsource-variable/instrument-sans@5.3.0/files/instrument-sans-latin-standard-normal.woff2 (about 57 KB)
- Italic (Latin): https://cdn.jsdelivr.net/npm/@fontsource-variable/instrument-sans@5.3.0/files/instrument-sans-latin-standard-italic.woff2 (about 62 KB)
- Roman (Latin Ext, optional): https://cdn.jsdelivr.net/npm/@fontsource-variable/instrument-sans@5.3.0/files/instrument-sans-latin-ext-standard-normal.woff2
- Weight-only alternative (30 KB, no `wdth`): https://cdn.jsdelivr.net/npm/@fontsource-variable/instrument-sans@5.3.0/files/instrument-sans-latin-wght-normal.woff2
- Upstream source: https://github.com/google/fonts/tree/main/ofl/instrumentsans. License: https://raw.githubusercontent.com/google/fonts/main/ofl/instrumentsans/OFL.txt

### Fragment Mono (Wei Huang / URW base), OFL 1.1, meta/machine
- Regular (Latin): https://cdn.jsdelivr.net/npm/@fontsource/fragment-mono@5.3.0/files/fragment-mono-latin-400-normal.woff2 (about 25 KB)
- Italic (Latin): https://cdn.jsdelivr.net/npm/@fontsource/fragment-mono@5.3.0/files/fragment-mono-latin-400-italic.woff2 (about 26 KB)
- Regular (Latin Ext, optional): https://cdn.jsdelivr.net/npm/@fontsource/fragment-mono@5.3.0/files/fragment-mono-latin-ext-400-normal.woff2
- Upstream source: https://github.com/google/fonts/tree/main/ofl/fragmentmono. License: https://raw.githubusercontent.com/google/fonts/main/ofl/fragmentmono/OFL.txt

The critical-path budget is about 215 KB (Newsreader roman, Instrument Sans roman and
Fragment Mono). Preload the first two. Italics load on demand.

Example `@font-face` (main site):
```css
@font-face {
  font-family: "Newsreader";
  src: url("/static/fonts/newsreader-latin-opsz-normal.woff2") format("woff2");
  font-weight: 200 800; font-style: normal; font-display: swap;
}
@font-face {
  font-family: "Instrument Sans";
  src: url("/static/fonts/instrument-sans-latin-standard-normal.woff2") format("woff2");
  font-weight: 400 700; font-stretch: 75% 100%; font-style: normal; font-display: swap;
}
@font-face {
  font-family: "Fragment Mono";
  src: url("/static/fonts/fragment-mono-latin-400-normal.woff2") format("woff2");
  font-weight: 400; font-style: normal; font-display: swap;
}
```

**Note for visitor front ends:** they run on the user-content domain under
`font-src 'self' data:`, so they can't load these files today, and the builder model
can't easily write binary WOFF2. That is why the taste brief tells it to use system
stacks well. If house fonts are wanted inside front ends later, the user-content
service could serve the same three OFL files at a fixed same-origin path, and the
system prompt could permit that one path. That is a protocol change, so it is not
assumed here.

---

## 9. As built (2026-10-01)

What shipped from this document, and where it deliberately differs.

**Fonts.** The six WOFF2 files and the three `OFL.txt` licenses live in
`web/static/fonts/` (sources and changes in `SOURCES.txt`). The variable
files are instanced with fontTools to the axis ranges the design uses:
Newsreader `wght` 300–400 and `opsz` 16–72 (84 KB roman, 93 KB italic), Instrument
Sans `wght` 400–600 and `wdth` 85–100 (53 KB, 57 KB), Fragment Mono unchanged (25 KB,
26 KB). The critical path (Newsreader roman, Instrument Sans roman, Fragment Mono)
is about 160 KB, down from 215 KB. The files are embedded in both binaries
(`web.Fonts`): the main site serves them at `/static/fonts/`, and the user-content
service serves them at `/fonts/<file>` with `Access-Control-Allow-Origin: *`, so
any front end, including visitor-made ones, can load them under `font-src 'self'`.
Both cache them for a year (`immutable`): never change a file in place. The Latin
subset has no arrows or ⌘ ↵ ⇧, so the house type avoids them.

**Stylesheets.** `base.css` (fonts, tokens, reset, reduced motion), `site.css` (the
transcript, the corner button, the draft tape), `build-modal.css` (the Studio, its
`--bm-*` properties now aliases of the tokens) and `admin.css` + `builder.css`.
The grain is `web/static/grain.png` (generated, 4-bit, 33 KB).

**Transcript (5.3).** The same composition as 5.1/5.2: meta row, the name on two
lines at `--t-mega`, `( About )` bio, `( Experiments — 0N )` index with hairline
rows, a quiet footer. The meta row's nav comes from the published pages and is
numbered. The role line is fixed chrome ("Creative technology / AI"), not CMS
content, and there is no clock or location (nothing honest to show). A short
first paragraph ("Coming soon.") is set as an italic aside and the next one is
the lede (the doc's rule would have made "Coming soon." the loudest line).
Experiments aren't links (they have no pages), so index rows have no hover.

**Default front end (5.1, 5.2, 6).** `site/` now renders DOM (Rust, `web-sys`) in
the house fonts and calls `site.ready()` as soon as the text is up, so it works
without WebGPU. WebGPU adds one thing: a fixed grid of fine dots behind the page
that swell into a halftone halo of the name (a mask drawn from the name's own
letterforms), lean toward the pointer, and appear outward from the letters on load.
It is monochrome, static under reduced motion, and a lost device only hides it.
Consequence: without WebGPU the default front end goes live instead of falling back,
so `e2e/tests/no-webgpu.spec.ts` now checks that, and checks the transcript by
making the user-content origin unreachable.

**Corner button (5.4).** As specified, inset `min(--m, 32px)`. There is no
hide-on-scroll: scrolling happens inside the cross-origin front end, which the
parent can't observe. Cmd/Ctrl+K opens the builder from the parent page.

**Studio (5.5).** The dialog keeps its accessible name "Make your own version"
(the e2e tests and screen readers use it) and sets it in the mono header. There are
no thumbnails to make a contact sheet from (front ends are cross-origin), so each
creation is a serif title with its versions as a hairline log of the visitor's own
prompts, newest first, with an accent dot on the one on the site. The Build button
is the accent rectangle (it is the composer's submit); Submit for review is an
outline button. Enter sends and Shift+Enter is a new line. On phones and tablets it
is a 92svh bottom sheet; dragging the grabber or header down closes it.

**Builder taste (7).** The taste brief is in `internal/builder/prompt.go` as "Taste
bar", followed by "Type: the house fonts" with the `/fonts/` paths and an
`@font-face` snippet. Two edits to the brief keep it consistent with the prompt's
existing rule against stock defaults (numbered section labels, monospace labels):
"hairline rules and alignment beat cards", without "numbering", and no monospace
suggestion. The note about the corner button's size was updated to the new pill.

**Admin (5.7).** Paper by default (`data-theme="light"`), Newsreader page titles,
mono meta labels and table heads, hairline tables that scroll sideways on phones,
bottom-border fields, and `--fg` primary buttons. The accent appears only in the
nav count badges.
