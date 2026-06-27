import { logger } from '../utils/logger.js';
import { verifySessionToken } from '../utils/sessionToken.js';

// Validates a session JWT issued by POST /api/auth/callback.
// Pure local HMAC verification — no network calls.
export function validateToken(token) {
  const payload = verifySessionToken(token);
  if (!payload) return { authenticated: false, username: null };
  return { authenticated: true, username: payload.sub };
}

// Express middleware — rejects unauthenticated requests with 401.
export function requireAuth(req, res, next) {
  const header = req.headers.authorization;
  if (!header?.startsWith('Bearer ')) {
    return res.status(401).json({ message: 'Authentication required' });
  }
  const { authenticated, username } = validateToken(header.slice(7));
  if (!authenticated) {
    logger.debug('auth: rejected invalid or expired session token');
    return res.status(401).json({ message: 'Invalid or expired token' });
  }
  req.user = { username };
  next();
}

// Used by wsHub to validate the ?token= query param on WebSocket upgrade.
export function validateWsToken(token) {
  return validateToken(token).authenticated;
}
