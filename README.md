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

Repeat the setup (token + add save) on the second device, using the **same repo URL and the same Name**, but that device's own local folder.

## Everyday use

- On the device you just played on: **⬆ Upload save**.
- On the other device, before playing: **⬇ Download save** (overwrites its local folder with the latest).
- **Versions** lists every upload; history lives in GitHub, so you can always recover an older save from the repo.

> One rule keeps it clean: **download before you play, upload after.** Then history stays linear and nothing is ever lost. (If a device uploads while behind, it force-overwrites the branch tip — but every prior commit is still in the repo.)

## Finding your save folder

- **Steam Deck (Proton games):** `~/.local/share/Steam/steamapps/compatdata/<appid>/pfx/drive_c/users/steamuser/...` — the exact path varies per game (AppData/Documents inside the Proton prefix).
- **Windows:** commonly `%USERPROFILE%\AppData\Roaming\...`, `...\AppData\Local\...`, `Documents\My Games\...`, or `Saved Games\...`.
- [PCGamingWiki](https://www.pcgamingwiki.com/) lists the save location for most games on both platforms.

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
