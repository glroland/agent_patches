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
    // Path to the CSV file listing enrolled agents (display name, fqdn, port, os flavor).
    inventoryFile: process.env.AGENT_INVENTORY_FILE,
    // Bearer token sent to every endpoint-server agent, when set. Must match
    // each agent's security.token (security.scheme: bearer in config.yaml).
    authToken: process.env.AGENT_AUTH_TOKEN,
  },
  logging: {
    level: process.env.LOG_LEVEL || 'info',
  },
};
