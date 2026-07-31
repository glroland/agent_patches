import { logger } from '../utils/logger.js';

// Kubernetes hits these on a short liveness/readiness interval — logging them
// at info level would drown out everything else. They're still visible with
// LOG_LEVEL=debug.
const QUIET_PATHS = new Set(['/api/health', '/api/health/ready']);

export function requestLogger(req, res, next) {
  const start = Date.now();
  res.on('finish', () => {
    const level = QUIET_PATHS.has(req.path) ? 'debug' : 'info';
    logger[level](`${req.method} ${req.originalUrl} -> ${res.statusCode} (${Date.now() - start}ms)`);
  });
  next();
}
