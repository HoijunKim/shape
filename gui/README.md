# shape desktop GUI

A Wails v2 desktop app that reuses shape's Go core (`internal/pipeline`) to
profile a data file and export its JSON Schema. Part of the same Go module as
the CLI; the CLI (`shape.exe`) never links Wails and stays cgo-free.

## Build order (important)

The generated Wails TypeScript bindings under `frontend/wailsjs/` are NOT
committed (they are regenerated from the bound Go `App` methods). So a clean
checkout must generate them BEFORE the frontend build:

    cd gui
    wails generate module        # writes frontend/wailsjs/ from gui/app.go
    cd frontend && npm ci && npm run build   # vite build -> frontend/dist
    npm run check                # svelte-check typecheck

`wails dev` and `wails build` run `wails generate module` for you, so:

    cd gui && wails build         # -> gui/build/bin/shape-gui.exe (Windows: cgo-free)
    cd gui && wails dev           # hot-reload dev window

## Requirements

Wails v2, Node 18+, and (on Windows) the WebView2 runtime. On macOS/Linux the
GUI build needs cgo + native webkit; the CLI stays cgo-free on every OS.
