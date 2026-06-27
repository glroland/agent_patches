import crypto from 'node:crypto';

const SECRET   = process.env.SESSION_SECRET || 'dev-insecure-please-set-SESSION_SECRET';
const DURATION = 8 * 60 * 60; // 8 hours in seconds

const enc = (obj) => Buffer.from(JSON.stringify(obj)).toString('base64url');
const dec = (s)   => JSON.parse(Buffer.from(s, 'base64url').toString('utf8'));
const sign = (s)  => crypto.createHmac('sha256', SECRET).update(s).digest('base64url');

export function issueSessionToken(username) {
  const h = enc({ alg: 'HS256', typ: 'JWT' });
  const p = enc({
    sub: username,
    iat: Math.floor(Date.now() / 1000),
    exp: Math.floor(Date.now() / 1000) + DURATION,
  });
  return `${h}.${p}.${sign(`${h}.${p}`)}`;
}

// Returns the payload object if the token is valid and unexpired, null otherwise.
export function verifySessionToken(token) {
  if (!token) return null;
  const parts = token.split('.');
  if (parts.length !== 3) return null;
  const [h, p, sig] = parts;

  const expected = sign(`${h}.${p}`);
  try {
    if (!crypto.timingSafeEqual(Buffer.from(sig, 'base64url'), Buffer.from(expected, 'base64url'))) {
      return null;
    }
  } catch {
    return null;
  }

  const payload = dec(p);
  if (payload.exp < Math.floor(Date.now() / 1000)) return null;
  return payload;
}
