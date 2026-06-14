import { createApp } from './app.js';
import { config } from './config/index.js';
import { logger } from './utils/logger.js';
import * as poller from './services/poller.js';

const app = createApp();

app.listen(config.server.port, config.server.host, () => {
  logger.info(`central-backend listening on http://${config.server.host}:${config.server.port}`);
  poller.start();
});
