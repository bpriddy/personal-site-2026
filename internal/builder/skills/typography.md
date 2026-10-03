description: choosing and self-hosting type beyond the house fonts (Google Fonts import, pairing, loading without layout shift)

# Typography

## When
The house fonts (Newsreader, Instrument Sans, Fragment Mono under /fonts/) are a strong default. Import a Google Font when the concept needs a voice they can't give: a grotesk with character, a display serif, a typewriter, a condensed poster face, a script for one word.

## Choose
- Pick for the concept, then for quality: families with real optical sizes or variable axes (e.g. "Fraunces", "Bricolage Grotesque", "Instrument Serif", "Space Grotesk", "Syne", "DM Serif Display", "IBM Plex Mono", "JetBrains Mono", "Playfair Display", "Archivo", "Big Shoulders Display", "Unbounded").
- One display face plus one text face (or the house sans) at most. Never two display faces.
- Body text needs a face built for reading at 16–18px: don't set long text in a display or script face.

## Import
`google_fonts_import` with the exact family name and axes: "wght@400;700", a variable range "wght@300..800", or italics "ital,wght@0,400;1,400". Paste the returned @font-face rules into your CSS unchanged (they point at /media/). Up to 3 families per run.

## Load well
- Give every family a fallback stack with similar metrics (`font-family: "Space Grotesk", "Instrument Sans", system-ui, sans-serif;`).
- Wait for the fonts before measuring text for a canvas or WebGPU texture: `await document.fonts.load('700 48px "Space Grotesk"')` (with a timeout, as site.loaded-style code does), then draw.
- Big display type: tighten tracking (`letter-spacing: -0.02em` to `-0.04em`) and line-height (0.9–1.05); small caps labels: open tracking (0.06–0.12em).
