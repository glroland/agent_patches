import { config } from '../config/index.js';

const LEVELS = ['debug', 'info', 'warn', 'error'];

function log(level, ...args) {
  if (LEVELS.indexOf(level) < LEVELS.indexOf(config.logging.level)) return;
  const ts = new Date().toISOString();
  // eslint-disable-next-line no-console
  console[level === 'debug' ? 'log' : level](`[${ts}] [${level}]`, ...args);
}

export const logger = {
  debug: (...args) => log('debug', ...args),
  info: (...args) => log('info', ...args),
  warn: (...args) => log('warn', ...args),
  error: (...args) => log('error', ...args),
};
