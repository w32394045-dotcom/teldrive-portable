# -*- coding: utf-8 -*-
"""Verify a distributable bundle zip before publishing it.

Fails (exit 1) when something is missing or when the archive would leak
secrets: a published bundle must ship placeholder keys that are generated on
the user's machine at first run.

    python tools/verify_bundle.py dist/teldrive-2-win64.zip
"""
import sys
import zipfile

REQUIRED = [
    "teldrive.exe",
    "config.toml",
    "start.bat",
    "stop.bat",
    "_open-browser.bat",
    "_init-keys.bat",
    "使用说明.txt",
    "pgsql/bin/postgres.exe",
    "pgsql/bin/initdb.exe",
    "pgsql/bin/pg_ctl.exe",
]

FORBIDDEN_PARTS = ("/data/", "pgdata", "postgres.log", "initdb.log", "/node_modules/")

PLACEHOLDERS = ("__SIGNING_KEY__", "__DATA_KEY__")


def main(path: str) -> int:
    with zipfile.ZipFile(path) as z:
        names = z.namelist()
        bad_crc = z.testzip()
        roots = {name.split("/")[0] for name in names if "/" in name}
        root = next(iter(roots)) if len(roots) == 1 else ""

        def read(member: str) -> bytes:
            full = f"{root}/{member}"
            return z.read(full) if full in names else b""

        config_bytes = read("config.toml")
        exe_size = len(read("teldrive.exe"))
        pg_size = len(read("pgsql/bin/postgres.exe"))
        start_bytes = len(read("start.bat"))

    problems = []

    if bad_crc:
        problems.append(f"corrupt entry: {bad_crc}")

    if len(roots) != 1:
        problems.append(f"expected a single top-level folder, found {sorted(roots)}")

    for suffix in REQUIRED:
        if f"{root}/{suffix}" not in names:
            problems.append(f"missing required file: {suffix}")

    for name in names:
        lowered = name.lower()
        for part in FORBIDDEN_PARTS:
            if part in lowered:
                problems.append(f"forbidden entry in bundle: {name}")

    config = config_bytes.decode("utf-8", errors="replace")
    for placeholder in PLACEHOLDERS:
        if placeholder not in config:
            problems.append(
                f"config.toml does not contain {placeholder}: refusing to publish a bundle "
                "that may carry real signing/data keys"
            )

    if exe_size < 20_000_000:
        problems.append(f"teldrive.exe looks too small ({exe_size} bytes)")
    if pg_size < 1_000_000:
        problems.append(f"postgres.exe looks too small ({pg_size} bytes)")

    print(f"entries        : {len(names)}")
    print(f"top-level      : {root}")
    print(f"teldrive.exe   : {exe_size / 1048576:.1f} MB")
    print(f"postgres.exe   : {pg_size / 1048576:.1f} MB")
    print(f"start.bat bytes: {start_bytes}")
    if problems:
        print("\nBUNDLE VERIFICATION FAILED:")
        for row in problems:
            print("  -", row)
        return 1
    print("\nbundle OK: complete, and keys are placeholders (generated on first run)")
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(__doc__)
        sys.exit(2)
    sys.exit(main(sys.argv[1]))
