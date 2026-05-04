import { generateVerifier, generateState, computeChallenge } from './pkce';
import type { BackendClient } from './client';

interface PendingAuth {
  verifier: string;
  state: string;
}

export class AuthFlow {
  private pending: PendingAuth | null = null;

  constructor(
    private client: BackendClient,
    private backendUrl: () => string,
  ) {}

  async beginAuth(): Promise<void> {
    const verifier = generateVerifier();
    const state = generateState();
    const challenge = await computeChallenge(verifier);
    this.pending = { verifier, state };

    const redirectUri = 'obsidian://pubobs-callback';
    const base = this.backendUrl().replace(/\/$/, '');
    const url =
      `${base}/auth/plugin` +
      `?code_challenge=${encodeURIComponent(challenge)}` +
      `&code_challenge_method=S256` +
      `&redirect_uri=${encodeURIComponent(redirectUri)}` +
      `&state=${encodeURIComponent(state)}`;

    window.open(url);
  }

  async handleCallback(
    params: Record<string, string>,
    onSuccess: () => Promise<void>,
    onError: (msg: string) => void,
  ): Promise<void> {
    if (!this.pending) {
      onError('No pending auth session');
      return;
    }
    if (params['state'] !== this.pending.state) {
      onError('State mismatch — possible CSRF');
      this.pending = null;
      return;
    }
    const code = params['code'];
    if (!code) {
      onError('Missing code in callback');
      this.pending = null;
      return;
    }
    const { verifier } = this.pending;
    this.pending = null;
    try {
      const tokens = await this.client.exchangeToken(code, verifier);
      this.client.applyTokens(tokens);
      await onSuccess();
    } catch (e: unknown) {
      onError(e instanceof Error ? e.message : String(e));
    }
  }
}
