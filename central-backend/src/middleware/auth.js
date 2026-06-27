import { readFileSync } from 'node:fs';
import { config } from '../config/index.js';
import { logger } from '../utils/logger.js';

const SA_TOKEN_PATH = '/var/run/secrets/kubernetes.io/serviceaccount/token';

// Simple TTL cache: token string → { authenticated, username, exp }
const cache = new Map();
const CACHE_TTL_MS = 60_000;

function getSaToken() {
  try {
    return readFileSync(SA_TOKEN_PATH, 'utf8').trim();
  } catch {
    return null;
  }
}

// Validates a bearer token via the Kubernetes TokenReview API.
// Returns { authenticated: bool, username: string|null }.
export async function validateToken(bearerToken) {
  const hit = cache.get(bearerToken);
  if (hit && hit.exp > Date.now()) return hit;

  const saToken = getSaToken();
  if (!saToken) {
    logger.warn('auth: no SA token — assuming dev environment, skipping validation');
    const result = { authenticated: true, username: 'dev', exp: Date.now() + CACHE_TTL_MS };
    cache.set(bearerToken, result);
    return result;
  }

  let result;
  try {
    const resp = await fetch(
      `${config.openshift.apiUrl}/apis/authentication.k8s.io/v1/tokenreviews`,
      {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${saToken}`,
          'Content-Type': 'application/json',
          Accept: 'application/json',
        },
        body: JSON.stringify({
          apiVersion: 'authentication.k8s.io/v1',
          kind: 'TokenReview',
          spec: { token: bearerToken },
        }),
      },
    );

    if (!resp.ok) {
      logger.warn(`auth: TokenReview ${resp.status}`);
      result = { authenticated: false, username: null, exp: Date.now() + 5_000 };
    } else {
      const data = await resp.json();
      result = {
        authenticated: data.status?.authenticated === true,
        username: data.status?.user?.username ?? null,
        exp: Date.now() + CACHE_TTL_MS,
      };
    }
  } catch (err) {
    logger.error(`auth: TokenReview error: ${err.message}`);
    result = { authenticated: false, username: null, exp: Date.now() + 5_000 };
  }

  cache.set(bearerToken, result);
  setTimeout(() => cache.delete(bearerToken), CACHE_TTL_MS);
  return result;
}

// Express middleware — rejects unauthenticated requests with 401.
// Attach this after /api/auth and /api/health routes.
export async function requireAuth(req, res, next) {
  const header = req.headers.authorization;
  if (!header?.startsWith('Bearer ')) {
    return res.status(401).json({ message: 'Authentication required' });
  }

  const token = header.slice(7);
  try {
    const { authenticated, username } = await validateToken(token);
    if (!authenticated) {
      return res.status(401).json({ message: 'Invalid or expired token' });
    }
    req.user = { username };
    next();
  } catch (err) {
    logger.error(`auth: middleware error: ${err.message}`);
    res.status(500).json({ message: 'Auth service unavailable' });
  }
}

// Used by wsHub to validate the token query-param on WebSocket upgrade.
export async function validateWsToken(token) {
  if (!token) return false;
  const { authenticated } = await validateToken(token);
  return authenticated;
}
