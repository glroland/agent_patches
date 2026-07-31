// Persistent per-user chat history for the central UI.
// One JSON file per user under CHAT_DATA_DIR (a PVC mount in production),
// fronted by an in-memory cache so reads stay cheap and a failed disk write
// never loses the live session. When dataDir is unset the store is
// in-memory only.
//
// Pending flags track chats with a reply still in flight on this backend so
// a client that reloaded mid-send knows to keep watching. They are
// intentionally in-memory only — a backend restart aborts the request, so a
// persisted flag would never clear.

import { readFileSync, writeFileSync, existsSync, mkdirSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { logger } from '../utils/logger.js';
import { config } from '../config/index.js';

// Well-known chat ids shared with the UI. Individual agent chats use the
// agent id itself.
export const CENTRAL_CHAT_ID = '__fleet__';
export const BROADCAST_CHAT_ID = '__broadcast__';

const cache = new Map(); // username -> { chats: { chatId: [messages] } }
const pending = new Map(); // username -> Set(chatId)

function fileFor(username) {
  // Usernames come from OpenShift and may contain path-hostile characters.
  const safe = Buffer.from(username).toString('base64url');
  return join(config.chat.dataDir, `chats-${safe}.json`);
}

function load(username) {
  let data = cache.get(username);
  if (data) return data;
  data = { chats: {} };
  if (config.chat.dataDir) {
    try {
      const path = fileFor(username);
      if (existsSync(path)) data = JSON.parse(readFileSync(path, 'utf8'));
    } catch (err) {
      logger.warn(`chatStore: failed to load history for ${username}: ${err.message}`);
    }
  }
  cache.set(username, data);
  return data;
}

function persist(username) {
  if (!config.chat.dataDir) return;
  try {
    if (!existsSync(config.chat.dataDir)) mkdirSync(config.chat.dataDir, { recursive: true });
    writeFileSync(fileFor(username), JSON.stringify(cache.get(username)), 'utf8');
  } catch (err) {
    logger.warn(`chatStore: failed to persist history for ${username}: ${err.message}`);
  }
}

// All chat threads for a user plus the ids of chats with a reply in flight.
export function getHistory(username) {
  return { chats: load(username).chats, pending: [...(pending.get(username) ?? [])] };
}

export function appendMessage(username, chatId, message) {
  const data = load(username);
  const messages = [...(data.chats[chatId] ?? []), { ...message, time: new Date().toISOString() }];
  data.chats[chatId] = messages.length > config.chat.maxMessages
    ? messages.slice(-config.chat.maxMessages)
    : messages;
  persist(username);
}

export function clearChat(username, chatId) {
  const data = load(username);
  delete data.chats[chatId];
  pending.get(username)?.delete(chatId);
  persist(username);
}

// Total on-disk size of every persisted chat-history file. 0 when dataDir is
// unset (in-memory only) or hasn't been created yet. Cheap: one readdir plus
// a stat per user file, no different in cost from the write path already in
// use, and computed on demand rather than cached since the file count here
// is small (one per user, not one per message).
export function sizeBytes() {
  if (!config.chat.dataDir || !existsSync(config.chat.dataDir)) return 0;
  let total = 0;
  for (const name of readdirSync(config.chat.dataDir)) {
    if (!name.startsWith('chats-') || !name.endsWith('.json')) continue;
    try {
      total += statSync(join(config.chat.dataDir, name)).size;
    } catch {
      // file removed between readdir and stat — ignore
    }
  }
  return total;
}

export function setPending(username, chatId, isPending) {
  let set = pending.get(username);
  if (!set) pending.set(username, (set = new Set()));
  if (isPending) set.add(chatId);
  else set.delete(chatId);
}
