# -*- coding: utf-8 -*-
"""End-to-end proof that the released bundle has exactly one entry point.

Builds a throw-away bundle from real PostgreSQL binaries (linked, not copied),
starts `teldrive.exe` with no arguments and no launcher script, and checks that
the executable on its own:

  * generates first-run keys into config.toml (placeholders replaced),
  * initialises and starts the bundled PostgreSQL on the configured port,
  * creates the database and serves the web UI with brotli negotiation,
  * exposes the WebDAV tree and refuses it while the toggle is off,
  * honours a WebDAV toggle persisted across a restart,
  * stops the PostgreSQL it started when asked to shut down.

Ports are shifted off the defaults so this never collides with an installed
copy. Everything lives under dist/e2e-bundle and is deleted afterwards unless
--keep is passed.

    python tools/e2e-bundle.py            # uses dist/e2e-bundle
    python tools/e2e-bundle.py --keep     # leave the bundle in place
"""
import http.client
import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import time

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
BUNDLE = os.path.join(WS, "dist", "e2e-bundle")
EXE_SOURCE = os.path.join(WS, "dist", "teldrive-e2e.exe")
PG_SOURCE = r"C:\Users\ptfm\teldrive\pgsql"

HTTP_PORT = 8089
PG_PORT = 5434
DAV_PORT = 5434

KEEP = "--keep" in sys.argv
results = []


def check(name, ok, detail=""):
    results.append((name, ok, detail))
    print(f"{'PASS' if ok else 'FAIL'}  {name}" + ("" if ok or not detail else f"  -> {detail}"))


def port_open(port):
    with socket.socket() as sock:
        sock.settimeout(1.0)
        return sock.connect_ex(("127.0.0.1", port)) == 0


def header(headers, name):
    """HTTP header names are case insensitive; Go sends Www-Authenticate."""
    for key, value in headers.items():
        if key.lower() == name.lower():
            return value
    return None


def request(method, path, headers=None, body=None):
    connection = http.client.HTTPConnection("127.0.0.1", HTTP_PORT, timeout=15)
    try:
        connection.request(method, path, body=body, headers=headers or {})
        response = connection.getresponse()
        return response.status, dict(response.getheaders()), response.read()
    finally:
        connection.close()


class Server:
    """Starts the bundle's exe the way a user does: no arguments at all."""

    def __init__(self):
        self.process = None
        self.log_path = os.path.join(BUNDLE, "e2e-console.log")
        self.log = None

    def start(self, timeout=240):
        env = dict(os.environ)
        env["TELDRIVE_NO_PAUSE"] = "1"
        self.log = open(self.log_path, "ab")
        self.process = subprocess.Popen(
            [os.path.join(BUNDLE, "teldrive.exe")],
            cwd=BUNDLE, stdout=self.log, stderr=subprocess.STDOUT, env=env,
            creationflags=subprocess.CREATE_NEW_PROCESS_GROUP,
        )
        deadline = time.time() + timeout
        while time.time() < deadline:
            if self.process.poll() is not None:
                return False
            try:
                status, _, _ = request("GET", "/health/live")
                if status == 200:
                    return True
            except OSError:
                pass
            time.sleep(2)
        return False

    def tail(self, limit=1500):
        with open(self.log_path, encoding="utf-8", errors="replace") as handle:
            return handle.read()[-limit:]

    def stop(self):
        if self.process is None or self.process.poll() is not None:
            return
        os.kill(self.process.pid, signal.CTRL_BREAK_EVENT)
        try:
            self.process.wait(timeout=60)
        except subprocess.TimeoutExpired:
            self.process.terminate()
            self.process.wait(timeout=30)
        if self.log:
            self.log.close()
            self.log = None


def build_bundle():
    if not os.path.isfile(EXE_SOURCE):
        print(f"build the test binary first: {EXE_SOURCE} is missing")
        sys.exit(2)
    if not os.path.isdir(PG_SOURCE):
        print(f"portable PostgreSQL not found at {PG_SOURCE}")
        sys.exit(2)
    if os.path.isdir(BUNDLE):
        shutil.rmtree(BUNDLE)
    os.makedirs(BUNDLE)
    shutil.copy2(EXE_SOURCE, os.path.join(BUNDLE, "teldrive.exe"))
    subprocess.run(
        ["cmd", "/c", "mklink", "/J", os.path.join(BUNDLE, "pgsql"), PG_SOURCE],
        check=True, capture_output=True,
    )
    subprocess.run(
        [sys.executable, os.path.join(WS, "tools", "make_bundle.py"), BUNDLE, "--public"],
        check=True,
    )
    config_path = os.path.join(BUNDLE, "config.toml")
    with open(config_path, encoding="utf-8") as handle:
        config = handle.read()
    config = config.replace('address = "127.0.0.1:8080"', f'address = "127.0.0.1:{HTTP_PORT}"')
    config = config.replace("127.0.0.1:5433", f"127.0.0.1:{PG_PORT}")
    with open(config_path, "w", encoding="utf-8", newline="\r\n") as handle:
        handle.write(config)
    return config_path


