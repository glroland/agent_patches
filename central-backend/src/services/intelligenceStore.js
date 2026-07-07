// Persistent storage for fleet intelligence reports.
// Writes to INTELLIGENCE_DATA_DIR (a PVC mount in production).
// When dataDir is unset the module is a no-op — in-memory only behaviour is preserved.

import { readFileSync, writeFileSync, appendFileSync, existsSync, mkdirSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { logger } from '../utils/logger.js';
import { config } from '../config/index.js';

function latestPath() { return join(config.intelligence.dataDir, 'intelligence-latest.json'); }
function historyPath() { return join(config.intelligence.dataDir, 'intelligence-history.jsonl'); }

function ensureDir() {
  const dir = config.intelligence.dataDir;
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
}

// Returns the most recently persisted report, or null.
export function loadLatest() {
  if (!config.intelligence.dataDir) return null;
  try {
    const path = latestPath();
    if (!existsSync(path)) return null;
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch (err) {
    logger.warn(`intelligenceStore: failed to load latest report: ${err.message}`);
    return null;
  }
}

// Writes latest.json and appends to history.jsonl atomically enough for our purposes.
export function persist(report) {
  if (!config.intelligence.dataDir) return;
  try {
    ensureDir();
    writeFileSync(latestPath(), JSON.stringify(report), 'utf8');
    appendFileSync(historyPath(), JSON.stringify(report) + '\n', 'utf8');
  } catch (err) {
    logger.warn(`intelligenceStore: failed to persist report: ${err.message}`);
  }
}

// Deletes latest.json and history.jsonl so no persisted report survives.
export function clearAll() {
  if (!config.intelligence.dataDir) return;
  try {
    rmSync(latestPath(), { force: true });
    rmSync(historyPath(), { force: true });
  } catch (err) {
    logger.warn(`intelligenceStore: failed to clear persisted reports: ${err.message}`);
  }
}

// Returns the last n reports from history.jsonl, oldest first.
export function loadHistory(n) {
  if (!config.intelligence.dataDir || n <= 0) return [];
  try {
    const path = historyPath();
    if (!existsSync(path)) return [];
    const lines = readFileSync(path, 'utf8').trimEnd().split('\n').filter(Boolean);
    return lines.slice(-n).map((l) => JSON.parse(l));
  } catch (err) {
    logger.warn(`intelligenceStore: failed to load history: ${err.message}`);
    return [];
  }
}
