# -*- coding: utf-8 -*-
"""Create the distributable zip with UTF-8 filename flags (bsdtar omits the flag,
which makes Chinese filenames render as mojibake in some unzip tools)."""
import os
import sys
import zipfile

src = sys.argv[1]
out = sys.argv[2]
exclude_dirs = {"data"}

count = 0
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as z:
    for root, dirs, files in os.walk(src):
        rel_root = os.path.relpath(root, os.path.dirname(src))
        parts = rel_root.split(os.sep)
        dirs[:] = [d for d in dirs if d not in exclude_dirs]
        if any(p in exclude_dirs for p in parts):
            continue
        if not files and not dirs:
            continue
        z.writestr(rel_root.replace(os.sep, "/") + "/", b"")
        for name in files:
            full = os.path.join(root, name)
            rel = os.path.relpath(full, os.path.dirname(src)).replace(os.sep, "/")
            z.write(full, rel)
            count += 1
print("files:", count, "->", out, f"{os.path.getsize(out)/1048576:.1f} MB")
