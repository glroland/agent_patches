// OAuth2 Authorization Code + PKCE helpers.
// All sessionStorage keys are prefixed to avoid collisions.

const TOKEN_KEY    = 'ap_token';
const STATE_KEY    = 'ap_oauth_state';
const VERIFIER_KEY = 'ap_code_verifier';
const RETURN_KEY   = 'ap_return_url';

// ── PKCE helpers ──────────────────────────────────────────────────────────────

function base64url(bytes) {
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

async function generateCodeVerifier() {
  const buf = new Uint8Array(32);
  crypto.getRandomValues(buf);
  return base64url(buf);
}

async function generateCodeChallenge(verifier) {
  const data = new TextEncoder().encode(verifier);
  const digest = await crypto.subtle.digest('SHA-256', data);
  return base64url(new Uint8Array(digest));
}

function generateState() {
  const buf = new Uint8Array(16);
  crypto.getRandomValues(buf);
  return base64url(buf);
}

// ── Token storage (sessionStorage — cleared when tab closes) ──────────────────

export function getToken() {
  return sessionStorage.getItem(TOKEN_KEY);
}

export function storeToken(token) {
  sessionStorage.setItem(TOKEN_KEY, token);
}

export function clearToken() {
  [TOKEN_KEY, STATE_KEY, VERIFIER_KEY, RETURN_KEY].forEach((k) =>
    sessionStorage.removeItem(k));
}

// ── Login redirect ─────────────────────────────────────────────────────────────

export async function startLogin({ clientId, authorizeUrl }) {
  const state        = generateState();
  const verifier     = await generateCodeVerifier();
  const challenge    = await generateCodeChallenge(verifier);
  const returnUrl    = window.location.pathname + window.location.search;
  const redirectUri  = `${window.location.origin}/oauth/callback`;

  sessionStorage.setItem(STATE_KEY,    state);
  sessionStorage.setItem(VERIFIER_KEY, verifier);
  sessionStorage.setItem(RETURN_KEY,   returnUrl);

  const params = new URLSearchParams({
    client_id:             clientId,
    response_type:         'code',
    redirect_uri:          redirectUri,
    scope:                 'user:info',
    state,
    code_challenge:        challenge,
    code_challenge_method: 'S256',
  });

  window.location.href = `${authorizeUrl}?${params}`;
}

// ── Callback: exchange code for token ─────────────────────────────────────────
// Returns { access_token, returnUrl }.
// Throws on state mismatch or exchange failure.

export async function handleCallback(searchParams) {
  const code      = searchParams.get('code');
  const state     = searchParams.get('state');
  const savedState   = sessionStorage.getItem(STATE_KEY);
  const codeVerifier = sessionStorage.getItem(VERIFIER_KEY);
  const returnUrl    = sessionStorage.getItem(RETURN_KEY) || '/';

  if (!code)            throw new Error('No authorization code in callback URL');
  if (state !== savedState) throw new Error('OAuth state mismatch — possible CSRF');

  const redirectUri = `${window.location.origin}/oauth/callback`;

  const resp = await fetch('/api/auth/callback', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code, redirect_uri: redirectUri, code_verifier: codeVerifier }),
  });

  if (!resp.ok) {
    const err = await resp.json().catch(() => ({}));
    throw new Error(err.message || `Login failed (${resp.status})`);
  }

  const { access_token } = await resp.json();

  [STATE_KEY, VERIFIER_KEY, RETURN_KEY].forEach((k) => sessionStorage.removeItem(k));

  return { access_token, returnUrl };
}
