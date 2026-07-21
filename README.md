# Save Sync

A tiny, self-contained tool to sync game **save files between a Steam Deck and Windows** (or any two machines) using a GitHub repo as the transport and version history. Manual by design: you click **Upload save** on one device, **Download save** on the other. Every upload is a git commit, so you get automatic versioning and rollback.

It runs as a **local web app**: one small binary that serves a web UI at `http://localhost:8787`. No Node, Python, or git install required — a single executable per OS.

## Run it

- **Windows:** double-click `save-sync.exe` (or run it in a terminal).
- **Steam Deck:** switch to Desktop Mode, copy `save-sync-linux`, then:
  ```bash
  chmod +x save-sync-linux
  ./save-sync-linux
  ```
- **macOS (dev):** `./save-sync`

Your browser opens to the UI automatically. Flags: `--port <n>` (default 8787), `--no-open`.

## First-time setup

1. Create a GitHub repo for your saves (private is fine).
2. Create a **Personal Access Token** with `repo` scope (classic) or Contents read/write (fine-grained): <https://github.com/settings/tokens>. Paste it into the **GitHub access** box in the UI (stored locally on that device only).
3. Click **Add a save** and fill in:
   - **Name** — e.g. `Elden Ring`. This also becomes the folder name **inside** the repo, so one repo can hold many games (or use one repo per game).
   - **GitHub repo URL** — e.g. `https://github.com/you/game-saves.git`
   - **Local save folder** — the game's save directory on this device (see below).

## Steam Deck Game Mode (Decky plugin)

In Game Mode there's no easy browser, so there's a [Decky Loader](https://decky.xyz/) plugin (in [`decky/`](decky/)) that puts **Upload** / **Download** buttons for each game right in the Quick Access menu. Configure your games and token once in the desktop web app (Desktop Mode); the plugin reads the same config and drives the same engine.

Install it from a release: Decky ▸ Settings ▸ enable **Developer mode** ▸ **Install from URL**, and paste the `save-sync-decky.zip` asset URL from the [latest release](https://github.com/Spyes/GameSaveSync/releases).

The binary also has a JSON CLI the plugin uses (and you can script with): `save-sync list | status | upload <id> [--note ..] | download <id> [--hash ..] | history <id>`. Set `SAVESYNC_CONFIG_DIR` to point at a specific config directory.

## Setting up a second device (discovery)

You don't have to re-enter each game by hand. On the new device, set its PAT, then under **Discover games from another device**:

- **🔍 Discover from GitHub** — scans every repo your token can see for save-sync games and lists them all. Or
- **Import from repo URL** — paste your saves repo URL (e.g. `https://github.com/you/game-saves.git`) to list the games in it.

For each game found, pick **this device's** local folder and click **Adopt**. (The local path can't be shared across devices, but the tool prefills a guess from the folder the other device used.) Games already set up here are greyed out.

This works because every Upload also commits a small `.savesync.json` manifest at the repo root describing the games — that's what a new device reads. Repos created before this feature are still discoverable by URL (their subfolders are listed); they gain a manifest on the next upload.

> The manifest records each game's local folder path (which usually contains your OS username) and the uploading device's hostname, as convenience hints. Keep your saves repo **private** if you'd rather not publish those.

## Everyday use

- On the device you just played on: **⬆ Upload save**.
- On the other device, before playing: **⬇ Download save** (overwrites its local folder with the latest).
- **Versions** lists every upload. **Click any version to restore it** to your local folder; a banner warns when you're on an older version, and the one currently on your device is tagged. History lives in GitHub, so nothing is ever lost.
- **Notes** — the note you type before uploading is saved with that version and shown in the list, so you can record what a save contains (e.g. "beat fire temple, 3 hearts").
- **Update badges** — the app polls the remote every ~15s and shows **⬇ Update available** on a game when another device has pushed a newer save than you last synced (the Download button pulses). It never downloads on its own — you still click Download. **✓ In sync** means you're at the latest.

> One rule keeps it clean: **download before you play, upload after.** Then history stays linear and nothing is ever lost. (If a device uploads while behind, it force-overwrites the branch tip — but every prior commit is still in the repo.)

## Finding your save folder

- **Steam Deck (Proton games):** `~/.local/share/Steam/steamapps/compatdata/<appid>/pfx/drive_c/users/steamuser/...` — the exact path varies per game (AppData/Documents inside the Proton prefix).
- **Windows:** commonly `%USERPROFILE%\AppData\Roaming\...`, `...\AppData\Local\...`, `Documents\My Games\...`, or `Saved Games\...`.
- [PCGamingWiki](https://www.pcgamingwiki.com/) lists the save location for most games on both platforms.
- Paths must be absolute; a leading `~` is expanded to your home directory (e.g. `~/.local/share/...`).

## Build from source

Requires Go 1.24+.

```bash
go test ./...            # round-trip + safety tests (uses a local bare repo)
go build -o dist/save-sync .

# cross-compile
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/save-sync.exe .
GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o dist/save-sync-linux .
```

## How it works

- Each configured save maps to a `<Name>/` subfolder in the repo.
- The tool keeps a managed clone per repo under the OS config dir (`repos/<hash>/`), so `.git` never lands in your actual save folder.
- **Upload:** fetch → reset clone to the remote tip → mirror your local folder into `<Name>/` (copying changes, propagating deletions) → commit → push (force only if the branch diverged).
- **Download:** fetch → hard-reset to the remote tip → mirror `<Name>/` back onto your local folder (pruning stale files). Refuses to run if the repo has no `<Name>/` yet, so it can't wipe an unmatched local folder.
- Git operations use pure-Go [go-git](https://github.com/go-git/go-git) — hence no git dependency on the device.

Config + token live in `<user-config-dir>/save-sync/config.json` (0600 perms).
