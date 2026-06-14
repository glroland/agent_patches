import 'dotenv/config';

// Central configuration for the backend. Values are sourced from
// environment variables (see .env.example) with sensible defaults for
// local development.
export const config = {
  server: {
    host: process.env.HOST || '0.0.0.0',
    port: Number(process.env.PORT) || 4000,
  },
  cors: {
    origin: process.env.CORS_ORIGIN || 'http://localhost:5173',
  },
  agents: {
    // Interval the poller will use once implemented.
    pollIntervalSeconds: Number(process.env.AGENT_POLL_INTERVAL_SECONDS) || 60,
  },
  logging: {
    level: process.env.LOG_LEVEL || 'info',
  },
};
