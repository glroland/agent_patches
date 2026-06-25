// Loaded via --import before any application module is evaluated.
// Reads DOTENV_CONFIG_PATH (set by the Makefile) so the root .env file
// is available to config/index.js when it first runs.
import { config } from 'dotenv';
config({ path: process.env.DOTENV_CONFIG_PATH || '.env' });

// Must run after dotenv (so a .env-supplied OTEL_* var is visible) and
// before src/index.js is imported, so OpenTelemetry's auto-instrumentation
// patches http/express/undici before those modules are first required.
import './tracing.js';
