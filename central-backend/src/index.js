import { createServer } from 'node:http';
import { createApp } from './app.js';
import { config } from './config/index.js';
import { logger } from './utils/logger.js';
import * as poller from './services/poller.js';
import * as wsHub from './services/wsHub.js';
import * as notifier from './services/notifier.js';
import * as intelligence from './services/intelligence.js';
import * as briefing from './services/briefing.js';

const app = createApp();
const server = createServer(app);

wsHub.attach(server);

server.listen(config.server.port, config.server.host, () => {
  logger.info(`central-backend listening on http://${config.server.host}:${config.server.port}`);
  notifier.start(config.email);
  poller.start();
  intelligence.start();
  briefing.start();
});
