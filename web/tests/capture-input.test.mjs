import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const sent = [];
class Socket {
  static OPEN = 1;
  readyState = 1;
  send(data) { sent.push(typeof data === 'string' ? JSON.parse(data) : Array.from(new Uint8Array(data))); }
  close() { this.readyState = 3; }
}
const source = readFileSync(new URL('../src/lib/websocket.ts', import.meta.url), 'utf8');
const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
const exports = {};
new Function('exports', 'require', js)(exports, name => {
  if (name === 'websocket') return { w3cwebsocket: Socket };
  if (name.endsWith('service.ts')) return { getBaseUrl: () => 'ws://test' };
  if (name.endsWith('auth-events.ts')) return { notifyAuthExpired() {} };
  throw new Error(name);
});

test('capture input gate releases held controls, blocks all input representations and preserves heartbeat', () => {
  const c = new exports.WsClient();
  c.connect();
  assert.equal(c.send(new Uint8Array([1,0,0,4,0,0,0,0,0])), false);
  assert.equal(c.send(new Uint8Array([0])), true);
  c.setInputEnabled(true);
  assert.equal(c.send(new Uint8Array([1,2,0,4,0,0,0,0,0])), true);
  assert.equal(c.send(new Uint8Array([2,1,10,0,20,0,0])), true);
  c.setInputEnabled(false);
  assert.deepEqual(sent.slice(-2), [[1,0,0,0,0,0,0,0,0],[2,0,10,0,20,0,0]]);
  const count = sent.length;
  for (const value of [[1,0,0,4], new Uint8Array([2,1,3,4,0]), new Uint8Array([2,1,3,4,0]).buffer])
    assert.equal(c.send(value), false);
  assert.equal(sent.length, count);
  assert.equal(c.send(new Uint8Array([0])), true);
  c.setInputEnabled(true);
  c.send(new Uint8Array([2,1,100,100,1]));
  c.setInputEnabled(false);
  assert.deepEqual(sent.at(-1), [2,0,0,0,0]);
  c.close();
});

test('capture gate releases both AC Pan mouse report formats', () => {
 const c = new exports.WsClient(); c.connect(); c.setInputEnabled(true);
 c.send(new Uint8Array([2,1,3,4,5,6])); c.setInputEnabled(false);
 assert.deepEqual(sent.at(-1),[2,0,0,0,0,0]);
 c.setInputEnabled(true); c.send(new Uint8Array([2,1,10,0,20,0,5,6]));
 c.setInputEnabled(false); assert.deepEqual(sent.at(-1),[2,0,10,0,20,0,0,0]);
 c.close();
});
