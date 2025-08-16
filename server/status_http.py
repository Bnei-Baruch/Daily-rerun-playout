#!/usr/bin/env python3
import json, os, signal, subprocess, sys, time
from http.server import BaseHTTPRequestHandler, HTTPServer

# ---------- config ----------
PORT = int(os.environ.get("STATUS_HTTP_PORT", "8787"))
TOKEN = os.environ.get("STATUS_HTTP_TOKEN", "")  # optional Bearer token

STATE_DIR     = os.environ.get("STATE_DIR", "/opt/backup/state")
LOG_DIR       = os.environ.get("LOG_DIR",   "/opt/backup/logs")
CAP_STATE     = os.path.join(STATE_DIR, "capture.json")
CAP_PID_FILE  = os.path.join(STATE_DIR, "capture.pid")
PLAY_STATE    = os.path.join(STATE_DIR, "playout.json")
PLAY_PID_FILE = os.path.join(STATE_DIR, "playout.pid")
JACK_STATE    = os.path.join(STATE_DIR, "jack.json")
JACK_PID_FILE = os.path.join(STATE_DIR, "jackd.pid")

CAPTURE_BIN   = os.environ.get("CAPTURE_BIN", "/usr/local/bin/capture_sdi_dante.sh")
PLAYOUT_BIN   = os.environ.get("PLAYOUT_BIN", "/usr/local/bin/playout_all.sh")
BASH          = os.environ.get("BASH", "/bin/bash")  # run shell scripts through bash

os.makedirs(STATE_DIR, exist_ok=True)
os.makedirs(LOG_DIR, exist_ok=True)

def ts():
    return time.strftime("%Y-%m-%d %H:%M:%S")

def _read_json(path, default):
    try:
        with open(path, "r") as f:
            return json.load(f)
    except Exception:
        return default

def _write_json(path, data):
    tmp = f"{path}.tmp.{os.getpid()}"
    with open(tmp, "w") as f:
        json.dump(data, f)
    os.replace(tmp, path)

def _proc_alive(pid):
    try:
        if pid <= 0:
            return False
        os.kill(pid, 0)
        return True
    except OSError:
        return False

def _kill_pidfile(pidfile):
    try:
        with open(pidfile) as f:
            pid = int(f.read().strip() or "0")
    except Exception:
        pid = 0
    if _proc_alive(pid):
        os.kill(pid, signal.SIGTERM)
        return {"ok": True, "pid": pid}
    return {"ok": False, "pid": pid}

def _auth_ok(headers):
    if not TOKEN:
        return True
    auth = headers.get("Authorization", "")
    return auth == f"Bearer {TOKEN}"

def _json(self, code, payload):
    body = json.dumps(payload).encode("utf-8")
    self.send_response(code)
    self.send_header("Content-Type", "application/json")
    self.send_header("Content-Length", str(len(body)))
    self.end_headers()
    self.wfile.write(body)

def _parse_body(self):
    length = int(self.headers.get("Content-Length", "0") or "0")
    if length == 0:
        return {}
    raw = self.rfile.read(length)
    try:
        return json.loads(raw.decode("utf-8"))
    except Exception:
        return {}

def _coerce_state(path):
    d = _read_json(path, {})
    pid = int(d.get("pid") or 0)
    if d.get("running") and not _proc_alive(pid):
        d["running"] = False
        d["pid"] = 0
        d["ended_at"] = int(time.time())
        _write_json(path, d)
    return d

