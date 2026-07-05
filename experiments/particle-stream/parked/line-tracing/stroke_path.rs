// ───────────────────────────────────────────────────────────────────────────
// PARKED — character stroke-path / line-tracing (NOT compiled).
//
// This is the "camera flies along the letterforms" work, extracted from
// src/lib.rs and parked for possible future use. It is intentionally OUTSIDE
// src/ so Cargo/Trunk never build it.
//
// What it does: turn a rasterized title word into an ORDERED stroke-centerline
// path that a camera can trace, letter by letter.
//   • zhang_suen()       — Zhang-Suen thinning: binary coverage → 1px skeleton.
//   • stroke_path_from()  — skeletonize, label connected components (≈ letters),
//     walk each in reading order (continue-straight at junctions, nearest-
//     unvisited restart when a stroke ends), tag each point with local stroke
//     WIDTH. Returns flat [x, y, width, …], x,y in 0..1, width a fraction of the
//     raster width.
//
// Dependencies it needs from src/lib.rs (still present there):
//   • edt8(mask,w,h) -> Vec<(i32,i32)>   — 8SSEDT exterior distance transform.
//   • raster_title_sharp(ctx,w,h,title)  — the anti-aliased coverage raster.
//
// How it was wired when live (see git history before this was parked):
//   • bake_title() called stroke_path_from() and returned (path, sdf).
//   • the path drove the section camera: title_t scrubbed an index along it,
//     title_off_cur eased to the point (the "fly" across letter gaps), and the
//     zoom was fill / stroke-width at that point.
//   • a PATH editor (paths.json + window.__PATH/__PATH_VER + read_path_override()
//     + simplify_path() seed + the ↝ panel in ui.js/index.html) let it be hand-
//     edited as [[x,y,w],…] waypoints.
// To revive: re-add those call sites + the editor, or `git show <pre-parking
// commit>:personal-site/src/lib.rs` to recover the exact integration.
// ───────────────────────────────────────────────────────────────────────────

// ── Zhang-Suen thinning ──────────────────────────────────────────────
fn zhang_suen(g: &mut [u8], w: usize, h: usize) {
    let idx = |x: usize, y: usize| y * w + x;
    loop {
        let mut removed = false;
        for step in 0..2 {
            let mut rem: Vec<usize> = Vec::new();
            for y in 1..h - 1 {
                for x in 1..w - 1 {
                    if g[idx(x, y)] == 0 {
                        continue;
                    }
                    let n = [
                        g[idx(x, y - 1)], g[idx(x + 1, y - 1)], g[idx(x + 1, y)], g[idx(x + 1, y + 1)],
                        g[idx(x, y + 1)], g[idx(x - 1, y + 1)], g[idx(x - 1, y)], g[idx(x - 1, y - 1)],
                    ];
                    let b: u8 = n.iter().sum();
                    if b < 2 || b > 6 {
                        continue;
                    }
                    let mut a = 0; // 0→1 transitions around the ring
                    for k in 0..8 {
                        if n[k] == 0 && n[(k + 1) % 8] == 1 {
                            a += 1;
                        }
                    }
                    if a != 1 {
                        continue;
                    }
                    let (p2, p4, p6, p8) = (n[0], n[2], n[4], n[6]);
                    if step == 0 {
                        if p2 * p4 * p6 != 0 || p4 * p6 * p8 != 0 {
                            continue;
                        }
                    } else if p2 * p4 * p8 != 0 || p2 * p6 * p8 != 0 {
                        continue;
                    }
                    rem.push(idx(x, y));
                }
            }
            if !rem.is_empty() {
                removed = true;
                for &i in &rem {
                    g[i] = 0;
                }
            }
        }
        if !removed {
            break;
        }
    }
}

