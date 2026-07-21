import asyncio
import json
import os
import pwd
import stat

import decky


def _bin_path() -> str:
    return os.path.join(os.environ["DECKY_PLUGIN_DIR"], "bin", "save-sync")


def _run_env() -> dict:
    # Decky runs plugin backends as root; point the binary at the deck user's
    # home so it reads/writes the same config + clones as the desktop web app.
    home = os.environ.get("DECKY_USER_HOME") or os.path.expanduser("~")
    env = dict(os.environ)
    env["HOME"] = home
    env["SAVESYNC_CONFIG_DIR"] = os.path.join(home, ".config", "save-sync")
    return env


def _drop_ids():
    # Run the binary as the deck user so files stay owned by them (not root).
    user = os.environ.get("DECKY_USER")
    if not user:
        return (None, None)
    try:
        pw = pwd.getpwnam(user)
        return (pw.pw_uid, pw.pw_gid)
    except KeyError:
        return (None, None)


async def _run(*args) -> dict:
    """Run the save-sync binary and return {"data": <parsed>} or {"error": str}."""
    binary = _bin_path()
    if not os.path.exists(binary):
        return {"error": f"save-sync binary missing at {binary}"}

    uid, gid = _drop_ids()
    kwargs = {}
    if uid is not None:
        kwargs["user"] = uid
        kwargs["group"] = gid

    try:
        proc = await asyncio.create_subprocess_exec(
            binary, *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            env=_run_env(),
            **kwargs,
        )
        out, err = await proc.communicate()
    except Exception as e:
        return {"error": str(e)}

    text = out.decode("utf-8", "replace").strip()
    if proc.returncode != 0:
        # The binary emits {"error": ...} on stdout for handled failures.
        try:
            return {"error": json.loads(text).get("error", text)}
        except Exception:
            return {"error": err.decode("utf-8", "replace").strip() or text or f"exit {proc.returncode}"}
    try:
        return {"data": json.loads(text)}
    except Exception as e:
        return {"error": f"unexpected output: {e}"}


class Plugin:
    async def list_syncs(self) -> dict:
        r = await _run("list")
        return {"error": r["error"]} if "error" in r else {"syncs": r["data"]}

    async def remote_status(self) -> dict:
        r = await _run("status")
        return {"error": r["error"]} if "error" in r else {"statuses": r["data"]}

    async def upload(self, id: str, note: str = "") -> dict:
        args = ["upload", id]
        if note:
            args += ["--note", note]
        r = await _run(*args)
        return {"error": r["error"]} if "error" in r else {"result": r["data"].get("result", "Uploaded.")}

    async def download(self, id: str, hash: str = "") -> dict:
        args = ["download", id]
        if hash:
            args += ["--hash", hash]
        r = await _run(*args)
        return {"error": r["error"]} if "error" in r else {"result": r["data"].get("result", "Downloaded.")}

    async def _main(self):
        binary = _bin_path()
        try:
            mode = os.stat(binary).st_mode
            os.chmod(binary, mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
        except FileNotFoundError:
            decky.logger.error(f"save-sync binary not found at {binary}")
        decky.logger.info("Save Sync plugin loaded")

    async def _unload(self):
        decky.logger.info("Save Sync plugin unloaded")

    async def _migration(self):
        pass
