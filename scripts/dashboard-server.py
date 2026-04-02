#!/usr/bin/env python3
"""
Dashboard server for K8s Log Ingestor
Serves the dashboard UI and proxies API requests to the ingestor/ClickHouse
"""
import http.server
import socketserver
import json
import os
import urllib.request
import urllib.error
from urllib.parse import urlparse, parse_qs

PORT = 8082

INGESTOR_API = os.environ.get('INGESTOR_API', 'http://localhost:8080')
CLICKHOUSE_API = os.environ.get('CLICKHOUSE_API', 'http://localhost:8123')

class DashboardHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        web_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'web')
        super().__init__(*args, directory=web_dir, **kwargs)
    
    def do_GET(self):
        if self.path == '/api/health':
            self.proxy_request(f"{INGESTOR_API}/health")
        elif self.path.startswith('/api/ingestor'):
            self.proxy_request(f"{INGESTOR_API}{self.path[4:]}")
        elif self.path.startswith('/get-logs'):
            self.proxy_request(f"{INGESTOR_API}/get-logs{self.path[10:]}")
        elif self.path.startswith('/api/logs'):
            self.proxy_request(f"{INGESTOR_API}/get-logs")
        elif self.path.startswith('/api/get-logs'):
            self.proxy_request(f"{INGESTOR_API}/get-logs{self.path[12:]}")
        else:
            super().do_GET()
    
    def proxy_request(self, url, method='GET', data=None):
        try:
            req = urllib.request.Request(url, data=data, method=method)
            with urllib.request.urlopen(req, timeout=5) as response:
                self.send_response(response.status)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(response.read())
        except urllib.error.URLError as e:
            self.send_error(502, f"Bad Gateway: {e}")
        except Exception as e:
            self.send_error(500, f"Server Error: {e}")
    
    def handle_stats(self):
        stats = {
            'total_logs': self.query_clickhouse('SELECT count() as c FROM default.logs'),
            'today_logs': self.query_clickhouse("SELECT count() as c FROM default.logs WHERE timestamp >= today()"),
            'last_hour': self.query_clickhouse("SELECT count() as c FROM default.logs WHERE timestamp >= now() - INTERVAL 1 HOUR"),
            'errors': self.query_clickhouse("SELECT count() as c FROM default.logs WHERE level = 'error' AND timestamp >= now() - INTERVAL 1 HOUR"),
        }
        self.send_json({'success': True, 'data': stats})
    
    def handle_logs(self):
        sql = "SELECT timestamp, namespace, pod, container, level, message FROM default.logs ORDER BY timestamp DESC LIMIT 20"
        logs = self.query_clickhouse(sql, formatted=False)
        self.send_json({'success': True, 'data': {'logs': logs}})
    
    def handle_query(self):
        parsed = urlparse(self.path)
        params = parse_qs(parsed.query)
        sql = params.get('sql', [''])[0]
        
        if not sql:
            self.send_error(400, 'Missing sql parameter')
            return
        
        result = self.query_clickhouse(sql, formatted=True)
        self.send_json(result)
    
    def handle_namespaces(self):
        sql = """
            SELECT namespace, count() as c 
            FROM default.logs 
            WHERE timestamp >= now() - INTERVAL 1 HOUR 
            GROUP BY namespace 
            ORDER BY c DESC 
            LIMIT 10
        """
        data = self.query_clickhouse(sql)
        self.send_json({'success': True, 'data': data})
    
    def query_clickhouse(self, sql, formatted=True):
        try:
            params = f"query={urllib.parse.quote(sql)}"
            if formatted:
                params += "&default_format=JSON"
            
            url = f"{CLICKHOUSE_API}/?{params}"
            req = urllib.request.Request(url)
            
            with urllib.request.urlopen(req, timeout=10) as response:
                data = json.loads(response.read().decode())
                
                if formatted and 'data' in data:
                    return data['data']
                return data
        except Exception as e:
            print(f"ClickHouse query error: {e}")
            return []
    
    def send_json(self, data):
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

if __name__ == '__main__':
    print(f"Starting dashboard server on http://localhost:{PORT}")
    print(f"INGESTOR_API: {INGESTOR_API}")
    print(f"CLICKHOUSE_API: {CLICKHOUSE_API}")
    print(f"Dashboard: http://localhost:{PORT}/dashboard/")
    
    with socketserver.TCPServer(("", PORT), DashboardHandler) as httpd:
        httpd.serve_forever()