// ── ordered stroke-centerline trace ──────────────────────────────────
// trace an ordered stroke-centerline path from a sharp coverage mask: skeletonize
// (Zhang-Suen), walk it in reading order (per letter, junctions resolved), tag
// each point with stroke width. Flat [x,y,width,…]; x,y in 0..1, width a fraction
// of the raster width.
fn stroke_path_from(sharp: &[u8], w: u32, h: u32) -> Vec<f32> {
    let (wf, hf) = (w as f64, h as f64);
    let (wu, hu) = (w as usize, h as usize);
    let n = wu * hu;

    let mut g: Vec<u8> = sharp.iter().map(|&v| if v > 127 { 1 } else { 0 }).collect();
    zhang_suen(&mut g, wu, hu);

    let inv: Vec<u8> = sharp.iter().map(|&v| if v > 127 { 0 } else { 255 }).collect();
    let interior = edt8(&inv, wu, hu); // inside→background offset = stroke half-width
    let width_at = |i: usize| -> f32 {
        let (dx, dy) = interior[i];
        2.0 * ((dx * dx + dy * dy) as f32).sqrt()
    };

    let pts: Vec<usize> = (0..n).filter(|&i| g[i] == 1).collect();
    if pts.is_empty() {
        return Vec::new();
    }
    let xy = |i: usize| ((i % wu) as i32, (i / wu) as i32);
    // label connected components (≈ letters) with a flood fill, so we can trace
    // them in reading order and no thin letter gets stranded to the end.
    let mut comp = vec![-1i32; n];
    let mut comps: Vec<Vec<usize>> = Vec::new();
    for &seed in &pts {
        if comp[seed] >= 0 {
            continue;
        }
        let cid = comps.len() as i32;
        comp[seed] = cid;
        let mut stack = vec![seed];
        let mut members = Vec::new();
        while let Some(p) = stack.pop() {
            members.push(p);
            let (px, py) = xy(p);
            for ddy in -1..=1i32 {
                for ddx in -1..=1i32 {
                    if ddx == 0 && ddy == 0 {
                        continue;
                    }
                    let (nx, ny) = (px + ddx, py + ddy);
                    if nx < 0 || ny < 0 || nx >= wu as i32 || ny >= hu as i32 {
                        continue;
                    }
                    let ni = (ny as usize) * wu + nx as usize;
                    if g[ni] == 1 && comp[ni] < 0 {
                        comp[ni] = cid;
                        stack.push(ni);
                    }
                }
            }
        }
        comps.push(members);
    }
    comps.sort_by_key(|m| m.iter().map(|&i| i % wu).min().unwrap());

    let deg = |i: usize| -> usize {
        let (px, py) = xy(i);
        let mut d = 0;
        for ddy in -1..=1i32 {
            for ddx in -1..=1i32 {
                if (ddx != 0 || ddy != 0)
                    && px + ddx >= 0
                    && py + ddy >= 0
                    && px + ddx < wu as i32
                    && py + ddy < hu as i32
                    && g[((py + ddy) as usize) * wu + (px + ddx) as usize] == 1
                {
                    d += 1;
                }
            }
        }
        d
    };
    let mut visited = vec![false; n];
    let mut order: Vec<usize> = Vec::new();
    for members in &comps {
        // start at the leftmost endpoint (degree 1), else the leftmost pixel
        let mut cur = *members.iter().min_by_key(|&&i| (i % wu, i / wu)).unwrap();
        if let Some(&ep) = members.iter().filter(|&&i| deg(i) == 1).min_by_key(|&&i| (i % wu, i / wu)) {
            cur = ep;
        }
        visited[cur] = true;
        order.push(cur);
        let mut dir = (1i32, 0i32);
        loop {
            let (cx, cy) = xy(cur);
            let mut best: Option<usize> = None;
            let mut best_score = -2.0f32;
            for ddy in -1..=1i32 {
                for ddx in -1..=1i32 {
                    if ddx == 0 && ddy == 0 {
                        continue;
                    }
                    let (nx, ny) = (cx + ddx, cy + ddy);
                    if nx < 0 || ny < 0 || nx >= wu as i32 || ny >= hu as i32 {
                        continue;
                    }
                    let ni = (ny as usize) * wu + nx as usize;
                    if g[ni] != 1 || visited[ni] || comp[ni] != comp[cur] {
                        continue;
                    }
                    let dl = ((ddx * ddx + ddy * ddy) as f32).sqrt();
                    let score = (ddx as f32 * dir.0 as f32 + ddy as f32 * dir.1 as f32) / dl;
                    if score > best_score {
                        best_score = score;
                        best = Some(ni);
                    }
                }
            }
            let next = match best {
                Some(ni) => ni,
                None => {
                    let mut nn: Option<usize> = None;
                    let mut nd = i64::MAX;
                    for &i in members.iter() {
                        if visited[i] {
                            continue;
                        }
                        let (ix, iy) = xy(i);
                        let d = ((ix - cx) as i64).pow(2) + ((iy - cy) as i64).pow(2);
                        if d < nd {
                            nd = d;
                            nn = Some(i);
                        }
                    }
                    match nn {
                        Some(i) => i,
                        None => break,
                    }
                }
            };
            let (nx, ny) = xy(next);
            dir = ((nx - cx).signum(), (ny - cy).signum());
            visited[next] = true;
            order.push(next);
            cur = next;
        }
    }

    let mut out: Vec<f32> = Vec::with_capacity(order.len() * 3);
    for &i in &order {
        let (ix, iy) = xy(i);
        out.push(ix as f32 / wf as f32);
        out.push(iy as f32 / hf as f32);
        out.push(width_at(i) / wf as f32);
    }
    out
}
