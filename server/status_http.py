#!/usr/bin/env python3
import json, os, signal, subprocess, sys, time, threading
import urllib.request, urllib.error
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
PREP_STATE    = os.path.join(STATE_DIR, "playlist_prep.json")
JACK_STATE    = os.path.join(STATE_DIR, "jack.json")
JACK_PID_FILE = os.path.join(STATE_DIR, "jackd.pid")

CAPTURE_BIN   = os.environ.get("CAPTURE_BIN", "/usr/local/bin/capture_sdi_dante.sh")
PLAYOUT_BIN   = os.environ.get("PLAYOUT_BIN", "/usr/local/bin/playout_all.sh")
PLAYLIST_PREP_BIN = os.environ.get("PLAYLIST_PREP_BIN", "/usr/local/bin/playlist_prepare.sh")
BASH          = os.environ.get("BASH", "/bin/bash")  # run shell scripts through bash

GST_LOG_FILE = os.environ.get("GST_LOG_FILE", os.path.join(LOG_DIR, "playout_gst.log"))
FOLLOW_GST = str(os.environ.get("STATUS_HTTP_FOLLOW_GST", "1")).lower() not in ("0","false","no","off","")

os.makedirs(STATE_DIR, exist_ok=True)
os.makedirs(LOG_DIR, exist_ok=True)

# Media-streamer integration (optional): base URL of the external generator/runner
MEDIA_STREAMER_URL = os.environ.get("MEDIA_STREAMER_URL", "http://127.0.0.1:8081")

def ts() -> str:
    return time.strftime("%Y-%m-%d %H:%M:%S")

# ---------- small utils ----------
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

def _proc_alive(pid: int) -> bool:
    try:
        if pid <= 0: return False
        os.kill(pid, 0)
        return True
    except OSError:
        return False

def _file_int(path) -> int:
    try:
        return int(open(path).read().strip() or "0")
    except Exception:
        return 0

def _terminate(pid, timeout=3.0) -> bool:
    """TERM then (if needed) KILL a pid, waiting up to timeout."""
    if pid <= 0 or not _proc_alive(pid): return False
    try: os.kill(pid, signal.SIGTERM)
    except Exception: pass
    t0 = time.time()
    while time.time() - t0 < timeout:
        if not _proc_alive(pid): return True
        time.sleep(0.1)
    try: os.kill(pid, signal.SIGKILL)
    except Exception: pass
    time.sleep(0.1)
    return not _proc_alive(pid)

def _kill_pidfile(pidfile):
    pid = _file_int(pidfile)
    ok  = _terminate(pid)
    return {"ok": ok, "pid": pid}

def _auth_ok(headers) -> bool:
    if not TOKEN: return True
    return headers.get("Authorization", "") == f"Bearer {TOKEN}"

def _json(self, code, payload):
    body = json.dumps(payload).encode("utf-8")
    self.send_response(code)
    self.send_header("Content-Type", "application/json")
    self.send_header("Content-Length", str(len(body)))
    self.end_headers()
    self.wfile.write(body)

def _parse_body(self):
    length = int(self.headers.get("Content-Length", "0") or "0")
    raw = self.rfile.read(length) if length else b"{}"
    try: return json.loads(raw.decode("utf-8"))
    except Exception: return {}

def _coerce_state(path):
    """If state says running but pid is dead, flip to stopped."""
    d = _read_json(path, {})
    pid = int(d.get("pid") or 0)
    if d.get("running") and not _proc_alive(pid):
        d["running"] = False
        d["pid"] = 0
        d["ended_at"] = int(time.time())
        _write_json(path, d)
    return d

def _add_time_iso_fields(d: dict) -> dict:
    """Attach *_iso fields for known timestamp keys without altering originals."""
    if not isinstance(d, dict):
        return d
    out = dict(d)
    def _fmt(k):
        v = out.get(k)
        try:
            vi = int(v)
            if vi > 0:
                out[f"{k}_iso"] = time.strftime('%Y-%m-%d %H:%M:%S', time.localtime(vi))
        except Exception:
            pass
    for key in ("started_at", "ended_at", "heartbeat", "updated_at", "prepared_at"):
        _fmt(key)
    return out

def _drop_time_numeric_fields(d: dict) -> dict:
    if not isinstance(d, dict):
        return d
    out = dict(d)
    for key in ("started_at", "ended_at", "heartbeat", "updated_at", "prepared_at"):
        if key in out:
            try:
                int(out[key])
                out.pop(key, None)
            except Exception:
                pass
    return out

