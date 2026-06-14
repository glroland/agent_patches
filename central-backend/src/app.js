import express from 'express';
import cors from 'cors';
import { config } from './config/index.js';
import { requestLogger } from './middleware/requestLogger.js';
import { notFoundHandler, errorHandler } from './middleware/errorHandler.js';
import apiRouter from './routes/index.js';

export function createApp() {
  const app = express();

  app.use(cors({ origin: config.cors.origin }));
  app.use(express.json());
  app.use(requestLogger);

  app.use('/api', apiRouter);

  app.use(notFoundHandler);
  app.use(errorHandler);

  return app;
}
