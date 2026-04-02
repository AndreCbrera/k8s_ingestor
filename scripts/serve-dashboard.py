#!/usr/bin/env python3
"""Simple HTTP server for the dashboard"""

import http.server
import socketserver
import os
import json

PORT = 3000
DIRECTORY = "web/dashboard"

class DashboardHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=DIRECTORY, **kwargs)
    
    def do_GET(self):
        if self.path == "/api/stats":
            self.send_response(200)
            self.send_header("Content-type", "application/json")
            self.end_headers()
            stats = {
                "success": True,
                "data": {
                    "total_logs": 1234567,
                    "logs_per_second": 150,
                    "error_rate": 0.02,
                    "avg_latency_ms": 15,
                    "queue_depth": 45
                }
            }
            self.wfile.write(json.dumps(stats).encode())
        elif self.path == "/api/logs":
            self.send_response(200)
            self.send_header("Content-type", "application/json")
            self.end_headers()
            logs = {
                "success": True,
                "data": {
                    "logs": [
                        {"timestamp": "2024-01-01T12:00:00Z", "level": "error", "message": "Connection timeout", "namespace": "production"},
                        {"timestamp": "2024-01-01T12:00:01Z", "level": "warn", "message": "High memory usage", "namespace": "staging"},
                        {"timestamp": "2024-01-01T12:00:02Z", "level": "info", "message": "Request completed", "namespace": "production"},
                    ]
                }
            }
            self.wfile.write(json.dumps(logs).encode())
        else:
            super().do_GET()

    def log_message(self, format, *args):
        pass

if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    with socketserver.TCPServer(("", PORT), DashboardHandler) as httpd:
        print(f"Dashboard running at http://localhost:{PORT}")
        print(f"Open: http://localhost:{PORT}/index.html")
        httpd.serve_forever()