def main() -> int:
    config_path = build_bundle()

    shipped = sorted(os.listdir(BUNDLE))
    check("bundle ships no launcher scripts", not any(name.endswith(".bat") for name in shipped),
          f"contents: {shipped}")
    with open(config_path, encoding="utf-8") as handle:
        before = handle.read()
    check("config starts with key placeholders",
          "__SIGNING_KEY__" in before and "__DATA_KEY__" in before)

    server = Server()
    started = server.start()
    check("teldrive.exe alone brought the service up", started, server.tail())

    try:
        if started:
            with open(config_path, encoding="utf-8") as handle:
                after = handle.read()
            check("first-run keys were generated", "__SIGNING_KEY__" not in after and "__DATA_KEY__" not in after)
            check("generated signing-key looks base64",
                  len(after.split('signing-key = "')[1].split('"')[0]) >= 44)
            check("bundled PostgreSQL is listening", port_open(PG_PORT))

            status, _, body = request("GET", "/health/live")
            check("health endpoint answers", status == 200 and json.loads(body).get("status") == "ok", body[:120])

            status, headers, _ = request("GET", "/", {"Accept-Encoding": "br"})
            check("UI index served with brotli", status == 200 and header(headers, "Content-Encoding") == "br",
                  f"status={status} encoding={header(headers, 'Content-Encoding')}")

            status, headers, _ = request("GET", "/", {"Accept-Encoding": "identity"})
            check("identity fallback still works", status == 200 and header(headers, "Content-Encoding") is None)

            # WebDAV is off by default: the tree must say so, and the settings
            # endpoint must require a browser session.
            status, _, body = request("GET", "/webdav/")
            check("WebDAV tree refuses while disabled", status == 403, f"status={status} body={body[:120]}")
            check("refusal names the setting", b"Settings" in body, body[:160])

            status, headers, _ = request("PROPFIND", "/webdav/", {"Depth": "1"})
            check("PROPFIND refused while disabled", status == 403, f"status={status}")

            status, _, body = request("GET", "/api/webdav-config")
            check("settings endpoint requires a session", status == 401, f"status={status} body={body[:120]}")

            # Persist the toggle and restart: this is what "enable it in settings"
            # writes, and it must survive a restart.
            data_dir = os.path.join(BUNDLE, "data")
            with open(os.path.join(data_dir, "webdav.json"), "w", encoding="utf-8") as handle:
                json.dump({"enabled": True}, handle)

        server.stop()
        stopped = False
        for _ in range(30):
            if not port_open(PG_PORT):
                stopped = True
                break
            time.sleep(1)
        check("bundled PostgreSQL stopped on shutdown", stopped)
        check("no process left serving", not port_open(HTTP_PORT))

        if not started:
            return 1

        # Second run: the persisted toggle must now be honoured.
        restarted = server.start()
        check("restart after enabling WebDAV came up", restarted, server.tail())
        if restarted:
            status, headers, body = request("GET", "/webdav/")
            check("WebDAV tree now asks for credentials", status == 401, f"status={status} body={body[:120]}")
            challenge = str(header(headers, "WWW-Authenticate") or "")
            check("Basic challenge is present", challenge.startswith("Basic"), challenge)
            # These are the verbs chi has no routing tree for, so reaching the
            # handler at all (401) rather than a 405 is the real assertion.
            for method in ("PROPFIND", "MKCOL", "MOVE", "LOCK"):
                status, _, body = request(method, "/webdav/", {"Depth": "1"})
                check(f"{method} reaches the DAV handler", status == 401, f"status={status} body={body[:80]}")
            status, _, body = request("GET", "/api/webdav-config", {"Cookie": "teldrive_access=not-a-real-token"})
            check("bad session is rejected", status == 401, f"status={status} body={body[:120]}")
        server.stop()
        for _ in range(30):
            if not port_open(PG_PORT):
                break
            time.sleep(1)
    finally:
        server.stop()

    print()
    failed = [row for row in results if not row[1]]
    print(f"SUMMARY: {len(results) - len(failed)}/{len(results)} checks passed")

    if not KEEP:
        pg_ctl = os.path.join(PG_SOURCE, "bin", "pg_ctl.exe")
        subprocess.run([pg_ctl, "-D", os.path.join(BUNDLE, "data", "pgdata"), "-m", "immediate", "-w", "stop"],
                       capture_output=True)
        shutil.rmtree(BUNDLE, ignore_errors=True)
        print("cleaned up", BUNDLE)
    else:
        print("kept", BUNDLE)

    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
