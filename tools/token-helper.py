# What the spike on an Android device gets from this computer over USB (adb reverse tcp:7899 tcp:7899):
#   /token                     Gerdr's token (a real App pairs: docs/dev/ANDROID.md)
#   /plugins.json              the installed plugins, plugins/plugins.json: [{ id, src }], each bundle src with
#                              "#v=<its build's mtime>" (the UI follows it: a new build runs at once)
#   /plugins/<id>/<file>       a plugin's built bundle, plugins/<id>/out/ (`nsh build App.tsx --release --out out`)
# Listens on 127.0.0.1 only: what reaches it is this Mac's own processes (which can read ~/.gerdr/token anyway)
# and a device that `adb reverse` forwards. In a real build Gerdr's backend serves the plugins.
import http.server, json, os, re, subprocess, threading, time

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "plugins")
TOKEN = open(os.path.expanduser("~/.gerdr/token")).read().strip()


class H(http.server.BaseHTTPRequestHandler):
    def send(self, body: bytes, ctype: str):
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/token":
            return self.send(TOKEN.encode(), "text/plain")
        if self.path == "/plugins.json":
            out = []
            for p in json.load(open(os.path.join(ROOT, "plugins.json"))):
                b = os.path.join(ROOT, p["id"], "out", "bundle.js")
                if p["src"].startswith("bundle:") and os.path.isfile(b):
                    p = {**p, "src": p["src"] + "#v=%d" % int(os.path.getmtime(b) * 1000)}
                out.append(p)
            return self.send(json.dumps(out).encode(), "application/json")
        m = re.fullmatch(r"/plugins/([a-z0-9-]+)/([A-Za-z0-9._-]+)", self.path)
        if m:
            f = os.path.join(ROOT, m.group(1), "out", m.group(2))
            if os.path.isfile(f):
                return self.send(open(f, "rb").read(), "application/octet-stream")
        self.send_error(404)

    def log_message(self, *a):
        pass


NSH = os.path.expanduser("~/.local/bin/nsh")


def rebuild():
    """Builds a plugin again when one of its sources is newer than its bundle: saving a file is all an update takes."""
    while True:
        for pid in os.listdir(ROOT):
            d = os.path.join(ROOT, pid)
            if not os.path.isfile(os.path.join(d, "App.tsx")):
                continue
            src = max(os.path.getmtime(os.path.join(d, f)) for f in os.listdir(d) if f.endswith((".ts", ".tsx", ".json")))
            b = os.path.join(d, "out", "bundle.js")
            if not os.path.isfile(b) or src > os.path.getmtime(b):
                r = subprocess.run([NSH, "build", "App.tsx", "--release", "--out", "out"], cwd=d, capture_output=True, text=True)
                print(f"rebuilt {pid}: " + (r.stdout + r.stderr).strip().splitlines()[-1], flush=True)
        time.sleep(1)


threading.Thread(target=rebuild, daemon=True).start()
http.server.ThreadingHTTPServer(("127.0.0.1", 7899), H).serve_forever()
