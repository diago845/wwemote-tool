#!/usr/bin/env python3
"""Build the shippable WWEmote 1.6.0 package from source.

Release shape: the archive contains the emote mod assets plus WWEmoteTool.exe
and no user/ directory. Unpack anywhere under Mods/, run the exe, press
Configure; every produced file lands in <panel>/user.

    python build_release.py
    python build_release.py --check-only    # verify an existing zip

Requirements: Go 1.27+ on PATH and Python 3. Authoring assets live in
./assets, so a fresh clone rebuilds the exact package without any private
repository. Nothing under Mods/ is ever written by this script.
"""
import argparse
import hashlib
import json
import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
DIST = os.path.join(HERE, "assets")
STAGE = os.path.join(HERE, "release", "WWEmote")
ZIP = os.path.join(HERE, "release", "WWEmote-1.6.0-go.zip")


def go_cmd():
    """Resolve the Go toolchain from PATH; no machine-specific location."""
    return shutil.which("go.exe" if os.name == "nt" else "go") or "go"


# Installed panel folder to audit on build, so a backup ini cannot slip in
# when nobody thought to pass --mods. Unset by default: the public build has
# no author install to scan and audits only folders passed explicitly.
MODS_ROOT = ""

# Author assets that ship. user/ is deliberately excluded: it is the single
# durable root and must be generated fresh on the user's machine.
# VERSION is deliberately NOT shipped: the Go build never reads it, and an
# unreleased build has no version number to advertise.
ASSET_DIRS = ("Shaders", "Textures", "chars")
ASSET_FILES = ("WWEmote.ini",)

# Shaders that the shipped ini files actually reference. Two kinds of reference
# count: static (vs/ps/cs lines in WWEmote.ini and chars/*/*.ini) and dynamic
# (render/blocks.go writes ep_u5_shield.hlsl into the generated Mods ini at
# configure time). Any shader on disk that is not in this set is dead weight:
# 3DMigoto loads only what an ini names, so an orphan hlsl ships to the user
# and does nothing. Fail the build if the tree drifts in either direction.
SHADER_WHITELIST = (
    "ep_mod_delta_sparse.hlsl",  # static: chars/*, and generated Mods ini
    "ep_native_applier.hlsl",    # static: chars/*
    "ep_u5_shield.hlsl",         # dynamic: blocks.go writes cs= at configure
    "ui_2d.hlsl",                # static: WWEmote.ini
    "ui_tex_2d.hlsl",            # static: WWEmote.ini
)


def audit_shaders():
    src = os.path.join(DIST, "Shaders")
    if not os.path.isdir(src):
        raise SystemExit("missing dist shader dir: " + src)
    actual = set(os.listdir(src))
    want = set(SHADER_WHITELIST)
    # Missing is fatal: the panel would bind a shader that is not in the zip.
    missing = sorted(want - actual)
    if missing:
        raise SystemExit("release needs shaders that dist/ does not have: %s"
                         % missing)
    # Extra is NOT fatal and is NOT shipped. The author repo keeps abandoned
    # experiments next to live shaders (the retired zero-mod route, .bak /
    # .rejected copies); assert_shaders used to abort on those and took the
    # whole build down even though no ini referenced them.
    extra = sorted(actual - want)
    print("shaders: %d shipped%s" % (
        len(want), "; ignored %d unreferenced in dist: %s"
        % (len(extra), ", ".join(extra)) if extra else ""))