def _sanitize_playout_display(d: dict) -> dict:
    """Hide playlist prepare noise; show only current/last real run details."""
    if not isinstance(d, dict):
        return d
    pl = dict(d)  # copy for response
    playlist = str(pl.get("playlist") or "")
    file_ = str(pl.get("file") or "")
    if playlist.startswith("prepare:"):
        pl.pop("playlist", None)
    if file_ == "prepare":
        # don't show the placeholder file name
        pl.pop("file", None)
    return pl

def _playlist_prep_update(**fields):
    """Merge keys into playlist_prep.json with timestamp."""
    d = _read_json(PREP_STATE, {})
    d.update({k: v for k, v in fields.items() if v is not None})
    d["updated_at"] = int(time.time())
    _write_json(PREP_STATE, d)

def _playlist_prep_summary():
    """Return last prepared playlist info, optionally with file counts."""
    info = _read_json(PREP_STATE, {})
    playlist_dir = info.get("playlist_dir") or os.environ.get("PLAYLIST_DIR", "/opt/backup/playlist")
    meta_path = os.path.join(playlist_dir, "playlist_meta.json")
    meta = _read_json(meta_path, {})
    urls = info.get("urls") or []
    prepared_files = 0
    prepared_pngs = 0
    prepared_at = 0
    items = []
    try:
        # Count prepared assets
        files = [f for f in os.listdir(playlist_dir) if f.endswith('.mp4') and f.startswith('url_')]
        pngs  = [f for f in os.listdir(playlist_dir) if f.endswith('.png') and f.startswith('last_frame-')]
        prepared_files = len(files)
        prepared_pngs = len(pngs)

        # Build per-item mapping url -> file/png with mtimes
        for i, url in enumerate(urls, start=1):
            file_path = os.path.join(playlist_dir, f"url_{i}.mp4")
            png_path  = os.path.join(playlist_dir, f"last_frame-{i}.png")
            file_exists = os.path.isfile(file_path)
            png_exists  = os.path.isfile(png_path)
            file_mtime = int(os.path.getmtime(file_path)) if file_exists else 0
            png_mtime  = int(os.path.getmtime(png_path)) if png_exists else 0
            if file_mtime > prepared_at: prepared_at = file_mtime
            if png_mtime  > prepared_at: prepared_at = png_mtime
            m = meta.get(str(i)) or {}
            original_name = m.get("original_name")
            final_url = m.get("final_url")
            duration_hms = m.get("duration_hms")
            items.append({
                "index": i,
                "url": url,
                "file": file_path if file_exists else "",
                "png": png_path if png_exists else "",
                "original_name": original_name or "",
                "final_url": final_url or "",
                "duration_hms": duration_hms,
                "file_exists": bool(file_exists),
                "png_exists": bool(png_exists),
                "file_mtime_iso": (time.strftime('%Y-%m-%d %H:%M:%S', time.localtime(file_mtime)) if file_mtime else None),
            })
    except Exception:
        pass

    info["prepared_files"] = prepared_files
    info["prepared_pngs"] = prepared_pngs
    if prepared_at:
        info["prepared_at_iso"] = time.strftime('%Y-%m-%d %H:%M:%S', time.localtime(prepared_at))
    info["items"] = items
    # Attach *_iso to any saved times (e.g., started_at/updated_at) and drop numeric epochs
    info = _add_time_iso_fields(info)
    info = _add_time_iso_fields(info)  # idempotent; ensures iso keys present before drop
    # Remove numeric timestamp fields so only formatted strings remain
    for k in ("prepared_at", "updated_at", "started_at"):
        if k in info:
            try:
                int(info[k])
                info.pop(k, None)
            except Exception:
                pass
    return info or {"prepared": False}

def _playout_controller_alive() -> int:
    pid = _file_int(PLAY_PID_FILE)
    return pid if _proc_alive(pid) else 0

def _patch_playout_state(**fields):
    """Merge some keys into playout.json without touching running/pid."""
    d = _read_json(PLAY_STATE, {})
    d.update({k: v for k, v in fields.items() if v is not None})
    _write_json(PLAY_STATE, d)

