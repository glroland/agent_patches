import { Router }      from 'express';
import https            from 'node:https';
import { readFileSync } from 'node:fs';
import { config }       from '../config/index.js';
import { logger }       from '../utils/logger.js';
import { issueSessionToken } from '../utils/sessionToken.js';

const router = Router();

const SA_TOKEN_PATH = '/var/run/secrets/kubernetes.io/serviceaccount/token';
const SA_CA_PATH    = '/var/run/secrets/kubernetes.io/serviceaccount/ca.crt';

function getSaToken() {
  try { return readFileSync(SA_TOKEN_PATH, 'utf8').trim(); } catch { return null; }
}
function getClusterCa() {
  try { return readFileSync(SA_CA_PATH); } catch { return null; }
}

// Minimal node:https fetch-like wrapper.
// Avoids undici (Node native fetch) issues with custom CAs.
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
router.get('/config', (_req, res) => {
  res.json({
    clientId:     config.openshift.oauthClientId,
    authorizeUrl: config.openshift.oauthAuthorizeUrl,
  });
});

// Exchanges an authorization code for a signed session JWT.
//
// Step 1 — code exchange with the OpenShift OAuth Route (uses oauthTls).
// Step 2 — TokenReview: SA asks the k8s API to validate the user's token and
//           return the username.  SA needs system:auth-delegator for this.
// Step 3 — SubjectAccessReview: SA checks whether that username can perform
//           the configured verb on the configured resource (cluster-admin gate).
// Step 4 — issue signed 8-hour session JWT.
//
// In dev (no SA token / no cluster CA), steps 2-3 are skipped.
router.post('/callback', async (req, res) => {
  const { code, redirect_uri, code_verifier } = req.body;
  if (!code || !redirect_uri) {
    return res.status(400).json({ message: 'code and redirect_uri are required' });
  }

  const oauthTls = { rejectUnauthorized: !config.openshift.tlsInsecure };

  // ── Step 1: exchange authorization code for OpenShift access token ──────────
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
    if (!accessToken) {
      logger.warn('auth/callback: no access_token in exchange response');
      return res.status(401).json({ message: 'Token exchange returned no token' });
    }
  } catch (err) {
    logger.error(`auth/callback: exchange error: ${err.message}`);
    return res.status(500).json({ message: 'Token exchange error' });
  }

  // ── Steps 2-3: cluster validation (skipped outside a cluster) ───────────────
  const saToken   = getSaToken();
  const clusterCa = getClusterCa();
  let   username  = 'user';

  if (!saToken || !clusterCa) {
    logger.warn(`auth/callback: ${!saToken ? 'no SA token' : 'no cluster CA'} — skipping validation (dev mode)`);
  } else {
    // Step 2: TokenReview — validate the user's OAuth token and get the username.
    // The SA is the requester; system:auth-delegator grants create on tokenreviews.
    // This works with OpenShift OAuth tokens because the API server's authenticator
    // chain includes the OpenShift OAuth token validator.
    try {
      const trResp = await httpsRequest(
        `${config.openshift.apiUrl}/apis/authentication.k8s.io/v1/tokenreviews`,
        {
          method:  'POST',
          headers: { Authorization: `Bearer ${saToken}`, 'Content-Type': 'application/json' },
          body:    JSON.stringify({
            apiVersion: 'authentication.k8s.io/v1',
            kind:       'TokenReview',
            spec:       { token: accessToken },
          }),
          ca: clusterCa,
        },
      );

      if (!trResp.ok) {
        const errText = await trResp.text().catch(() => '');
        logger.warn(`auth/callback: TokenReview returned ${trResp.status}: ${errText}`);
        return res.status(401).json({ message: 'Token validation failed' });
      }

      const tr = await trResp.json();
      if (!tr.status?.authenticated) {
        logger.warn(`auth/callback: token not authenticated (TokenReview denied)`);
        return res.status(401).json({ message: 'Token not authenticated' });
      }

      username = tr.status.user?.username || 'user';
      logger.info(`auth/callback: authenticated as ${username}`);
    } catch (err) {
      logger.error(`auth/callback: TokenReview error: ${err.message}`);
      return res.status(500).json({ message: 'Token validation error' });
    }

    // Step 3: SubjectAccessReview — check cluster-admin gate.
    // SA is again the requester; system:auth-delegator grants create on subjectaccessreviews.
    try {
      const sarResp = await httpsRequest(
        `${config.openshift.apiUrl}/apis/authorization.k8s.io/v1/subjectaccessreviews`,
        {
          method:  'POST',
          headers: { Authorization: `Bearer ${saToken}`, 'Content-Type': 'application/json' },
          body:    JSON.stringify({
            apiVersion: 'authorization.k8s.io/v1',
            kind:       'SubjectAccessReview',
            spec: {
              user: username,
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
        const errText = await sarResp.text().catch(() => '');
        logger.warn(`auth/callback: SAR returned ${sarResp.status} for ${username}: ${errText}`);
        return res.status(403).json({ message: 'Access check failed' });
      }

      const sar = await sarResp.json();
      if (!sar.status?.allowed) {
        logger.warn(`auth/callback: access denied for ${username} — cluster-admin required`);
        return res.status(403).json({ message: 'Access denied: cluster-admin role required' });
      }

      logger.info(`auth/callback: ${username} passed cluster-admin check`);
    } catch (err) {
      logger.error(`auth/callback: SAR error: ${err.message}`);
      return res.status(500).json({ message: 'Access check error' });
    }
  }

  // ── Step 4: issue signed session JWT ────────────────────────────────────────
  const sessionToken = issueSessionToken(username);
  logger.info(`auth/callback: issued session for ${username}`);
  res.json({ access_token: sessionToken });
});

export default router;
