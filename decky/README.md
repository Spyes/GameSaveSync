# Save Sync — Decky Loader plugin

A Game Mode control surface for [Save Sync](../). It lets you **Upload** / **Download**
game saves from the Steam Deck's Quick Access menu without leaving Game Mode.

It's control-only: **add games and set your GitHub token in the desktop web app**
(Desktop Mode). This plugin reads the same `~/.config/save-sync/config.json` and
drives the same `save-sync` binary, so everything you configured there shows up here.

## How it works

- `main.py` (backend) runs the bundled `bin/save-sync` binary (built from the parent
  Go project) as the **deck user** — Decky runs backends as root, so it drops to
  `DECKY_USER` and points `HOME`/`SAVESYNC_CONFIG_DIR` at the deck user's config, keeping
  files consistent with the desktop app.
- `src/index.tsx` (frontend) renders the Quick Access panel: each game with an
  Upload / Download button and its In sync / Update-available status.

## Build

```bash
# from repo root: cross-compile the engine into the plugin
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o decky/bin/save-sync .

cd decky
pnpm install
pnpm build          # -> dist/index.js
```

Then package `plugin.json package.json main.py dist/ bin/` into a zip. CI does this
automatically on each release (`save-sync-decky.zip`).

## Install on the Deck

Decky Loader ▸ Settings ▸ enable **Developer mode** ▸ **Install from URL**, and paste
the `save-sync-decky.zip` asset URL from the GitHub release.
