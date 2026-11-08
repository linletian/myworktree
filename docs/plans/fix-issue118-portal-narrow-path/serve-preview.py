#!/usr/bin/env python3
"""issue #118 预览静态服务器（仅预览用，不进生产）。

- 绑定 0.0.0.0:<port>，经 Tailscale 从手机访问
- 只服务指定目录（SimpleHTTPRequestHandler 自带目录穿越防护），
  根路径映射到 preview-narrow-path.html
- 自动到期关闭（默认 60 分钟），PID 写入 /tmp/mw-preview-118.pid
  手工关闭：kill "$(cat /tmp/mw-preview-118.pid)"
"""
import argparse
import http.server
import os
import socketserver
import sys
import threading

PID_FILE = "/tmp/mw-preview-118.pid"
INDEX = "preview-narrow-path.html"


class Handler(http.server.SimpleHTTPRequestHandler):
    def _rewrite(self):
        base = self.path.split("?", 1)[0].split("#", 1)[0]
        if base in ("", "/"):
            self.path = "/" + INDEX

    def do_GET(self):
        self._rewrite()
        return super().do_GET()

    def do_HEAD(self):
        self._rewrite()
        return super().do_HEAD()

    def end_headers(self):
        self.send_header("Cache-Control", "no-store")
        super().end_headers()


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18800)
    ap.add_argument("--ttl", type=int, default=3600, help="秒后自动关闭")
    ap.add_argument("--dir", default=os.path.dirname(os.path.abspath(__file__)))
    args = ap.parse_args()

    directory = os.path.abspath(args.dir)
    with open(PID_FILE, "w") as fh:
        fh.write(str(os.getpid()))

    def handler(*a, **kw):
        return Handler(*a, directory=directory, **kw)

    httpd = Server(("0.0.0.0", args.port), handler)
    threading.Timer(args.ttl, httpd.shutdown).start()
    print("serving %s on 0.0.0.0:%d (ttl %ds, pid %d)"
          % (directory, args.port, args.ttl, os.getpid()), flush=True)
    try:
        httpd.serve_forever()
    finally:
        httpd.server_close()
        print("shutdown", flush=True)


if __name__ == "__main__":
    sys.exit(main())
