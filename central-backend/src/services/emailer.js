import nodemailer from 'nodemailer';
import { logger } from '../utils/logger.js';

function createTransport(cfg) {
  const base = { host: cfg.host, port: cfg.port };
  switch (cfg.tlsMode) {
    case 'tls':
      return nodemailer.createTransport({
        ...base,
        secure: true,
        auth: { user: cfg.username, pass: cfg.password },
      });
    case 'none':
      return nodemailer.createTransport({ ...base, secure: false, ignoreTLS: true });
    default: // starttls
      return nodemailer.createTransport({
        ...base,
        secure: false,
        auth: { user: cfg.username, pass: cfg.password },
      });
  }
}

export async function sendEmail(cfg, subject, body) {
  const transport = createTransport(cfg);
  await transport.sendMail({ from: cfg.from, to: cfg.to, subject, text: body });
  logger.info(`emailer: sent "${subject}"`);
}
