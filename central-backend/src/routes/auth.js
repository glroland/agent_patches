import { Router }      from 'express';
import https            from 'node:https';
import { readFileSync } from 'node:fs';
import { config }       from '../config/index.js';
import { logger }       from '../utils/logger.js';
import { issueSessionToken } from '../utils/sessionToken.js';

const router = Router();

const SA_CA_PATH = '/var/run/secrets/kubernetes.io/serviceaccount/ca.crt';

function getClusterCa() {
  try { return readFileSync(SA_CA_PATH); } catch { return undefined; }
}

// Minimal fetch-like wrapper around node:https so we can control TLS options
// explicitly — native fetch (undici) doesn't reliably pick up NODE_EXTRA_CA_CERTS
// on all Node 20 patch versions.
function httpsRequest(url, { method = 'GET', headers = {}, body = null, ...tlsOpts } = {}) {
  return new Promise((resolve, reject) => {
    const u   = new URL(url);
    const buf = body ? Buffer.from(body) : null;
    const req = https.request(
      {
        hostname: u.hostname,
        port:     u.port || 443,
        path:     u.pathname + u.search,
        method,
        headers:  { ...headers, ...(buf ? { 'Content-Length': buf.length } : {}) },
        ...tlsOpts,
      },
      (res) => {
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8');
          resolve({
            ok:     res.statusCode >= 200 && res.statusCode < 300,
            status: res.statusCode,
            text:   () => Promise.resolve(text),
            json:   () => Promise.resolve(JSON.parse(text)),
          });
        });
      },
    );
    req.on('error', reject);
    if (buf) req.write(buf);
    req.end();
  });
}

// Returns the public OAuth config the UI needs to build the login redirect.
// No authentication required.
router.get('/config', (_req, res) => {
  res.json({
    clientId:     config.openshift.oauthClientId,
    authorizeUrl: config.openshift.oauthAuthorizeUrl,
  });
});

// Exchanges an authorization code for a signed session JWT.
// Performs a SelfSubjectAccessReview to enforce cluster-admin requirement.
// No authentication required (this IS the auth endpoint).
router.post('/callback', async (req, res) => {
  const { code, redirect_uri, code_verifier } = req.body;
  if (!code || !redirect_uri) {
    return res.status(400).json({ message: 'code and redirect_uri are required' });
  }

  const oauthTls = { rejectUnauthorized: !config.openshift.tlsInsecure };

  // Step 1 — exchange authorization code for OpenShift access token.
  let accessToken;
  try {
    const params = new URLSearchParams({
      grant_type:    'authorization_code',
      code,
      redirect_uri,
      client_id:     config.openshift.oauthClientId,
      client_secret: config.openshift.oauthClientSecret,
    });
    if (code_verifier) params.set('code_verifier', code_verifier);

    const resp = await httpsRequest(config.openshift.oauthTokenUrl, {
      method:  'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body:    params.toString(),
      ...oauthTls,
    });

    if (!resp.ok) {
      const text = await resp.text();
      logger.warn(`auth/callback: exchange failed (${resp.status}): ${text}`);
      return res.status(401).json({ message: 'Token exchange failed' });
    }

    const data = await resp.json();
    accessToken = data.access_token;
  } catch (err) {
    logger.error(`auth/callback: exchange error: ${err.message}`);
    return res.status(500).json({ message: 'Token exchange error' });
  }

  // Step 2 — SelfSubjectAccessReview: confirm user has cluster-admin.
  // Uses the user's own token; no special SA permissions needed.
  // Uses node:https with the cluster CA cert for reliable TLS.
  const clusterCa = getClusterCa();
  if (clusterCa) {
    try {
      const sarResp = await httpsRequest(
        `${config.openshift.apiUrl}/apis/authorization.k8s.io/v1/selfsubjectaccessreviews`,
        {
          method:  'POST',
          headers: { Authorization: `Bearer ${accessToken}`, 'Content-Type': 'application/json' },
          body:    JSON.stringify({
            apiVersion: 'authorization.k8s.io/v1',
            kind:       'SelfSubjectAccessReview',
            spec: {
              resourceAttributes: {
                resource: config.openshift.sarResource,
                verb:     config.openshift.sarVerb,
              },
            },
          }),
          ca: clusterCa,
        },
      );

      if (!sarResp.ok) {
        logger.warn(`auth/callback: SAR returned ${sarResp.status} — denying access`);
        return res.status(403).json({ message: 'Access check failed' });
      }
      const sar = await sarResp.json();
      if (!sar.status?.allowed) {
        logger.warn('auth/callback: SAR denied — cluster-admin role required');
        return res.status(403).json({ message: 'Access denied: cluster-admin role required' });
      }
    } catch (err) {
      logger.error(`auth/callback: SAR error: ${err.message}`);
      return res.status(500).json({ message: 'Access check error' });
    }
  } else {
    // Outside cluster (dev) — skip SAR.
    logger.warn('auth/callback: no cluster CA found, skipping SAR check (dev mode)');
  }

  // Step 3 — resolve username via OpenShift userinfo endpoint (OIDC standard).
  let username = 'user';
  const userinfoUrl = config.openshift.oauthTokenUrl.replace(/\/token$/, '/userinfo');
  try {
    const uiResp = await httpsRequest(userinfoUrl, {
      headers: { Authorization: `Bearer ${accessToken}` },
      ...oauthTls,
    });
    if (uiResp.ok) {
      const info = await uiResp.json();
      username = info.sub || info.preferred_username || info.name || 'user';
    }
  } catch {
    // Non-fatal — username stays 'user'.
  }

  // Step 4 — issue a signed session JWT.
  // The frontend stores this and sends it as the Bearer token for all API calls.
  // requireAuth verifies it locally with no network calls.
  const sessionToken = issueSessionToken(username);
  logger.info(`auth/callback: issued session for ${username}`);
  res.json({ access_token: sessionToken });
});

export default router;
