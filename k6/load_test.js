import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const errorRate = new Rate('errors');
const latency = new Trend('latency');

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

const testLog = {
  log: '2026-04-02 00:00:00 INFO: K6 load test message',
  time: new Date().toISOString(),
  kubernetes: {
    namespace_name: 'default',
    pod_name: 'test-pod',
    container_name: 'test-container',
    labels: { app: 'k6-test' },
    host: 'kind-control-plane',
    pod_ip: '10.244.0.1'
  }
};

export const options = {
  stages: [
    { duration: '30s', target: 10 },
    { duration: '1m', target: 50 },
    { duration: '2m', target: 100 },
    { duration: '1m', target: 50 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    errors: ['rate<0.1'],
  },
};

export default function () {
  group('Health Check', () => {
    const res = http.get(`${BASE_URL}/health`);
    check(res, {
      'health check status is 200': (r) => r.status === 200,
      'health check returns healthy': (r) => r.json('status') === 'healthy',
    });
  });

  group('Log Ingestion', () => {
    const payload = JSON.stringify([testLog]);
    
    const res = http.post(`${BASE_URL}/logs`, payload, {
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': __ENV.API_KEY || '',
      },
      tags: { name: 'log_ingestion' },
    });

    latency.add(res.timings.duration);
    
    const success = check(res, {
      'log ingestion status is 200': (r) => r.status === 200,
      'response has count': (r) => r.json('count') !== undefined,
    });
    
    errorRate.add(!success);
  });

  group('Batch Ingestion', () => {
    const logs = [];
    for (let i = 0; i < 100; i++) {
      logs.push({
        ...testLog,
        log: `2026-04-02 00:00:00 INFO: Batch test message ${i}`,
        time: new Date().toISOString(),
      });
    }
    
    const payload = JSON.stringify(logs);
    
    const res = http.post(`${BASE_URL}/logs`, payload, {
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': __ENV.API_KEY || '',
      },
      tags: { name: 'batch_ingestion' },
    });

    latency.add(res.timings.duration);
    
    const success = check(res, {
      'batch ingestion status is 200': (r) => r.status === 200,
      'all logs processed': (r) => r.json('count') === 100,
    });
    
    errorRate.add(!success);
  });

  group('Rate Limiting', () => {
    const payload = JSON.stringify([testLog]);
    
    const res = http.post(`${BASE_URL}/logs`, payload, {
      headers: {
        'Content-Type': 'application/json',
      },
    });

    if (res.status === 429) {
      errorRate.add(1);
    }
  });

  sleep(0.1);
}

export function handleSummary(data) {
  return {
    'stdout': textSummary(data, { indent: ' ', enableColors: true }),
    'summary.json': JSON.stringify(data),
  };
}

function textSummary(data, options) {
  const indent = options.indent || '';
  let output = '\n' + indent + '=== Load Test Summary ===\n\n';
  
  output += indent + 'HTTP Metrics:\n';
  output += indent + `  Requests: ${data.metrics.http_reqs.values.count}\n`;
  output += indent + `  Duration Avg: ${data.metrics.http_req_duration.values.avg.toFixed(2)}ms\n`;
  output += indent + `  Duration P95: ${data.metrics.http_req_duration.values['p(95)'].toFixed(2)}ms\n`;
  output += indent + `  Duration P99: ${data.metrics.http_req_duration.values['p(99)'].toFixed(2)}ms\n`;
  
  output += '\n' + indent + 'Custom Metrics:\n';
  output += indent + `  Latency Avg: ${data.metrics.latency.values.avg.toFixed(2)}ms\n`;
  output += indent + `  Latency P95: ${data.metrics.latency.values['p(95)'].toFixed(2)}ms\n`;
  output += indent + `  Error Rate: ${(data.metrics.errors.values.rate * 100).toFixed(2)}%\n`;
  
  return output;
}
