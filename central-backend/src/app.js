import express from 'express';
import cors from 'cors';
import { config } from './config/index.js';
import { requestLogger } from './middleware/requestLogger.js';
import { requireAuth } from './middleware/auth.js';
import { notFoundHandler, errorHandler } from './middleware/errorHandler.js';
import authRouter from './routes/auth.js';
import apiRouter from './routes/index.js';
import * as gatewayService from './services/gatewayService.js';

export function createApp() {
  const app = express();

  app.use(cors({ origin: config.cors.origin }));
  app.use(express.json());
  app.use(requestLogger);

  // Public endpoints — no auth required.
  app.use('/api/auth', authRouter);

  // Liveness: this process is up. Must never depend on downstream services —
  // a llm-gateway outage should not cause Kubernetes to restart this pod.
  app.get('/api/health', (_req, res) => res.json({ status: 'ok' }));

  // Readiness: this process is up AND its llm-gateway dependency is up.
  // Deliberately checks llm-gateway's plain /health (process reachability
  // only) rather than /health/ready (which would also verify the upstream
  // LLM model) — see gatewayService.getHealth for why. Used as the
  // readinessProbe so this pod only receives traffic once the gateway is
  // actually reachable.
  app.get('/api/health/ready', async (_req, res) => {
    try {
      const gateway = await gatewayService.getHealth();
      if (gateway && !gateway.ok) {
        return res.status(503).json({ status: 'unhealthy', gateway });
      }
      res.json({ status: 'ok', gateway: gateway || { configured: false } });
    } catch (err) {
      res.status(503).json({ status: 'unhealthy', error: err.message });
    }
  });

  // All other /api routes require a valid OpenShift bearer token.
  app.use('/api', requireAuth, apiRouter);

  app.use(notFoundHandler);
  app.use(errorHandler);

  return app;
}
