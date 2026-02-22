## Taking browser screenshots

The devcontainer includes a `take-screenshot` command for capturing web page screenshots using headless Chromium.

### Usage

```
take-screenshot <url> <output-path> [width] [height]
```

- **url** — The page to screenshot (e.g., `http://localhost:5173`)
- **output-path** — Where to save the PNG (e.g., `/tmp/screenshot.png`)
- **width** — Viewport width in pixels (default: 1280)
- **height** — Viewport height in pixels (default: 720)

### Example

```
take-screenshot http://localhost:3000 /tmp/home.png
```

Then use the Read tool to view the resulting PNG.

### Tips

- The page waits for `networkidle` before capturing, so dynamically loaded content should be visible.
- To capture a specific viewport size (e.g., mobile), pass width and height: `take-screenshot http://localhost:3000 /tmp/mobile.png 375 812`
- The screenshot captures the viewport only (not full-page scroll).
