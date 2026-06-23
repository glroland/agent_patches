// Loaded via --import before any application module is evaluated.
// Reads DOTENV_CONFIG_PATH (set by the Makefile) so the root .env file
// is available to config/index.js when it first runs.
import { config } from 'dotenv';
config({ path: process.env.DOTENV_CONFIG_PATH || '.env' });
