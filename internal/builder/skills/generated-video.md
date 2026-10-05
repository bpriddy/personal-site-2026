description: short generated video clips (Krea: Seedance 2.5, MiniMax H3) for hero motion and living backgrounds; Ben's runs only, costly, use sparingly

# Generated video

## When
Only when motion is the point of the concept and code can't do it as well: a cinematic hero shot (a ship gliding past, waves breaking, smoke in light), a living background loop, a short "trailer" moment. Never for something CSS, canvas or WebGPU can animate (gradients, particles, type motion, 3D models). Each clip costs real money (about $0.70 to $4): one great clip per front end is the norm, two at most.

## Workflow
0. If Ben attached an image to animate or to use as the reference, it's already in the media store: pass its /media/ path as `start_image` directly (no need to generate a still), unless he asks for a new look based on it.
1. Make the first frame a still with `krea_generate_image` (you see it; iterate until it's right). It sets the look.
2. Animate it: `krea_generate_video` with `start_image` = that still's /media/ path, a prompt that describes only the motion and camera ("the ship glides slowly left to right, slight camera drift, dust motes in the light, calm pace"), 5–8 seconds.
3. Models: `seedance-2.5` for the richest, most cinematic motion; `minimax-h3` to stay faithful to the start frame at lower cost. Aspect ratio for the slot (16:9 or 21:9 bands, 9:16 for a phone-first hero).
4. You can't watch the result: use it as designed and describe it honestly in your summary.

## Place it
```html
<video src="/media/assets/videos/krea/<id>.mp4" poster="/media/assets/images/krea/<still>.png"
       muted loop playsinline autoplay preload="metadata" aria-hidden="true"></video>
```
- Always muted (autoplay requires it); the site's own loops play the same way.
- Under `prefers-reduced-motion`, don't autoplay: show the poster (or pause on the first frame).
- Pause it when off screen (IntersectionObserver), and give it a fixed aspect-ratio box so nothing shifts while it loads.
- A seamless loop: crossfade the ends with CSS, or choose motion that reads as continuous (drift, flow).
- In WebGPU, a video can be a texture: `device.importExternalTexture({ source: video })` each frame (set `video.crossOrigin = "anonymous"` before `src`).
