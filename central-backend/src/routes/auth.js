import { Router } from 'express';
import { readFileSync } from 'node:fs';
import { config } from '../config/index.js';
import { logger } from '../utils/logger.js';

const router = Router();

const SA_TOKEN_PATH = '/var/run/secrets/kubernetes.io/serviceaccount/token';

function getSaToken() {
  try { return readFileSync(SA_TOKEN_PATH, 'utf8').trim(); } catch { return null; }
}

// Returns the public OAuth config the UI needs to build the login redirect.
// No authentication required.
router.get('/config', (_req, res) => {
  res.json({
    clientId:     config.openshift.oauthClientId,
    authorizeUrl: config.openshift.oauthAuthorizeUrl,
  });
});

// Exchanges an authorization code for an OpenShift access token.
// Performs a SelfSubjectAccessReview to enforce cluster-admin requirement.
// No authentication required (this IS the auth endpoint).
router.post('/callback', async (req, res) => {
  const { code, redirect_uri, code_verifier } = req.body;
  if (!code || !redirect_uri) {
    return res.status(400).json({ message: 'code and redirect_uri are required' });
  }

  // Exchange authorization code for access token.
  let accessToken;
  try {
    const params = new URLSearchParams({
      grant_type:   'authorization_code',
      code,
      redirect_uri,
      client_id:    config.openshift.oauthClientId,
      client_secret: config.openshift.oauthClientSecret,
    });
    if (code_verifier) params.set('code_verifier', code_verifier);

    const resp = await fetch(config.openshift.oauthTokenUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: params.toString(),
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

  // SelfSubjectAccessReview — check user has cluster-admin (can list nodes).
  // Uses the user's own token so no special SA permissions are needed.
  try {
    const sarResp = await fetch(
      `${config.openshift.apiUrl}/apis/authorization.k8s.io/v1/selfsubjectaccessreviews`,
      {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${accessToken}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          apiVersion: 'authorization.k8s.io/v1',
          kind: 'SelfSubjectAccessReview',
          spec: {
            resourceAttributes: {
              resource: config.openshift.sarResource,
              verb:     config.openshift.sarVerb,
            },
          },
        }),
      },
    );

    if (sarResp.ok) {
      const sar = await sarResp.json();
      if (!sar.status?.allowed) {
        logger.warn(`auth/callback: access denied for token (SAR not allowed)`);
        return res.status(403).json({ message: 'Access denied: cluster-admin role required' });
      }
    } else {
      logger.warn(`auth/callback: SAR returned ${sarResp.status} — denying access`);
      return res.status(403).json({ message: 'Access check failed' });
    }
  } catch (err) {
    logger.error(`auth/callback: SAR error: ${err.message}`);
    return res.status(500).json({ message: 'Access check error' });
  }

  res.json({ access_token: accessToken });
});

export default router;
