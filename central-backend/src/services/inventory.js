// Loads the read-only agent inventory from a CSV file referenced by the
// AGENT_INVENTORY_FILE environment variable. The CSV must have a header row
// with columns: display_name, fqdn, port, os_type. The optional columns
// "role" and "tags" (a comma-separated list within the cell) carry
// operator-assigned metadata about each host.

import fs from 'node:fs';
import path from 'node:path';
import { parse } from 'csv-parse/sync';
import { config } from '../config/index.js';

const REQUIRED_COLUMNS = ['display_name', 'fqdn', 'port', 'os_type'];

function toAgent(record) {
  return {
    id: record.fqdn,
    displayName: record.display_name,
    fqdn: record.fqdn,
    port: Number(record.port),
    osType: record.os_type,
    role: record.role || '',
    tags: record.tags
      ? record.tags.split(',').map((tag) => tag.trim()).filter(Boolean)
      : [],
  };
}

// Reads and parses the inventory CSV. Throws if AGENT_INVENTORY_FILE is not
// set, the file cannot be read, or required columns are missing.
export function loadInventory() {
  const file = config.agents.inventoryFile;
  if (!file) {
    throw new Error('AGENT_INVENTORY_FILE is not set');
  }

  const resolved = path.resolve(file);
  const contents = fs.readFileSync(resolved, 'utf8');
  const records = parse(contents, {
    columns: true,
    skip_empty_lines: true,
    trim: true,
  });

  for (const column of REQUIRED_COLUMNS) {
    if (records.length > 0 && !(column in records[0])) {
      throw new Error(`inventory file ${resolved} is missing required column "${column}"`);
    }
  }

  return records.map(toAgent);
}

// Returns the full agent inventory.
export function listAgents() {
  return loadInventory();
}

// Returns a single agent by id (fqdn), or undefined if not found.
export function getAgent(id) {
  return loadInventory().find((agent) => agent.id === id);
}
