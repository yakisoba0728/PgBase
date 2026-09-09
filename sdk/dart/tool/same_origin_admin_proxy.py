#!/usr/bin/env python3
"""N7-only loopback proxy for browser admin cookie/CSRF acceptance."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.request import Request, urlopen

API = "http://127.0.0.1:58091"
PAGE = Path(__file__).with_name("same_origin_admin_harness.html")

class Handler(BaseHTTPRequestHandler):
    def do_GET(self): self._serve()
    def do_POST(self): self._serve()
    def do_PUT(self): self._serve()
    def _serve(self):
        if self.path == "/":
            body = PAGE.read_bytes()
            self.send_response(200); self.send_header("Content-Type", "text/html"); self.end_headers(); self.wfile.write(body); return
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        headers = {k:v for k,v in self.headers.items() if k.lower() not in {"host", "origin", "content-length"}}
        headers["Origin"] = API
        request = Request(API + self.path, body or None, headers=headers, method=self.command)
        try:
            response = urlopen(request)
        except Exception as error:
            response = error
        self.send_response(response.code)
        for key, value in response.headers.items():
            if key.lower() in {"set-cookie", "content-type"}: self.send_header(key, value)
        self.end_headers(); self.wfile.write(response.read())

ThreadingHTTPServer(("127.0.0.1", 58093), Handler).serve_forever()