def _jack_status():
    """Report JACK status. Prefer jack_start.sh state, then pidfile/process, then jack_lsp if present."""
    info = {"running": False}

    # Prefer jack.json written by jack_start.sh
    d = _read_json(JACK_STATE, {})
    pid = int(d.get("pid") or 0)
    if pid and _proc_alive(pid):
        d["running"] = True
        d["source"] = "jack.json"
        return d

    # pidfile written by jack_start.sh
    try:
        with open(JACK_PID_FILE) as f:
            pid = int(f.read().strip() or "0")
    except Exception:
        pid = 0
    if pid and _proc_alive(pid):
        return {"running": True, "pid": pid, "source": "pidfile"}

    # process list (pgrep jackd)
    try:
        out = subprocess.check_output(["pgrep", "-x", "jackd"], timeout=2).decode().strip()
        if out:
            pid = int(out.splitlines()[0])
            return {"running": True, "pid": pid, "source": "pgrep"}
    except Exception:
        pass

    # Optional: try jack_lsp if available (some installs don't ship it)
    for exe in filter(None, [os.environ.get("JACK_LSP"), "/usr/local/bin/jack_lsp", "/opt/homebrew/bin/jack_lsp", "jack_lsp"]):
        try:
            out = subprocess.check_output([exe], stderr=subprocess.STDOUT, timeout=2)
            ports = out.decode("utf-8", "ignore").strip().splitlines()
            return {"running": True, "clients": ports[:100], "bin": exe, "source": "jack_lsp"}
        except FileNotFoundError:
            continue
        except Exception:
            continue

    return info

