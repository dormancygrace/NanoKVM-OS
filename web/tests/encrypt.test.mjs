import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import test from 'node:test';

import { encrypt } from '../src/lib/encrypt.ts';

const require = createRequire(import.meta.url);
// The full crypto-js build, as the previous implementation imported it.
const CryptoJS = require('crypto-js');
const AES = require('crypto-js/aes');
const SECRET_KEY = 'nanokvm-sipeed-2024';

test('encrypt output is URI-encoded OpenSSL-salted AES that the full library decrypts', () => {
  for (const input of ['admin', 'p@ss wörd/+=?&', '', 'пароль 🔑']) {
    const encoded = encrypt(input);
    const ciphertext = decodeURIComponent(encoded);
    assert.equal(encoded, encodeURIComponent(ciphertext));
    // Base64 of the "Salted__" header, as CryptoJS.AES.encrypt(...).toString() emits.
    assert.match(ciphertext, /^U2FsdGVkX1[A-Za-z0-9+/]+=*$/);
    const plain = CryptoJS.AES.decrypt(ciphertext, SECRET_KEY).toString(CryptoJS.enc.Utf8);
    assert.equal(plain, input);
  }
});

test('the modular AES import matches the full crypto-js build byte for byte', () => {
  const salt = CryptoJS.enc.Hex.parse('0011223344556677');
  const full = CryptoJS.AES.encrypt('secret', SECRET_KEY, { salt }).toString();
  const modular = AES.encrypt('secret', SECRET_KEY, { salt }).toString();
  assert.equal(modular, full);
});
