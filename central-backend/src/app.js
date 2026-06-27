import express from 'express';
import cors from 'cors';
import { config } from './config/index.js';
import { requestLogger } from './middleware/requestLogger.js';
import { requireAuth } from './middleware/auth.js';
import { notFoundHandler, errorHandler } from './middleware/errorHandler.js';
import authRouter from './routes/auth.js';
import apiRouter from './routes/index.js';

export function createApp() {
  const app = express();

  app.use(cors({ origin: config.cors.origin }));
  app.use(express.json());
  app.use(requestLogger);

  // Public endpoints — no auth required.
  app.use('/api/auth', authRouter);
  app.get('/api/health', (_req, res) => res.json({ status: 'ok' }));

  // All other /api routes require a valid OpenShift bearer token.
  app.use('/api', requireAuth, apiRouter);

  app.use(notFoundHandler);
  app.use(errorHandler);

  return app;
}