class Handler(BaseHTTPRequestHandler):
    server_version = "status_http/fg-logger"

    def _log_req(self, extra=""):
        print(f"[{ts()}] {self.client_address[0]} {self.command} {self.path} {extra}".strip(), flush=True)

    def _log(self, *args):
        print(f"[{ts()}]", *args, flush=True)

    def do_GET(self):
        self._log_req()
        path = self.path.split("?", 1)[0]

        if path == "/healthz":
            return _json(self, 200, {"ok": True, "ts": int(time.time())})

        elif path == "/v1/state":
            cap = _coerce_state(CAP_STATE)
            pl  = _coerce_state(PLAY_STATE)
            jack = _jack_status()
            return _json(self, 200, {
                "capture": cap or {"running": False},
                "playout": pl or {"running": False},
                "jack": jack,
            })

        elif path == "/v1/capture":
            d = _coerce_state(CAP_STATE)
            return _json(self, 200, d or {"running": False})

        elif path == "/v1/playout":
            d = _coerce_state(PLAY_STATE)
            return _json(self, 200, d or {"running": False})

        elif path == "/v1/jack":
            return _json(self, 200, {"jack": _jack_status()})

        else:
            return _json(self, 404, {"error": "not found"})

    def do_POST(self):
        if not _auth_ok(self.headers):
            self._log_req("(unauthorized)")
            return _json(self, 401, {"error": "unauthorized"})

        body = _parse_body(self)
        self._log_req(f"body={body}")
        path = self.path.split("?", 1)[0]

        # ---- CAPTURE START ----
        if path == "/v1/capture/start":
            label = str(body.get("label") or "capture")
            duration = body.get("duration")

            cap = _read_json(CAP_STATE, {})
            if cap.get("running") and _proc_alive(int(cap.get("pid") or 0)):
                self._log("capture: already running, refusing")
                return _json(self, 409, {"error": "capture already running", "state": cap})

            if not os.path.exists(CAPTURE_BIN):
                self._log("capture: binary not found:", CAPTURE_BIN)
                return _json(self, 500, {"error": f"capture binary not found: {CAPTURE_BIN}"})

            cmd = [BASH, CAPTURE_BIN, "-r", label]
            if isinstance(duration, int) and duration > 0:
                cmd += ["-d", str(duration)]

            outp = os.path.join(LOG_DIR, "status_http_capture.out")
            errp = os.path.join(LOG_DIR, "status_http_capture.err")
            try:
                with open(outp, "ab") as out, open(errp, "ab") as err:
                    p = subprocess.Popen(cmd, stdout=out, stderr=err, start_new_session=True)
                    self._log("capture: spawned", cmd, "pid=", p.pid)
                return _json(self, 202, {"ok": True, "cmd": cmd})
            except Exception as e:
                self._log("capture: spawn failed:", repr(e))
                return _json(self, 500, {"error": "spawn failed", "detail": str(e), "cmd": cmd})

        # ---- CAPTURE STOP ----
        elif path == "/v1/capture/stop":
            res = _kill_pidfile(CAP_PID_FILE)
            d = _read_json(CAP_STATE, {})
            d.update({"running": False, "pid": 0, "ended_at": int(time.time())})
            _write_json(CAP_STATE, d)
            self._log(f"capture: stop requested; kill={res}")
            return _json(self, 200, {"ok": True, "killed": res})

        # ---- PLAYOUT START ----
        elif path == "/v1/playout/start":
            label = body.get("label")
            file_ = body.get("file")

            pl = _read_json(PLAY_STATE, {})
            if pl.get("running") and _proc_alive(int(pl.get("pid") or 0)):
                self._log("playout: already running, refusing")
                return _json(self, 409, {"error": "playout already running", "state": pl})

            if not os.path.exists(PLAYOUT_BIN):
                self._log("playout: binary not found:", PLAYOUT_BIN)
                return _json(self, 500, {"error": f"playout binary not found: {PLAYOUT_BIN}"})

            if label and not file_:
                arg = f"lesson-{str(label).split('-')[-1]}" if str(label).startswith("lesson-") else str(label)
                cmd = [BASH, PLAYOUT_BIN, arg]
            elif file_:
                cmd = [BASH, PLAYOUT_BIN, str(file_)]
            else:
                self._log("playout: bad request, need label or file")
                return _json(self, 400, {"error": "must supply label or file"})

            outp = os.path.join(LOG_DIR, "status_http_playout.out")
            errp = os.path.join(LOG_DIR, "status_http_playout.err")
            try:
                with open(outp, "ab") as out, open(errp, "ab") as err:
                    p = subprocess.Popen(cmd, stdout=out, stderr=err, start_new_session=True)
                    self._log("playout: spawned", cmd, "pid=", p.pid)
                return _json(self, 202, {"ok": True, "cmd": cmd})
            except Exception as e:
                self._log("playout: spawn failed:", repr(e))
                return _json(self, 500, {"error": "spawn failed", "detail": str(e), "cmd": cmd})

        # ---- PLAYOUT STOP ----
        elif path == "/v1/playout/stop":
            res = _kill_pidfile(PLAY_PID_FILE)
            d = _read_json(PLAY_STATE, {})
            d.update({"running": False, "pid": 0, "ended_at": int(time.time())})
            _write_json(PLAY_STATE, d)
            self._log(f"playout: stop requested; kill={res}")
            return _json(self, 200, {"ok": True, "killed": res})

        else:
            return _json(self, 404, {"error": "not found"})

    def log_message(self, fmt, *args):
        # suppress BaseHTTPRequestHandler default logging
        pass

class ReuseHTTPServer(HTTPServer):
    allow_reuse_address = True

def main():
    try:
        who = os.getlogin()
    except Exception:
        who = os.environ.get("USER", "unknown")
    print(f"[{ts()}] status_http: foreground mode on port {PORT}", flush=True)
    print(f"[{ts()}] user={who}  STATE_DIR={STATE_DIR}  LOG_DIR={LOG_DIR}", flush=True)
    print(f"[{ts()}] capture_bin={CAPTURE_BIN}  playout_bin={PLAYOUT_BIN}", flush=True)
    if TOKEN:
        print(f"[{ts()}] auth: Bearer token enabled", flush=True)
    else:
        print(f"[{ts()}] auth: DISABLED (accepts local calls without token)", flush=True)

    srv = ReuseHTTPServer(("", PORT), Handler)
    try:
        srv.serve_forever(poll_interval=0.5)
    except KeyboardInterrupt:
        print(f"\n[{ts()}] status_http: shutting down (Ctrl+C)", flush=True)
    finally:
        srv.server_close()

if __name__ == "__main__":
    main()
