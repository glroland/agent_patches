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

// Minimal fetch-like wrapper backed by node:https.
// Avoids undici (Node native fetch) issues with custom CAs on Node < 20.12.
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
// Step 1 — code exchange (to OAuth Route, uses oauthTls).
// Step 2 — token validation + username lookup via /oauth/userinfo (user:info scope is enough).
// Step 3 — cluster-admin check via SubjectAccessReview using the SA token as requester
//           (scope-independent; SA has system:auth-delegator so it can check other users).
// Step 4 — issue session JWT.
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

  // ── Step 2: validate token + get username via /oauth/userinfo ────────────────
  // The user:info OAuth scope is explicitly designed for this endpoint, so this
  // works regardless of what other Kubernetes API calls the scope might restrict.
  const userinfoUrl = config.openshift.oauthTokenUrl.replace(/\/token$/, '/userinfo');
  let username = 'user';
  try {
    const uiResp = await httpsRequest(userinfoUrl, {
      headers: { Authorization: `Bearer ${accessToken}` },
      ...oauthTls,
    });
    if (!uiResp.ok) {
      const text = await uiResp.text();
      logger.warn(`auth/callback: userinfo returned ${uiResp.status}: ${text}`);
      return res.status(401).json({ message: 'Token validation failed' });
    }
    const info = await uiResp.json();
    username = info.sub || info.preferred_username || info.name || 'user';
    logger.info(`auth/callback: authenticated as ${username}`);
  } catch (err) {
    logger.error(`auth/callback: userinfo error: ${err.message}`);
    return res.status(500).json({ message: 'Token validation error' });
  }

  // ── Step 3: cluster-admin check via SubjectAccessReview (SA as requester) ───
  // Using the SA token means this is unaffected by the user's OAuth token scope.
  // system:auth-delegator on the SA grants permission to create SubjectAccessReviews.
  const saToken   = getSaToken();
  const clusterCa = getClusterCa();

  if (saToken && clusterCa) {
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
  } else {
    // Outside cluster (dev) — no SA token or CA, skip permission check.
    logger.warn(`auth/callback: skipping permission check — ${!saToken ? 'no SA token' : 'no cluster CA'} (dev mode)`);
  }

  // ── Step 4: issue signed session JWT ────────────────────────────────────────
  const sessionToken = issueSessionToken(username);
  logger.info(`auth/callback: issued session for ${username}`);
  res.json({ access_token: sessionToken });
});

export default router;
