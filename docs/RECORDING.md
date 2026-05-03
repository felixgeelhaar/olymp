# Recording the demo for the landing page

The page has two recording slots, both with graceful fallbacks:

| Slot | File | Fallback | Best for |
|---|---|---|---|
| **Terminal** | `docs/assets/demo.cast` | static `<pre>` block | demo-full.sh output, curl runs |
| **Dashboard UI** | `docs/assets/dashboard-loop.mp4` | inline animated SVG | the radial topology + packets + drawer |

Both are auto-loaded by the page; missing files fall through silently.

## Terminal recording (asciinema)

Lightweight, scrubbable, weighs a few KB, plays in any browser via
the asciinema-player web component already wired into the page.

```bash
brew install asciinema   # or: pipx install asciinema
asciinema rec docs/assets/demo.cast
./deploy/demo-full.sh
exit                     # ends the recording
```

A hand-authored cast ships at `docs/assets/demo.cast` already — replace
it with a real one whenever the demo flow changes.

Trim with `asciinema-tools` or just edit the JSON cast directly to
remove dead time / banner output.

## Dashboard UI recording (MP4)

Target: ~5–10 seconds, silent, ≤2 MB, H.264 + AAC, 720p.

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
