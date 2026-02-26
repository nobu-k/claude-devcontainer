---
name: take-screenshot
description: Capture a browser screenshot of a web page using headless Chromium. Use when you need to visually inspect a running web app.
---

# take-screenshot

Capture a browser screenshot using headless Chromium.

## Usage

```
take-screenshot [options] <url> <output-path> [width] [height]
```

**Options** (must come before positional arguments):

- `--full-page` — Capture the entire scrollable page, not just the viewport
- `--media print|screen` — Emulate a CSS media type (e.g., `print` for print stylesheets)

**Positional arguments:**

- **url** — The page to screenshot (e.g., `http://localhost:5173`)
- **output-path** — Where to save the PNG (e.g., `/tmp/screenshot.png`)
- **width** — Viewport width in pixels (default: 1280)
- **height** — Viewport height in pixels (default: 720)

## Examples

Basic screenshot:

```bash
take-screenshot http://localhost:3000 /tmp/home.png
```

Full-page screenshot with print media emulation at A4 size (96 dpi):

```bash
take-screenshot --full-page --media print http://localhost:5173 /tmp/print.png 794 1123
```

Then use the Read tool to view the resulting PNG.

## Tips

- The page waits for `networkidle` before capturing, so dynamically loaded content should be visible.
- To capture a specific viewport size (e.g., mobile), pass width and height: `take-screenshot http://localhost:3000 /tmp/mobile.png 375 812`
- Use `--media print` to test print stylesheets — this activates `@media print` CSS rules.
- By default the screenshot captures the viewport only. Use `--full-page` to capture the entire page.
