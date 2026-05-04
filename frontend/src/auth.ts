import { generateVerifier, generateState, computeChallenge } from './pkce';
import { exchangeToken, applyTokenResponse, tokenStore } from './api';

const VERIFIER_KEY = 'pubobs_pkce_verifier';
const STATE_KEY = 'pubobs_pkce_state';

export async function beginAuth(): Promise<void> {
  const verifier = generateVerifier();
  const state = generateState();
  const challenge = await computeChallenge(verifier);
  sessionStorage.setItem(VERIFIER_KEY, verifier);
  sessionStorage.setItem(STATE_KEY, state);

  const redirectUri = encodeURIComponent(location.origin + '/');
  const url =
    `/auth/plugin` +
    `?code_challenge=${encodeURIComponent(challenge)}` +
    `&code_challenge_method=S256` +
    `&redirect_uri=${redirectUri}` +
    `&state=${encodeURIComponent(state)}`;
  location.href = url;
}

// Returns true if a callback was detected and handled.
export async function handleCallbackIfPresent(): Promise<boolean> {
  const params = new URLSearchParams(location.search);
  const code = params.get('code');
  const state = params.get('state');
  if (!code || !state) return false;

  // Clean the URL immediately so a refresh doesn't re-run this
  history.replaceState(null, '', '/');

  const storedState = sessionStorage.getItem(STATE_KEY);
  const verifier = sessionStorage.getItem(VERIFIER_KEY);
  sessionStorage.removeItem(STATE_KEY);
  sessionStorage.removeItem(VERIFIER_KEY);

  if (state !== storedState || !verifier) {
    throw new Error('Auth state mismatch — please try signing in again');
  }

  const tokens = await exchangeToken(code, verifier);
  applyTokenResponse(tokens);
  return true;
}

export function isAuthenticated(): boolean {
  const t = tokenStore.get();
  return t != null && t.accessToken !== '';
}

export function signOut(): void {
  tokenStore.clear();
  location.hash = '';
  location.reload();
}
