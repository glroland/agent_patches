import { logger } from '../utils/logger.js';

export function notFoundHandler(req, res) {
  res.status(404).json({ error: 'not_found', message: `No route for ${req.method} ${req.originalUrl}` });
}

// eslint-disable-next-line no-unused-vars
export function errorHandler(err, req, res, next) {
  const status = err.status || 500;
  logger.error(`${req.method} ${req.originalUrl} -> unhandled error (${status}): ${err.message}`, { stack: err.stack });
  res.status(status).json({ error: err.code || 'internal_error', message: err.message || 'Unexpected error' });
}
