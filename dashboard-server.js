const http = require('http');
const fs = require('fs');
const path = require('path');

const PORT = 8082;
const INGESTOR_API = 'http://localhost:8081';
const WEB_DIR = path.join(__dirname, 'web', 'dashboard');

const mimeTypes = {
    '.html': 'text/html',
    '.js': 'text/javascript',
    '.css': 'text/css',
    '.json': 'application/json'
};

function proxyRequest(targetUrl, res) {
    const url = new URL(targetUrl);
    const options = {
        hostname: url.hostname,
        port: url.port,
        path: url.pathname + url.search,
        method: 'GET'
    };

    const proxyReq = http.request(options, (proxyRes) => {
        let data = '';
        proxyRes.on('data', chunk => data += chunk);
        proxyRes.on('end', () => {
            res.writeHead(200, {
                'Content-Type': 'application/json',
                'Access-Control-Allow-Origin': '*'
            });
            res.end(data);
        });
    });

    proxyReq.on('error', (e) => {
        res.writeHead(500, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: e.message }));
    });

    proxyReq.end();
}

const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://localhost:' + PORT);
    
    if (url.pathname === '/api/health') {
        proxyRequest(INGESTOR_API + '/health', res);
        return;
    }
    
    if (url.pathname === '/api/logs') {
        proxyRequest(INGESTOR_API + '/get-logs?limit=20', res);
        return;
    }
    
    var filePath;
    if (url.pathname === '/' || url.pathname === '/dashboard' || url.pathname === '/dashboard/') {
        filePath = path.join(WEB_DIR, 'index.html');
    } else if (url.pathname.startsWith('/dashboard/')) {
        filePath = path.join(WEB_DIR, url.pathname.replace('/dashboard/', ''));
    } else {
        filePath = path.join(WEB_DIR, url.pathname);
    }
    
    var ext = path.extname(filePath);
    var contentType = mimeTypes[ext] || 'text/html';
    
    fs.readFile(filePath, function(err, data) {
        if (err) {
            res.writeHead(404);
            res.end('Not found');
            return;
        }
        res.writeHead(200, { 'Content-Type': contentType });
        res.end(data);
    });
});

server.listen(PORT, function() {
    console.log('Dashboard: http://localhost:' + PORT + '/dashboard/');
});