# ---------- jack ----------
def _jack_status():
    info = {"running": False}
    d = _read_json(JACK_STATE, {})
    pid = int(d.get("pid") or 0)
    if pid and _proc_alive(pid):
        d["running"] = True; d["source"] = "jack.json"; return d
    pid = _file_int(JACK_PID_FILE)
    if pid and _proc_alive(pid):
        return {"running": True, "pid": pid, "source": "pidfile"}
    try:
        out = subprocess.check_output(["pgrep", "-x", "jackd"], timeout=2).decode().strip()
        if out:
            pid = int(out.splitlines()[0])
            return {"running": True, "pid": pid, "source": "pgrep"}
    except Exception:
        pass
    for exe in filter(None, [os.environ.get("JACK_LSP"), "/usr/local/bin/jack_lsp", "/opt/homebrew/bin/jack_lsp", "jack_lsp"]):
        try:
            out = subprocess.check_output([exe], stderr=subprocess.STDOUT, timeout=2)
            ports = out.decode("utf-8", "ignore").strip().splitlines()
            return {"running": True, "clients": ports[:100], "bin": exe, "source": "jack_lsp"}
        except Exception:
            continue
    return info

# ---------- gst log follower ----------
def _follow_gst_log():
    path = GST_LOG_FILE
    last_pos = 0
    last_inode = None
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END)
            end = f.tell()
            back = min(end, 128 * 1024)
            f.seek(end - back)
            tail = f.read().decode("utf-8", "ignore").splitlines()[-50:]
            if tail:
                print(f"[{ts()}] [gst-follow] showing last {len(tail)} lines from {path}", flush=True)
                for line in tail:
                    print(f"[{ts()}] [gst] {line}", flush=True)
            last_pos = end
    except Exception:
        pass
    print(f"[{ts()}] [gst-follow] following {path}", flush=True)
    while True:
        try:
            with open(path, "r", encoding="utf-8", errors="ignore") as f:
                st = os.fstat(f.fileno())
                inode = getattr(st, "st_ino", None)
                if last_inode is not None and inode is not None and inode != last_inode:
                    print(f"[{ts()}] [gst-follow] log rotated; restarting from 0", flush=True)
                    last_pos = 0
                last_inode = inode
                size = st.st_size
                if last_pos > size: last_pos = 0
                f.seek(last_pos)
                while True:
                    line = f.readline()
                    if not line: break
                    print(f"[{ts()}] [gst] {line.rstrip()}", flush=True)
                last_pos = f.tell()
        except FileNotFoundError:
            pass
        except Exception as e:
            print(f"[{ts()}] [gst-follow] error: {e}", flush=True)
        time.sleep(0.5)

