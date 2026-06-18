if (!process.env.AGENT_AUTH_TOKEN) {
  throw new Error('AGENT_AUTH_TOKEN environment variable is required (see .env.example)');
}

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
    pollIntervalSeconds: Number(process.env.AGENT_POLL_INTERVAL_SECONDS) || 60,
    inventoryFile: process.env.AGENT_INVENTORY_FILE,
    authToken: process.env.AGENT_AUTH_TOKEN,
  },
  email: {
    // Set EMAIL_ENABLED=true and fill in the remaining vars to enable.
    enabled: process.env.EMAIL_ENABLED === 'true',
    host: process.env.EMAIL_HOST || '',
    port: Number(process.env.EMAIL_PORT) || 587,
    username: process.env.EMAIL_USERNAME || '',
    password: process.env.EMAIL_PASSWORD || '',
    from: process.env.EMAIL_FROM || '',
    // Comma-separated list of recipient addresses.
    to: (process.env.EMAIL_TO || '').split(',').map((s) => s.trim()).filter(Boolean),
    // Transport security: "starttls" (default, port 587) | "tls" (port 465) | "none"
    tlsMode: process.env.EMAIL_TLS_MODE || 'starttls',
    // Local HH:MM time to send the daily fleet summary.
    dailySummaryTime: process.env.EMAIL_DAILY_SUMMARY_TIME || '07:00',
  },
  logging: {
    level: process.env.LOG_LEVEL || 'info',
  },
};
