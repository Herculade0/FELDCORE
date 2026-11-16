#!/bin/bash
# Mod sync for the FELDCORE Valheim template.
# The image's PRE_SERVER_RUN_HOOK runs this on every server start.
# It always exits 0. A Thunderstore failure leaves the last good mod set
# and the Valheim server still starts.
#
# Spec: docs/FELDCORE-STORE.md, "First template: modded Valheim".

set -u

log() { printf '%s thunderstore-sync: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
warn() { log "warning: $*"; }

if ! command -v python3 >/dev/null 2>&1; then
  warn "python3 is missing; trying to install it"
  if ! apt-get update -qq || ! DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3; then
    warn "python3 is not available; leaving the current mods in place"
    exit 0
  fi
fi

export RCON_PORT="${RCON_PORT:-2459}"
export RCON_MOD_VERSION="${RCON_MOD_VERSION:-1.6.4}"

python3 - << 'PY'
import base64, io, json, os, shutil, time, urllib.request, zipfile
from pathlib import Path

PROFILE_URL = "https://thunderstore.io/api/experimental/legacyprofile/get/{code}/"
DOWNLOAD_URL = "https://thunderstore.io/package/download/{ns}/{name}/{version}/"
INDEX_URL = "https://thunderstore.io/c/valheim/api/v1/package/"
PREFIX = "#r2modman\n"
ROOT = Path("/config/feldcore")
CACHE = ROOT / "cache"
CATS = ROOT / "categories.json"
MANAGED = ROOT / "managed.txt"
PLUGINS = Path("/config/bepinex/plugins")
LIVE = Path("/opt/valheim/bepinex")
LIVE_PLUGINS = LIVE / "BepInEx" / "plugins"
RCON_NAMES = {"Tristan-ValheimRcon", "ferrinius-RCON_Next", "AviiNL-rcon"}
PACK = "denikson-BepInExPack_Valheim"
RCON_DEFAULT = "Tristan-ValheimRcon"
RCON_VERSION = os.environ.get("RCON_MOD_VERSION", "1.6.4")
RCON_PORT = os.environ.get("RCON_PORT", "2459")
RCON_PASSWORD = os.environ.get("RCON_PASSWORD", "")

def log(msg):
    print("thunderstore-sync: " + msg, flush=True)

def warn(msg):
    log("warning: " + msg)

def fetch(url, timeout):
    req = urllib.request.Request(url, headers={"User-Agent": "FELDCORE"})
    with urllib.request.urlopen(req, timeout=timeout) as res:
        return res.read()

def split_name(full):
    if "-" not in full:
        raise ValueError("mod name has no namespace: " + full)
    ns, name = full.split("-", 1)
    if not ns or not name:
        raise ValueError("mod name is incomplete: " + full)
    return ns, name

def version_of(mod):
    parts = [mod.get("major"), mod.get("minor"), mod.get("patch")]
    if any(p is None for p in parts):
        raise ValueError("mod is missing a version: " + mod.get("name", ""))
    return ".".join(str(int(p)) for p in parts)

def parse_export(text):
    if "mods:" not in text:
        raise ValueError("export.r2x has no mods list")
    mods = []
    cur = None
    in_version = False
    for raw in text.splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        line = raw.strip()
        if line.startswith("- name:"):
            if cur:
                mods.append(cur)
            cur = {"name": line.split(":", 1)[1].strip().strip("\"'"), "enabled": True}
            in_version = False
        elif cur is None:
            continue
        elif line.startswith("enabled:"):
            cur["enabled"] = line.split(":", 1)[1].strip().lower() == "true"
            in_version = False
        elif line.startswith("version:"):
            rest = line.split(":", 1)[1].strip().strip("\"'")
            parts = rest.split(".") if rest else []
            if len(parts) >= 3 and all(p.isdigit() for p in parts[:3]):
                cur["major"], cur["minor"], cur["patch"] = (int(p) for p in parts[:3])
                in_version = False
            else:
                in_version = True
        elif in_version and line.startswith(("major:", "minor:", "patch:")):
            key, val = line.split(":", 1)
            cur[key] = int(val.strip())
    if cur:
        mods.append(cur)
    return mods

def load_categories():
    fresh = CATS.exists() and time.time() - CATS.stat().st_mtime < 24 * 3600
    if fresh:
        return json.loads(CATS.read_text())
    try:
        raw = fetch(INDEX_URL, 120)
        slim = {}
        for pkg in json.loads(raw):
            full = pkg.get("full_name")
            if full:
                slim[full] = pkg.get("categories") or []
        CATS.parent.mkdir(parents=True, exist_ok=True)
        CATS.write_text(json.dumps(slim))
        return slim
    except Exception as exc:
        warn("Valheim package index unavailable ({})".format(exc))
        if CATS.exists():
            warn("using the cached category list")
            return json.loads(CATS.read_text())
        warn("client-only filtering skipped for this start")
        return None

def client_only(cats):
    low = {c.lower() for c in cats}
    return "client-side" in low and "server-side" not in low

def safe_zip(zf, dest):
    dest = dest.resolve()
    dest.mkdir(parents=True, exist_ok=True)
    for info in zf.infolist():
        name = info.filename.replace("\\", "/")
        if name.endswith("/"):
            continue
        parts = Path(name).parts
        if not parts or name.startswith("/") or ".." in parts:
            raise ValueError("zip path refused: " + name)
        target = (dest / name).resolve()
        if dest != target and dest not in target.parents:
            raise ValueError("zip path refused: " + name)
        target.parent.mkdir(parents=True, exist_ok=True)
        with zf.open(info) as src, open(target, "wb") as out:
            shutil.copyfileobj(src, out)

def download_mod(full, version):
    ns, name = split_name(full)
    dest = CACHE / "{}-{}.zip".format(full, version)
    if dest.exists() and dest.stat().st_size > 0 and dest.read_bytes()[:2] == b"PK":
        return dest
    CACHE.mkdir(parents=True, exist_ok=True)
    tmp = dest.with_suffix(".zip.partial")
    data = fetch(DOWNLOAD_URL.format(ns=ns, name=name, version=version), 60)
    if data[:2] != b"PK":
        raise ValueError("download was not a zip: " + full)
    tmp.write_bytes(data)
    tmp.replace(dest)
    return dest

def install_zip(path, full, stage_plugins, stage_bepinex):
    with zipfile.ZipFile(path) as zf:
        names = [n.replace("\\", "/") for n in zf.namelist()]
        pack = full == PACK or any(n.startswith("BepInEx/") for n in names)
        if pack:
            safe_zip(zf, stage_bepinex)
            return "pack"
        plugin_root = stage_plugins / full
        # Prefer a plugins/ folder inside the zip. Otherwise copy DLLs.
        has_plugins = any(n.startswith("plugins/") for n in names)
        if has_plugins:
            safe_zip(zf, plugin_root)
            nested = plugin_root / "plugins"
            if nested.is_dir():
                for child in nested.iterdir():
                    target = plugin_root / child.name
                    if target.exists():
                        shutil.rmtree(target) if target.is_dir() else target.unlink()
                    shutil.move(str(child), str(target))
                nested.rmdir()
        else:
            plugin_root.mkdir(parents=True, exist_ok=True)
            for info in zf.infolist():
                name = info.filename.replace("\\", "/")
                if name.endswith(".dll") and ".." not in Path(name).parts:
                    target = plugin_root / Path(name).name
                    with zf.open(info) as src, open(target, "wb") as out:
                        shutil.copyfileobj(src, out)
        dlls = list(plugin_root.rglob("*.dll"))
        if not dlls:
            warn("no plugin files in " + full)
        return "plugin"

def place(src, dest):
    # New tree is complete in a sibling directory before the old one is removed.
    tmp = dest.with_name(dest.name + ".new")
    if tmp.exists():
        shutil.rmtree(tmp)
    copy_tree(src, tmp)
    if dest.exists():
        shutil.rmtree(dest)
    tmp.rename(dest)

def copy_tree(src, dest):
    if not src.exists():
        return
    dest.mkdir(parents=True, exist_ok=True)
    for dirpath, _, files in os.walk(src):
        rel = Path(dirpath).relative_to(src)
        target_dir = dest / rel
        target_dir.mkdir(parents=True, exist_ok=True)
        for filename in files:
            shutil.copy2(Path(dirpath) / filename, target_dir / filename)

def write_rcon():
    if not RCON_PASSWORD or any(c in RCON_PASSWORD for c in "\r\n"):
        warn("RCON password is empty or has a newline; ValheimRcon will not listen")
        return
    text = "[1. Rcon]\n\nPort = {}\nPassword = {}\n".format(RCON_PORT, RCON_PASSWORD)
    for folder in (LIVE / "BepInEx" / "config", Path("/config/bepinex/BepInEx/config")):
        try:
            folder.mkdir(parents=True, exist_ok=True)
            (folder / "org.tristan.rcon.cfg").write_text(text)
        except OSError as exc:
            warn("could not write RCON config in {}: {}".format(folder, exc))

def read_managed():
    if not MANAGED.exists():
        return []
    return [line.strip() for line in MANAGED.read_text().splitlines() if line.strip()]

def remove_plugin(name):
    for folder in (PLUGINS / name, LIVE_PLUGINS / name):
        if folder.is_dir():
            shutil.rmtree(folder)

def main():
    code = os.environ.get("THUNDERSTORE_PROFILE", "").strip()
    uuid = "0123456789abcdefABCDEF-"
    ok_uuid = len(code) == 36 and code[8] == code[13] == code[18] == code[23] == "-" and all(c in uuid for c in code.replace("-", ""))
    if not ok_uuid:
        warn("profile code is blank or not a UUID; leaving the current mods in place")
        write_rcon()
        return
    try:
        body = fetch(PROFILE_URL.format(code=code), 30)
    except Exception as exc:
        warn("Thunderstore profile unreachable ({}); starting with the last good mod set".format(exc))
        write_rcon()
        return
    text = body.decode("utf-8", "replace")
    if not text.startswith(PREFIX):
        warn("profile body is not an r2modman export; starting with the last good mod set")
        write_rcon()
        return
    try:
        raw = base64.b64decode(text[len(PREFIX):], validate=False)
        stage = ROOT / "profile"
        if stage.exists():
            shutil.rmtree(stage)
        stage.mkdir(parents=True)
        with zipfile.ZipFile(io.BytesIO(raw)) as zf:
            safe_zip(zf, stage)
        export = (stage / "export.r2x").read_text()
        mods = [m for m in parse_export(export) if m.get("enabled", True)]
    except Exception as exc:
        warn("profile could not be read ({}); starting with the last good mod set".format(exc))
        write_rcon()
        return

    if not any(m["name"] in RCON_NAMES for m in mods):
        mods.append({"name": RCON_DEFAULT, "major": RCON_VERSION.split(".")[0], "minor": RCON_VERSION.split(".")[1], "patch": RCON_VERSION.split(".")[2], "enabled": True})
        log("adding {} {} because the profile has no RCON plugin".format(RCON_DEFAULT, RCON_VERSION))
    elif any(m["name"] == "AviiNL-rcon" for m in mods):
        warn("profile uses AviiNL-rcon, last updated in 2024; not replacing it")

    cats = load_categories()
    chosen = []
    for mod in mods:
        full = mod["name"]
        if cats is not None and full != PACK and full not in RCON_NAMES:
            known = cats.get(full)
            if known is None:
                warn("installed {}; not in the Valheim package index".format(full))
            elif client_only(known):
                warn("skipped client-only {}".format(full))
                continue
            elif "server-side" not in {c.lower() for c in known}:
                warn("installed {}; categories do not say client or server".format(full))
        try:
            chosen.append((full, version_of(mod)))
        except Exception as exc:
            warn("profile version unusable ({}); starting with the last good mod set".format(exc))
            write_rcon()
            return

    downloaded = []
    try:
        for full, version in chosen:
            downloaded.append((full, version, download_mod(full, version)))
    except Exception as exc:
        warn("download failed ({}); starting with the last good mod set".format(exc))
        write_rcon()
        return

    stage_plugins = ROOT / "stage-plugins"
    stage_bepinex = ROOT / "stage-bepinex"
    shutil.rmtree(stage_plugins, ignore_errors=True)
    shutil.rmtree(stage_bepinex, ignore_errors=True)
    stage_plugins.mkdir(parents=True)
    try:
        managed_now = []
        for full, version, path in downloaded:
            kind = install_zip(path, full, stage_plugins, stage_bepinex)
            if kind == "plugin":
                managed_now.append(full)
        # Config files from the profile, except the RCON file this script owns.
        profile_cfg = ROOT / "profile" / "config"
        if profile_cfg.is_dir():
            for cfg in profile_cfg.rglob("*"):
                if cfg.is_file() and cfg.name != "org.tristan.rcon.cfg":
                    rel = cfg.relative_to(profile_cfg)
                    for dest_root in (LIVE / "BepInEx" / "config", Path("/config/bepinex/BepInEx/config")):
                        target = dest_root / rel
                        target.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copy2(cfg, target)
        copy_tree(stage_bepinex / "BepInEx", LIVE / "BepInEx")
        PLUGINS.mkdir(parents=True, exist_ok=True)
        # Copy each new plugin into place before deleting anything the profile dropped.
        for name in managed_now:
            place(stage_plugins / name, PLUGINS / name)
            if LIVE_PLUGINS.exists():
                place(stage_plugins / name, LIVE_PLUGINS / name)
        for name in sorted(set(read_managed()) - set(managed_now)):
            remove_plugin(name)
            log("removed " + name)
        MANAGED.parent.mkdir(parents=True, exist_ok=True)
        MANAGED.write_text("".join(n + "\n" for n in managed_now))
        log("installed {} mod{}".format(len(managed_now), "" if len(managed_now) == 1 else "s"))
    except Exception as exc:
        warn("install failed ({}); starting with the last good mod set".format(exc))
    finally:
        write_rcon()

main()
PY
exit 0