# ---------- HTTP handler ----------
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
            cap  = _drop_time_numeric_fields(_add_time_iso_fields(_coerce_state(CAP_STATE)))
            pl   = _drop_time_numeric_fields(_add_time_iso_fields(_sanitize_playout_display(_coerce_state(PLAY_STATE))))
            jack = _jack_status()
            prep = _playlist_prep_summary()
            # fetch media-streamer status (optional)
            hls = {"ok": False}
            try:
                with urllib.request.urlopen(f"{MEDIA_STREAMER_URL}/api/pipeline/status", timeout=2) as resp:
                    hls = json.loads(resp.read().decode("utf-8", "ignore"))
            except Exception as e:
                hls = {"ok": False, "error": str(e)}
            return _json(self, 200, {"capture": cap or {"running": False},
                                     "playout": pl or {"running": False},
                                     "jack": jack,
                                     "hls": hls,
                                     "playlist_last_prepared": prep})
        elif path == "/v1/capture":
            return _json(self, 200, _drop_time_numeric_fields(_add_time_iso_fields(_coerce_state(CAP_STATE))) or {"running": False})
        elif path == "/v1/playout":
            return _json(self, 200, _drop_time_numeric_fields(_add_time_iso_fields(_sanitize_playout_display(_coerce_state(PLAY_STATE)))) or {"running": False})
        elif path == "/v1/playlist/prepare/status":
            return _json(self, 200, _playlist_prep_summary())
        elif path == "/v1/jack":
            return _json(self, 200, {"jack": _jack_status()})
        elif path == "/v1/hls/status":
            # proxy media-streamer status if available
            try:
                with urllib.request.urlopen(f"{MEDIA_STREAMER_URL}/api/pipeline/status", timeout=2) as resp:
                    data = json.loads(resp.read().decode("utf-8", "ignore"))
                    return _json(self, 200, {"ok": True, "media_streamer": data})
            except Exception as e:
                return _json(self, 200, {"ok": False, "error": str(e)})
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
            mode = str(body.get("mode") or "").strip().lower()  # e.g. "sdi" to use embedded stereo
            cap = _coerce_state(CAP_STATE)
            if cap.get("running") and _proc_alive(int(cap.get("pid") or 0)):
                self._log("capture: already running, refusing")
                return _json(self, 409, {"error": "capture already running", "state": cap})
            if not os.path.exists(CAPTURE_BIN):
                self._log("capture: binary not found:", CAPTURE_BIN)
                return _json(self, 500, {"error": f"capture binary not found: {CAPTURE_BIN}"})
            cmd = [BASH, CAPTURE_BIN, "-r", label]
            if isinstance(duration, int) and duration > 0:
                cmd += ["-d", str(duration)]
            if mode == "sdi":
                cmd += ["--audio", "sdi"]
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

        # ---- PLAYLIST PREPARE ----
        elif path == "/v1/playlist/prepare":
            # New endpoint for preparing playlists
            playlist = body.get("playlist")  # playlist of web URLs
            label = body.get("label", "playlist-prep")
            
            if not playlist:
                self._log("playlist: bad request, need playlist")
                return _json(self, 400, {"error": "must supply playlist"})
            
            # Parse playlist and create URL array
            if isinstance(playlist, str):
                # Convert comma-separated URLs to array
                url_array = [url.strip() for url in playlist.split(",") if url.strip()]
            elif isinstance(playlist, list):
                url_array = playlist
            else:
                return _json(self, 400, {"error": "playlist must be string or array"})
            
            if not url_array:
                return _json(self, 400, {"error": "playlist is empty"})
            
            # Create command for playlist preparation
            cmd = [BASH, PLAYLIST_PREP_BIN, "--playlist", ",".join(url_array), "--label", str(label)]

            # Persist prep request details immediately
            playlist_dir = os.environ.get("PLAYLIST_DIR", "/opt/backup/playlist")
            _playlist_prep_update(preparing=True,
                                   label=str(label),
                                   urls=url_array,
                                   url_count=len(url_array),
                                   playlist_dir=playlist_dir,
                                   started_at=int(time.time()))
            
            # Spawn the playlist preparation process
            proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

            # Record pid for reference
            _playlist_prep_update(prep_pid=int(proc.pid))
            
            return _json(self, 202, {"ok": True, "cmd": cmd, "message": f"Preparing playlist with {len(url_array)} URLs"})
            
        # ---- PLAYLIST PLAY ----
        elif path == "/v1/playlist/play":
            # New endpoint for playing prepared playlists
            label = body.get("label", "playlist-play")
            loop = bool(body.get("loop") or False)
            overlay_enabled = body.get("overlay_enabled", True)
            overlay = body.get("overlay")
            preroll = body.get("preroll")
            
            # Convert string "false" to boolean False
            if isinstance(overlay_enabled, str):
                overlay_enabled = overlay_enabled.lower() not in ("false", "0", "no", "off")
            
            # Create command for playlist playback
            cmd = [BASH, PLAYOUT_BIN]
            if loop: cmd.append("--loop")
            if overlay_enabled == False:
                cmd.append("--no-overlay")
            elif overlay and overlay_enabled:
                cmd += ["--overlay", str(overlay)]
            cmd += ["--playlist", "play", "--label", str(label)]
            if preroll: cmd += ["--preroll", str(preroll)]
            
            # Spawn the playlist playback process
            proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            
            # Update playout state
            _patch_playout_state(loop=bool(loop),
                                 overlay=str(overlay) if overlay and overlay_enabled else None,
                                 overlay_enabled=bool(overlay_enabled),
                                 url=None,
                                 playlist="play",
                                 preroll=str(preroll) if preroll else None,
                                 label=f"playlist-play-{label}", file=None)
            
            return _json(self, 202, {"ok": True, "cmd": cmd, "message": "Starting playlist playback"})

        # ---- PLAYLIST TEST (short 5s segments, files only) ----
        elif path == "/v1/playlist/test":
            # Use playout_all unified TEST pipeline (only first 10s per file, no preroll/png)
            label = str(body.get("label") or "playlist-test")
            loop = bool(body.get("loop") or False)
            # Default to no overlay in TEST mode unless explicitly enabled
            overlay_enabled = body.get("overlay_enabled", False)
            overlay = body.get("overlay")

            # Ensure no conflicting playout controller/child is active
            ctrl_pid  = _file_int(PLAY_PID_FILE)
            state     = _read_json(PLAY_STATE, {})
            child_pid = int(state.get("pid") or 0)
            if ctrl_pid or (child_pid and _proc_alive(child_pid)):
                killed = {}
                if ctrl_pid:
                    killed["controller"] = {"pid": ctrl_pid, "stopped": _terminate(ctrl_pid)}
                    try: os.remove(PLAY_PID_FILE)
                    except FileNotFoundError: pass
                if child_pid and _proc_alive(child_pid):
                    killed["child"] = {"pid": child_pid, "stopped": _terminate(child_pid)}
                state.update({"running": False, "pid": 0, "ended_at": int(time.time())})
                _write_json(PLAY_STATE, state)
                self._log(f"playlist/test: pre-stopped conflicting playout; kill={killed}")

            # Build command
            cmd = [BASH, PLAYOUT_BIN]
            if loop: cmd.append("--loop")
            if not overlay_enabled:
                cmd.append("--no-overlay")
            elif overlay:
                cmd += ["--overlay", str(overlay)]
            cmd += ["--playlist", "test", "--label", label]

            # Spawn and redirect outputs to server logs like playout/start
            outp = os.path.join(LOG_DIR, "status_http_playout.out")
            errp = os.path.join(LOG_DIR, "status_http_playout.err")
            try:
                with open(outp, "ab") as out, open(errp, "ab") as err:
                    p = subprocess.Popen(cmd, stdout=out, stderr=err, start_new_session=True)
                    self._log("playlist/test: spawned", cmd, "pid=", p.pid)
            except Exception as e:
                return _json(self, 500, {"ok": False, "error": "spawn failed", "detail": str(e), "cmd": cmd})

            # Write requested flags immediately; also clear stale overlay by setting empty string
            _patch_playout_state(loop=bool(loop),
                                 overlay=(str(overlay) if overlay and overlay_enabled else ""),
                                 overlay_enabled=bool(overlay_enabled),
                                 url=None,
                                 playlist="test",
                                 preroll=None,
                                 label=label, file="play-test")

            return _json(self, 202, {"ok": True, "message": "Starting playlist test via playout_all", "cmd": cmd})
        
        # ---- HLS GENERATE (via media-streamer) ----
        elif path == "/v1/playout/hls/generate":
            url = str(body.get("url") or "").strip()
            label = str(body.get("label") or "hls").strip()
            if not url:
                return _json(self, 400, {"error": "missing url"})
            try:
                req = json.dumps({"source": "local", "file": url}).encode("utf-8")
                r = urllib.request.Request(f"{MEDIA_STREAMER_URL}/api/pipeline/generate", data=req, headers={"Content-Type": "application/json"})
                with urllib.request.urlopen(r, timeout=8) as resp:
                    res = resp.read().decode("utf-8", "ignore")
            except Exception as e:
                return _json(self, 502, {"error": "media-streamer generate failed", "detail": str(e)})
            _patch_playout_state(loop=False,
                                 overlay=None,
                                 overlay_enabled=False,
                                 url=url,
                                 playlist=None,
                                 preroll=None,
                                 label=label, file="hls")
            return _json(self, 202, {"ok": True, "message": "HLS pipeline generated", "url": url})

        # ---- HLS START (via media-streamer) ----
        elif path == "/v1/playout/hls/start":
            label = str(body.get("label") or "hls").strip()
            url = str(body.get("url") or "").strip()  # optional, just for state
            try:
                r = urllib.request.Request(f"{MEDIA_STREAMER_URL}/api/pipeline/start", data=b"{}", headers={"Content-Type": "application/json"})
                with urllib.request.urlopen(r, timeout=5) as resp:
                    _ = resp.read()
            except Exception as e:
                return _json(self, 502, {"error": "media-streamer start failed", "detail": str(e)})
            if url:
                _patch_playout_state(loop=False, overlay=None, overlay_enabled=False, url=url, playlist=None, preroll=None, label=label, file="hls")
            return _json(self, 202, {"ok": True, "message": "HLS playout started"})

        # ---- HLS STOP (via media-streamer) ----
        elif path == "/v1/playout/hls/stop":
            try:
                r = urllib.request.Request(f"{MEDIA_STREAMER_URL}/api/pipeline/stop", data=b"{}", headers={"Content-Type": "application/json"})
                with urllib.request.urlopen(r, timeout=5) as resp:
                    _ = resp.read()
            except Exception as e:
                return _json(self, 502, {"error": "media-streamer stop failed", "detail": str(e)})
            # flip local state
            d = _read_json(PLAY_STATE, {})
            d.update({"running": False, "pid": 0, "ended_at": int(time.time())})
            _write_json(PLAY_STATE, d)
            return _json(self, 200, {"ok": True, "message": "HLS playout stop requested"})
            
        # ---- IMAGE ONLY OUTPUT (no sound, endless loop) ----
        elif path == "/v1/image/start":
            # Stop any conflicting playout first
            ctrl_pid = _playout_controller_alive()
            child_pid = _coerce_state(PLAY_STATE).get("pid", 0)
            killed = {}
            
            if ctrl_pid:
                killed["controller"] = {"pid": ctrl_pid, "stopped": _terminate(ctrl_pid)}
                try: os.remove(PLAY_PID_FILE)
                except FileNotFoundError: pass
            if child_pid and _proc_alive(child_pid):
                killed["child"] = {"pid": child_pid, "stopped": _terminate(child_pid)}
            
            if killed:
                self._log(f"image/start: pre-stopped conflicting playout; kill={killed}")

            # Get parameters
            image_path = body.get("image_path", "/Users/shidur/shidur.png")
            label = str(body.get("label") or "image-only")
            loop = bool(body.get("loop") or True)  # Default to true for endless image
            
            # Use playout_all.sh with --image argument
            cmd = [BASH, PLAYOUT_BIN, "--image", str(image_path), "--label", label]
            if loop: cmd.append("--loop")
            
            # Spawn and redirect outputs to server logs
            outp = os.path.join(LOG_DIR, "status_http_playout.out")
            errp = os.path.join(LOG_DIR, "status_http_playout.err")
            try:
                with open(outp, "ab") as out, open(errp, "ab") as err:
                    p = subprocess.Popen(cmd, stdout=out, stderr=err, start_new_session=True)
                    self._log("image/start: spawned", cmd, "pid=", p.pid)
            except Exception as e:
                return _json(self, 500, {"ok": False, "error": "spawn failed", "detail": str(e), "cmd": cmd})

            # Update state immediately
            _patch_playout_state(loop=bool(loop),
                                 overlay=None,
                                 overlay_enabled=False,
                                 url=None,
                                 playlist=None,
                                 preroll=None,
                                 label=label, file=f"image:{image_path}")

            return _json(self, 202, {"ok": True, "message": "Starting image output via playout_all", "cmd": cmd})
            
        # ---- IMAGE STOP ----
        elif path == "/v1/image/stop":
            # Use existing playout stop mechanism
            ctrl_pid  = _file_int(PLAY_PID_FILE)
            state     = _read_json(PLAY_STATE, {})
            child_pid = int(state.get("pid") or 0)

            killed = {}
            if ctrl_pid:
                killed["controller"] = {"pid": ctrl_pid, "stopped": _terminate(ctrl_pid)}
            if child_pid and _proc_alive(child_pid):
                killed["child"] = {"pid": child_pid, "stopped": _terminate(child_pid)}

            try: os.remove(PLAY_PID_FILE)
            except FileNotFoundError: pass

            state.update({"running": False, "pid": 0, "ended_at": int(time.time())})
            _write_json(PLAY_STATE, state)
            
            return _json(self, 200, {"ok": True, "message": "Image output stopped", "killed": killed})
            
        # ---- PLAYOUT START ----
        elif path == "/v1/playout/start":
            label   = body.get("label")
            file_   = body.get("file")
            url     = body.get("url")  # new: web URL support
            playlist = body.get("playlist")  # new: playlist of web URLs
            loop    = bool(body.get("loop") or False)
            overlay = body.get("overlay")  # optional path
            overlay_enabled = body.get("overlay_enabled", True)  # new: control overlay display
            # Convert string "false" to boolean False
            if isinstance(overlay_enabled, str):
                overlay_enabled = overlay_enabled.lower() not in ("false", "0", "no", "off")
            
            # Debug logging
            self._log(f"DEBUG: overlay_enabled raw={body.get('overlay_enabled')} processed={overlay_enabled} type={type(overlay_enabled)}")
            preroll = body.get("preroll")  # new: preroll video path

            # refuse if controller loop is alive
            ctrl_pid = _playout_controller_alive()
            if ctrl_pid:
                self._log(f"playout: already running (controller pid {ctrl_pid}), refusing")
                return _json(self, 409, {"error": "playout already running (controller)", "controller_pid": ctrl_pid})

            # also refuse if current child in state is alive
            pl = _coerce_state(PLAY_STATE)
            if pl.get("running") and _proc_alive(int(pl.get("pid") or 0)):
                self._log("playout: already running (child alive), refusing")
                return _json(self, 409, {"error": "playout already running", "state": pl})

            if not os.path.exists(PLAYOUT_BIN):
                self._log("playout: binary not found:", PLAYOUT_BIN)
                return _json(self, 500, {"error": f"playout binary not found: {PLAYOUT_BIN}"})

            # build command + resolve label/file for state
            cmd = [BASH, PLAYOUT_BIN]
            if loop: cmd.append("--loop")
            # For web URLs, default to no overlay unless explicitly specified
            self._log(f"DEBUG: url={url}, overlay={overlay}, overlay_enabled={overlay_enabled}")
            if url and not overlay:
                self._log("DEBUG: Adding --no-overlay")
                cmd.append("--no-overlay")
            elif overlay and overlay_enabled: 
                self._log("DEBUG: Adding --overlay")
                cmd += ["--overlay", str(overlay)]
            if playlist: cmd += ["--playlist", str(playlist)]
            elif url: cmd += ["--url", str(url)]
            if preroll: cmd += ["--preroll", str(preroll)]
            if label: cmd += ["--label", str(label)]

            resolved_label = None
            resolved_file  = None
            if playlist:
                # Playlist case
                resolved_label = f"playlist-{str(label) if label else 'streams'}"
                resolved_file = str(playlist)
                cmd.append(resolved_file)
            elif url:
                # Web URL case
                resolved_label = f"web-{str(label) if label else 'stream'}"
                resolved_file = str(url)
                cmd.append(resolved_file)
            elif label and not file_:
                resolved_label = f"lesson-{str(label).split('-')[-1]}" if str(label).startswith("lesson-") else str(label)
                # For lesson-* labels, pass the label so the script expands it; also set state file path
                if resolved_label.startswith("lesson-"):
                    cmd.append(resolved_label)
                    resolved_file = f"/opt/backup/latest/{resolved_label}.mp4"
                else:
                    # For non-lesson labels, if a latest symlink/file exists, pass the full path
                    candidate = f"/opt/backup/latest/{resolved_label}.mp4"
                    if os.path.exists(candidate):
                        resolved_file = candidate
                        cmd.append(resolved_file)
                    else:
                        # Fallback: pass the label as-is (expected to be a direct file path)
                        cmd.append(resolved_label)
            elif file_:
                resolved_file = str(file_)
                resolved_label = os.path.basename(os.path.splitext(resolved_file)[0])
                cmd.append(resolved_file)
            else:
                self._log("playout: bad request, need label, file, or url")
                return _json(self, 400, {"error": "must supply label, file, or url"})

            outp = os.path.join(LOG_DIR, "status_http_playout.out")
            errp = os.path.join(LOG_DIR, "status_http_playout.err")
            try:
                with open(outp, "ab") as out, open(errp, "ab") as err:
                    p = subprocess.Popen(cmd, stdout=out, stderr=err, start_new_session=True)
                    self._log("playout: spawned", cmd, "pid=", p.pid)
                # record requested flags/paths immediately (script will soon write pid/running)
                _patch_playout_state(loop=bool(loop),
                                     overlay=str(overlay) if overlay and overlay_enabled else None,
                                     overlay_enabled=bool(overlay_enabled),
                                     url=str(url) if url else None,
                                     playlist=str(playlist) if playlist else None,
                                     preroll=str(preroll) if preroll else None,
                                     label=resolved_label, file=resolved_file)
                return _json(self, 202, {"ok": True, "cmd": cmd})
            except Exception as e:
                self._log("playout: spawn failed:", repr(e))
                return _json(self, 500, {"error": "spawn failed", "detail": str(e), "cmd": cmd})

        # ---- PLAYOUT STOP ----
        elif path == "/v1/playout/stop":
            ctrl_pid  = _file_int(PLAY_PID_FILE)
            state     = _read_json(PLAY_STATE, {})
            child_pid = int(state.get("pid") or 0)

            killed = {}
            if ctrl_pid:
                killed["controller"] = {"pid": ctrl_pid, "stopped": _terminate(ctrl_pid)}
            if child_pid and _proc_alive(child_pid):
                killed["child"] = {"pid": child_pid, "stopped": _terminate(child_pid)}

            try: os.remove(PLAY_PID_FILE)
            except FileNotFoundError: pass

            state.update({"running": False, "pid": 0, "ended_at": int(time.time())})
            _write_json(PLAY_STATE, state)

            self._log(f"playout: stop requested; killed={killed} (ctrl={ctrl_pid}, child={child_pid})")
            return _json(self, 200, {"ok": True, "killed": killed,
                                     "controller_pid": ctrl_pid, "child_pid": child_pid})

        # ---- LESSON CONCAT REPEAT ----
        elif path == "/v1/lesson/concat":
            # Parameters: lesson label or file path, repeats (int)
            label = str(body.get("label") or "lesson-2")
            repeats = int(body.get("repeats") or 3)
            overlay = body.get("overlay")
            overlay_enabled = body.get("overlay_enabled", False)

            # Refuse if controller loop is alive
            ctrl_pid = _playout_controller_alive()
            if ctrl_pid:
                return _json(self, 409, {"error": "playout already running (controller)", "controller_pid": ctrl_pid})

            # Also refuse if current child in state is alive
            pl = _coerce_state(PLAY_STATE)
            if pl.get("running") and _proc_alive(int(pl.get("pid") or 0)):
                return _json(self, 409, {"error": "playout already running", "state": pl})

            if not os.path.exists(PLAYOUT_BIN):
                return _json(self, 500, {"error": f"playout binary not found: {PLAYOUT_BIN}"})

            # Resolve file: lesson-* in /opt/backup/latest else treat as path
            if label.startswith("lesson-"):
                file_ = f"/opt/backup/latest/{label}.mp4"
            else:
                file_ = body.get("file") or label

            cmd = [BASH, PLAYOUT_BIN, "--repeat-concat", str(repeats), "--label", label]
            if overlay and overlay_enabled:
                cmd += ["--overlay", str(overlay)]
            else:
                cmd.append("--no-overlay")
            cmd.append(file_)

            outp = os.path.join(LOG_DIR, "status_http_playout.out")
            errp = os.path.join(LOG_DIR, "status_http_playout.err")
            try:
                with open(outp, "ab") as out, open(errp, "ab") as err:
                    p = subprocess.Popen(cmd, stdout=out, stderr=err, start_new_session=True)
                    self._log("lesson/concat: spawned", cmd, "pid=", p.pid)
                _patch_playout_state(loop=False,
                                     overlay=str(overlay) if overlay and overlay_enabled else None,
                                     overlay_enabled=bool(overlay_enabled),
                                     url=None,
                                     playlist=None,
                                     preroll=None,
                                     label=label, file=file_)
                return _json(self, 202, {"ok": True, "cmd": cmd})
            except Exception as e:
                return _json(self, 500, {"error": "spawn failed", "detail": str(e), "cmd": cmd})

        else:
            return _json(self, 404, {"error": "not found"})

    def log_message(self, fmt, *args):
        # silence BaseHTTPRequestHandler default logs (we log ourselves)
        pass

