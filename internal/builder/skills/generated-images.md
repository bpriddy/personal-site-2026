description: bespoke images generated with Krea (hero art, illustration, backgrounds, textures, posters): when to generate, how to prompt, how to place them

# Generated images

## When
Generate when the concept needs an image that doesn't exist: art in a specific style (a 70s sci-fi paperback cover, a risograph print, a Bauhaus poster, a botanical plate), a background or atmosphere (a nebula, fog over water, a paper texture), or a texture for WebGPU. Don't generate for decoration's sake, and never instead of Ben's real content: his project media stays the media for his projects.

## Prompt
Write it like an art director's brief, in this order:
1. Subject and what it's doing ("an Imperial-style wedge-shaped capital ship drifting past a ringed planet").
2. Medium and style ("1977 matte painting, gouache on board, visible brush texture").
3. Light ("hard key light from the left, deep shadows, faint rim").
4. Composition and framing for where it will sit ("subject in the right third, empty dark sky on the left for text").
5. Palette ("desaturated slate greys with one warm orange accent").
No text, letters, logos, watermarks or signatures in the image (they come out garbled; set text in the page). No real people, no brands or trademarked characters: describe the look in your own words instead.

Pick the aspect ratio for the slot: 16:9 or 21:9 for a full-bleed band, 4:5 or 3:4 for a portrait panel, 1:1 for a tile or texture, 9:16 for a phone-first background.

## Judge it
You see the result. If it misses the concept (wrong style, cluttered, wrong framing, garbled details), regenerate once with a sharper prompt rather than settling. Up to 4 per run: one great image beats four mediocre ones.

## Place it
- In HTML: `<img src="/media/assets/images/krea/<id>.png" alt="..." width=".." height="..">` with the real size, so nothing shifts as it loads; `alt=""` when it's purely atmosphere.
- As a background: `background: url(...) center / cover`, with a scrim (gradient or tinted overlay) under any text; check contrast.
- In WebGPU: load with `img.crossOrigin = "anonymous"`, `await img.decode()`, `createImageBitmap(img)`, then copy into a `rgba8unorm-srgb` texture.
- Grade it into the design (CSS filters, blend modes, a shared tint, grain) so it belongs to the page instead of sitting on it.
