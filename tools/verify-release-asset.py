# -*- coding: utf-8 -*-
"""Inspects a released Windows bundle before trusting it.

Checks that the zip has the expected layout, that the config still carries the
first-run key placeholders, and that the brotli siblings actually made it into
the embedded filesystem inside teldrive.exe (which is what proves the whole
compression path shipped, not just the source).
"""
import os
import sys
import zipfile

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
ZIP = sys.argv[1] if len(sys.argv) > 1 else os.path.join(WS, "dist", "released-v2.1.0.zip")
RETIRED_SCRIPTS = ("start.bat", "stop.bat", "_open-browser.bat", "_init-keys.bat")

with zipfile.ZipFile(ZIP) as archive:
    names = archive.namelist()
    print("entries:", len(names))
    print("top level:", sorted({name.split("/")[0] for name in names})[:5])
    for required in ("teldrive.exe", "config.toml", "使用说明.txt"):
        print(f"  has {required}:", any(name.endswith(required) for name in names))
    print("  ships runtime data/:", any("/data/" in name for name in names))
    # The whole point of this release: exactly one entry point.
    for script in RETIRED_SCRIPTS:
        print(f"  ships {script}:", any(name.endswith(script) for name in names))
    print("  ships any .bat at all:", any(name.lower().endswith(".bat") for name in names))

    exe_name = next(name for name in names if name.endswith("teldrive.exe"))
    blob = archive.read(exe_name)
    print("exe size MB:", round(len(blob) / 1024 / 1024, 1))
    # The embedded brotli siblings are the proof that compression shipped.
    for marker in (b".js.br", b".css.br", b"index.html.br", b"Accept-Encoding", b"precompressed"):
        print(f"  binary contains {marker!r}:", marker in blob)

    config = archive.read(next(name for name in names if name.endswith("config.toml")))
    text = config.decode("utf-8", "replace")
    print("  signing-key placeholder:", "__SIGNING_KEY__" in text)
    print("  data-key placeholder:", "__DATA_KEY__" in text)
