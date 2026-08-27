/**
 * A CORS shim for the screenshot harness.
 *
 * The app runs on Android, where there is no origin and no preflight. A
 * browser enforces both, so rendering the real screens against the real API
 * needs the responses to carry Access-Control-Allow-Origin — and adding that
 * to the server would be changing production code to suit a test.
 *
 * So it lives here instead: forward to the API, add the header, and let the
 * harness point at this.
 */
import { createServer } from 'node:http';

const TARGET = process.env.TARGET ?? 'http://127.0.0.1:8099';
const PORT = Number(process.env.PORT ?? 8099 + 1);

createServer(async (request, response) => {
  const headers = {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET,POST,DELETE,OPTIONS',
    'Access-Control-Allow-Headers': 'Content-Type',
  };

  if (request.method === 'OPTIONS') {
    response.writeHead(204, headers);
    response.end();
    return;
  }

  try {
    const upstream = await fetch(`${TARGET}${request.url}`, { method: request.method });
    const body = await upstream.text();
    response.writeHead(upstream.status, {
      ...headers,
      'Content-Type': upstream.headers.get('content-type') ?? 'application/json',
    });
    response.end(body);
  } catch (error) {
    response.writeHead(502, headers);
    response.end(JSON.stringify({ error: { code: 'internal', message: String(error) } }));
  }
}).listen(PORT, '127.0.0.1', () => console.log(`cors proxy ${PORT} -> ${TARGET}`));
