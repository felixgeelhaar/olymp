# Recording the dashboard loop for the landing page

The hero section on https://felixgeelhaar.github.io/olymp/ tries to
play `docs/assets/dashboard-loop.mp4` and falls back to the inline
animated SVG when the file is missing. Drop a real screen capture in
that path and the page picks it up on next deploy.

Target spec: ~5–10 seconds, silent, ≤2 MB, H.264 + AAC, 720p.

## 1. Boot the demo

```bash
./deploy/demo-full.sh
```

Wait for the script to finish — it submits one `explain` and one
`remediate` so the dashboard already has runs visible.

## 2. Open the dashboard

```bash
open http://localhost:8080/dashboard
```

Maximise the window so the radial topology fills the screen.

## 3. Trigger fresh activity to record

In another terminal, fire a few back-to-back runs so packets are
flying continuously while you record:

```bash
for i in 1 2 3 4 5; do
  curl -fsS -X POST http://localhost:8080/v1/runs \
    -H 'Content-Type: application/json' \
    -H 'X-Olymp-Caller-Type: user' -H 'X-Olymp-Caller-Id: rec' \
    -d '{"type":"explain","subject":"payments-latency","payload":{"subject":"payments-latency"}}' >/dev/null
  sleep 1
done
```

While these run, click a packet → the inspector drawer opens with the
JSON each layer received. That moment is the screenshot you want in
the loop.

## 4. Record (macOS)

`Cmd+Shift+5` → choose "Record selected portion" → drag a tight box
around the dashboard area → record ~8 seconds → stop. Output lands as
`Screen Recording 2026-…mov` on the desktop.

(Linux: use `peek` or `ffmpeg -f x11grab`. Windows: Game Bar.)

## 5. Re-encode for web

```bash
ffmpeg -i input.mov \
  -an \
  -vcodec libx264 -profile:v main -level 3.1 \
  -pix_fmt yuv420p \
  -movflags +faststart \
  -crf 28 -preset slow \
  -vf "scale=1280:-2,fps=30" \
  docs/assets/dashboard-loop.mp4
```

Confirm the output is under 2 MB:

```bash
ls -lh docs/assets/dashboard-loop.mp4
```

## 6. Commit + push

```bash
git add docs/assets/dashboard-loop.mp4
git commit -m "docs(pages): record dashboard loop for hero"
git push origin main
```

GitHub Pages redeploys on `docs/**` change. The hero `<video>` will
fade in within ~30 seconds; the SVG fallback hides automatically.

## Don't have time to record?

The page works without the file — the inline SVG runs forever and
covers the same shape. Record it whenever a polished demo asset is
worth the effort.
