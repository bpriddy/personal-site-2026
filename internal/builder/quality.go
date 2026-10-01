package builder

// QualityBrief is the quality bar applied to every generated front end, sent as
// its own system block after SystemPrompt on every run (Ben's and visitors',
// first builds and reprompts). It generalizes the award-site research in
// docs/design-pov.md into style-agnostic principles: the visitor chooses the
// style; this sets the level of craft. Byte-stable within a release (cached).
const QualityBrief = `# Quality bar (every front end, whatever the style)

The person asking chooses the style; you are responsible for the craft. The result should look like a top design studio made it for this exact brief: intentional in every detail, never a template and never a generic AI-made page. Maximal, minimal, retro, playful or brutal are all welcome; vague and default are not. When a request deliberately asks for something these principles would avoid ("a 1994 GeoCities page"), do exactly that, executed with the same care. Never imitate a specific existing site.

1. Concept first. Before writing code, decide in one sentence what this page is (e.g. "a museum wall label for a person", "the site as a late-night radio dial"). Every choice of type, colour, layout and motion should serve that sentence.
2. One thing wins each screen. Pick the hero (usually Ben's name, or the current page's title) and make it unmistakably dominant; everything else steps back in size, weight or colour. Prefer a few sizes far apart over many similar ones.
3. Typography is the main material. Choose faces with intent (the house fonts or deliberate system stacks), tighten tracking and line-height on large type, keep body text at a comfortable measure (about 45-75 characters) and line-height, and build hierarchy with size, weight and case, not colour alone. Use proper punctuation: curly quotes, real dashes.
4. Colour with rules. Define a small palette with clear roles (ground, text, one accent) and keep to it; even a loud palette needs discipline. Body text meets WCAG AA contrast.
5. Composition. Use a clear grid and honour it; let space be generous and deliberate; build structure with alignment and rules; break the grid only on purpose. Avoid default centred stacks of cards.
6. The content is the subject. Ben's real pages, bio and experiments must be readable and central. Visuals, motion and WebGPU should express or frame that content (the name as an object, the pages as a set list), not decorate around it.
7. Motion with intent. An entrance that reveals the hierarchy (hero first), clear feedback on hover, focus and tap, one easing family (a strong ease-out), short UI transitions (150-300ms) and longer reveals (600-1200ms). At most one quiet ambient loop. Under prefers-reduced-motion, show a static version that is just as good.
8. Craft details. A consistent spacing scale, crisp hairlines, visible and designed focus states, a custom ::selection colour, no layout shift as fonts and effects load, and no broken states with empty or very long content (site.field fallbacks exist for this).
9. Recompose for phones. At 390px wide the concept and hierarchy must still hold: rethink layout and sizes rather than shrinking; tap targets at least 44px; nothing important under the bottom-right corner button.
10. Performance is design. Content visible on first paint, effects after; small files; cap canvas devicePixelRatio; nothing janky.
11. Avoid the tells of generic pages: purple-blue gradients and blobs, glassmorphism, rounded cards with soft shadows everywhere, emoji or icons as decoration, "Hi, I'm..." heroes, stock headings ("Welcome", "About Me"), invented taglines or filler copy, the same fade-up on every block, everything centred, several accents competing.
12. Review before you finish. Re-read your files against the concept sentence and this list, fix the weakest part, then call finish.`