class ReuseHTTPServer(HTTPServer):
    allow_reuse_address = True

def main():
    try: who = os.getlogin()
    except Exception: who = os.environ.get("USER", "unknown")
    print(f"[{ts()}] status_http: foreground mode on port {PORT}", flush=True)
    print(f"[{ts()}] user={who}  STATE_DIR={STATE_DIR}  LOG_DIR={LOG_DIR}", flush=True)
    print(f"[{ts()}] capture_bin={CAPTURE_BIN}  playout_bin={PLAYOUT_BIN}", flush=True)
    if TOKEN:
        print(f"[{ts()}] auth: Bearer token enabled", flush=True)
    else:
        print(f"[{ts()}] auth: DISABLED (accepts local calls without token)", flush=True)

    if FOLLOW_GST:
        threading.Thread(target=_follow_gst_log, daemon=True).start()
    else:
        print(f"[{ts()}] [gst-follow] disabled (set STATUS_HTTP_FOLLOW_GST=1 to enable)", flush=True)

    srv = ReuseHTTPServer(("", PORT), Handler)
    try:
        srv.serve_forever(poll_interval=0.5)
    except KeyboardInterrupt:
        print(f"\n[{ts()}] status_http: shutting down (Ctrl+C)", flush=True)
    finally:
        srv.server_close()

if __name__ == "__main__":
    main()