def sha256(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def pe_subsystem(path):
    """Read the PE OptionalHeader Subsystem: 2 = GUI, 3 = CUI (console)."""
    with open(path, "rb") as f:
        b = f.read()
    off = struct.unpack_from("<I", b, 0x3C)[0]
    if b[off:off + 4] != b"PE\0\0":
        raise SystemExit("not a PE binary: " + path)
    return struct.unpack_from("<H", b, off + 24 + 68)[0]


def build_exe():
    gofmt = go_cmd()
    env = dict(os.environ)
    out = os.path.join(HERE, "wwemote-test.exe")
    # -buildvcs=false keeps the binary reproducible: a fresh `git clone` has a
    # .git directory, and Go would otherwise stamp vcs.revision / vcs.modified
    # into the exe, changing its hash with every commit even when no source
    # byte moved. The expected sha256 in README.md assumes no vcs stamp.
    # -H=windowsgui links the GUI subsystem. Without it Windows always spawns a
    # console window before the WebView2 window, so the user sees two windows.
    subprocess.run([gofmt, "build", "-trimpath", "-buildvcs=false",
                    "-ldflags", "-H=windowsgui",
                    "-o", out, "./cmd/wwemote"], cwd=HERE, env=env, check=True)
    sub = pe_subsystem(out)
    if sub != 2:
        raise SystemExit("exe subsystem is %d (GUI=2); console window will appear" % sub)
    print("exe subsystem: GUI (2)")
    return out


def frozen_char_dirs():
    """Static asset folders of characters frozen in chars.json.

    A frozen character keeps its source and its ini, but must not ship: the
    generator drops it (panel.Load skips `frozen`), and its chars/<Name>/*.ini
    carries hash-keyed TextureOverrides that would still hook the character
    through include_recursive even with no generated sections. Read the
    single source of truth rather than hard-coding a name here.
    """
    p = os.path.join(HERE, "internal", "panel", "data", "chars.json")
    try:
        with open(p, encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, ValueError) as e:
        raise SystemExit("cannot read chars.json for the frozen list: %s" % e)
    return tuple(c["name"][:1].upper() + c["name"][1:]
                 for c in data.get("chars", []) if c.get("frozen"))


TOK_SEC_RE = re.compile(r"\[ResourceEpTok(\d{3})\]")
REPO_VAL_RE = re.compile(r"(\$ep_repo\d+) = (-?\d+)")


def _char_ini(chars_root, name):
    return os.path.join(chars_root, name, name + ".ini")


def _token_ids(path):
    with open(path, encoding="utf-8-sig") as f:
        return sorted(int(m) for m in TOK_SEC_RE.findall(f.read()))


def assemble(exe):
    if os.path.isdir(STAGE):
        shutil.rmtree(STAGE)
    os.makedirs(STAGE)
    frozen = frozen_char_dirs()
    for d in ASSET_DIRS:
        src = os.path.join(DIST, d)
        if not os.path.isdir(src):
            raise SystemExit("missing dist asset dir: " + src)
        # chars/*/native_delta/*.json is a machine-local bake cache: the shipped
        # panel only needs the .buf. Shipping the cache put a stale stamp and a
        # Chinese note in the package (the audit refuses both), and the file is
        # regenerated on the user side anyway.
        char_ignore = list(frozen) + ["*_manifest.json"]
        ignore = shutil.ignore_patterns(*char_ignore) if d == "chars" else None
        if d == "Shaders":
            # Ship exactly the whitelisted shaders. dist/Shaders carries
            # experiments no ini references; copying the folder verbatim put
            # them in the zip and made verify() refuse the build.
            dst = os.path.join(STAGE, d)
            os.makedirs(dst, exist_ok=True)
            for nm in SHADER_WHITELIST:
                shutil.copy2(os.path.join(src, nm), os.path.join(dst, nm))
            continue
        shutil.copytree(src, os.path.join(STAGE, d), ignore=ignore)
    for f in ASSET_FILES:
        src = os.path.join(DIST, f)
        # The main ini names every character's command lists and globals. With
        # a character frozen but its ini text still present, `$ep_chr_<name>`
        # is never declared: 3DMigoto drops the whole `if` and the misplaced
        # `endif` shifts every later command into the wrong scope. Ship the
        # frozen snapshot instead of the live one.
        if frozen and f == "WWEmote.ini":
            # The snapshot lives beside this script first: the author repo
            # rebuilds dist/ from scratch and deletes anything it did not emit,
            # so a snapshot kept only in dist/ disappears on the next build.
            cands = [os.path.join(HERE, "WWEmote.frozen.ini"),
                     os.path.join(DIST, "WWEmote.frozen.ini")]
            alt = next((c for c in cands if os.path.isfile(c)), "")
            if not alt:
                raise SystemExit(
                    "frozen characters %s need a main ini without their "
                    "sections; looked for %s" % (", ".join(frozen), cands))
            src = alt
        if not os.path.isfile(src):
            raise SystemExit("missing dist asset file: " + src)
        # The snapshot lands under the shipped name, not its dist name.
        shutil.copy2(src, os.path.join(STAGE, f))
    shutil.copy2(exe, os.path.join(STAGE, "WWEmoteTool.exe"))
    if frozen:
        print("frozen characters excluded from the release: " + ", ".join(frozen))


def make_zip():
    os.makedirs(os.path.dirname(ZIP), exist_ok=True)
    if os.path.isfile(ZIP):
        os.remove(ZIP)
    with zipfile.ZipFile(ZIP, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for root, _dirs, files in os.walk(STAGE):
            for f in files:
                p = os.path.join(root, f)
                z.write(p, os.path.relpath(p, os.path.dirname(STAGE)).replace("\\", "/"))


def deployed_panels(explicit=None):
    """Panel folders to audit: explicit --mods, else every install under
    MODS_ROOT that carries WWEmote.ini (legacy name, DISABLED_ prefix and
    versioned folders all count -- a stale ini in any of them is loaded).
    """
    if explicit:
        return [explicit]
    if not MODS_ROOT or not os.path.isdir(MODS_ROOT):
        return []
    found = []
    for name in sorted(os.listdir(MODS_ROOT)):
        top = os.path.join(MODS_ROOT, name)
        if os.path.isfile(os.path.join(top, "WWEmote.ini")):
            found.append(top)
            continue
        if os.path.isdir(top):
            for sub in sorted(os.listdir(top)):
                inner = os.path.join(top, sub)
                if os.path.isfile(os.path.join(inner, "WWEmote.ini")):
                    found.append(inner)
    return found


def verify(mods=None):
    if not os.path.isfile(ZIP):
        raise SystemExit("zip not found: " + ZIP)
    with zipfile.ZipFile(ZIP) as z:
        names = z.namelist()
        bad = [n for n in names if n.startswith("WWEmote/user/") or n.endswith(".py")]
        if bad:
            raise SystemExit("release contains forbidden entries: " + ", ".join(bad[:5]))
        if not any(n == "WWEmote/WWEmoteTool.exe" for n in names):
            raise SystemExit("release is missing WWEmoteTool.exe")
        if not any(n == "WWEmote/WWEmote.ini" for n in names):
            raise SystemExit("release is missing WWEmote.ini")
        # guardPanel() accepts the panel when any of these exists; the scanner
        # needs chars/, and the shaders are part of the emote pipeline.
        for need in ("WWEmote/chars/", "WWEmote/Shaders/"):
            if not any(n.startswith(need) for n in names):
                raise SystemExit("release is missing " + need)
        # A frozen character must leave no static ini behind: those files carry
        # hash-keyed TextureOverrides and would keep hooking the character even
        # though the generator no longer emits its sections.
        for frozen in frozen_char_dirs():
            stray = [n for n in names if n.startswith("WWEmote/chars/" + frozen + "/")]
            if stray:
                raise SystemExit("frozen character %s leaked into the release: %s"
                                 % (frozen, ", ".join(stray[:3])))
            # The main ini must not name a frozen character either: an
            # undeclared `$ep_chr_<name>` kills the enclosing `if` and shifts
            # scope for every later command (silent, no error line).
            ini_text = z.read("WWEmote/WWEmote.ini").decode("utf-8", "replace")
            leaked = [ln for ln in ini_text.splitlines() if frozen.lower() in ln.lower()]
            if leaked:
                raise SystemExit(
                    "WWEmote.ini still references frozen character %s (%d line(s)), "
                    "e.g.: %s" % (frozen, len(leaked), leaked[0].strip()))
        # Token ids index the shader token group. Each character owns a
        # FIXED block (chars/__init__.py TOKEN_BASE), so a
        # frozen character leaves a hole instead of shifting everyone -- that
        # shift was exactly the 0.62 bug (empty shelf cells, icons two slots
        # late). What the package must guarantee is therefore: ids are unique,
        # and every shipped id is reachable through the main ini's bind chain.
        # An id with no `elif $ep_bind_tok == <id>` draws as a blank icon.
        tok_ids = []
        for n in names:
            if n.startswith("WWEmote/chars/") and n.endswith(".ini"):
                tok_ids.extend(int(m) for m in TOK_SEC_RE.findall(
                    z.read(n).decode("utf-8-sig", "replace")))
        if tok_ids:
            dup = sorted({t for t in tok_ids if tok_ids.count(t) > 1})
            if dup:
                raise SystemExit("token ids collide across characters: %s" % dup)
            bind_ids = {int(m) for m in re.findall(
                r"ep_bind_tok\s*==\s*(\d+)",
                z.read("WWEmote/WWEmote.ini").decode("utf-8", "replace"))}
            unreachable = sorted(set(tok_ids) - bind_ids)
            if unreachable:
                raise SystemExit(
                    "main ini cannot bind token ids %s (icons would draw blank)"
                    % unreachable)
        shipped_shaders = set(
            n[len("WWEmote/Shaders/"):] for n in names
            if n.startswith("WWEmote/Shaders/") and not n.endswith("/")
        )
        if shipped_shaders != set(SHADER_WHITELIST):
            raise SystemExit("shipped shader set drift -- extra: %s; missing: %s" % (
                sorted(shipped_shaders - set(SHADER_WHITELIST)) or "none",
                sorted(set(SHADER_WHITELIST) - shipped_shaders) or "none"))
        # Unreleased build: no version number anywhere in the shipped assets.
        if any(n == "WWEmote/VERSION" for n in names):
            raise SystemExit("release must not ship a standalone VERSION file")
        if "EP-PANEL-VERSION" in z.read("WWEmote/WWEmote.ini").decode("utf-8", "replace"):
            raise SystemExit("WWEmote.ini still advertises EP-PANEL-VERSION")
        exe_bytes = z.read("WWEmote/WWEmoteTool.exe")
        off = struct.unpack_from("<I", exe_bytes, 0x3C)[0]
        if exe_bytes[off:off + 4] != b"PE\0\0":
            raise SystemExit("WWEmoteTool.exe in zip is not a PE binary")
        sub = struct.unpack_from("<H", exe_bytes, off + 24 + 68)[0]
        if sub != 2:
            raise SystemExit("WWEmoteTool.exe in zip has subsystem %d (GUI=2); "
                             "a console window will appear" % sub)
        for panel in deployed_panels(mods):
            audit_release(z, panel)
    size = os.path.getsize(ZIP)
    print("OK %s -- %d entries, %.1f MB, sha256 %s" % (
        os.path.basename(ZIP), len(names), size / 1048576, sha256(ZIP)))
    return names


def audit_release(z, mods=None):
    """Run the build-time wwaudit tool over the unpacked package.

    The rule lives in Go (internal/guard, checked by `go test`) so there is
    exactly one definition of it. The auditor is a separate command that is
    compiled into a temp dir and never shipped -- a finished product must not
    carry the rule it is checked against. Two outcomes fail the build: an
    artifact whose comments or metadata carry non-ASCII prose, and a manifest
    another build produced (its tag would ship to the user).

    When mods is given it must be a DEPLOYED panel folder. include_recursive
    loads every .ini below it, so a backup left there tears the model with no
    error at build time. That check is deliberately wired into the build so a
    stray ini cannot be shipped past a green build.
    """
    go = go_cmd()
    env = dict(os.environ)
    with tempfile.TemporaryDirectory() as tmp:
        z.extractall(tmp)
        root = os.path.join(tmp, "WWEmote")
        tool = os.path.join(tmp, "wwaudit.exe" if os.name == "nt" else "wwaudit")
        subprocess.run([go, "build", "-o", tool, "./cmd/wwaudit"],
                       cwd=HERE, env=env, check=True)
        cmd = [tool, root]
        if mods:
            if not os.path.isdir(mods):
                raise SystemExit("--mods is not a directory: " + mods)
            cmd += ["--mods", mods]
        r = subprocess.run(cmd, capture_output=True,
                           encoding="utf-8", errors="replace")
        if r.returncode != 0:
            raise SystemExit("audit failed (exit %d):\n%s%s"
                             % (r.returncode, r.stdout, r.stderr))
        print(r.stdout.strip())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--check-only", action="store_true")
    ap.add_argument("--mods", default=None,
                    help="deployed panel folder to audit for stray ini leaves")
    args = ap.parse_args()
    if not args.check_only:
        audit_shaders()
        exe = build_exe()
        try:
            assemble(exe)
            make_zip()
        finally:
            # The intermediate binary is ~11 MB and used to be left in the
            # repo root after every build. The shipped copy is in the zip.
            if os.path.isfile(exe):
                os.remove(exe)
    verify(args.mods)


if __name__ == "__main__":
    main()
