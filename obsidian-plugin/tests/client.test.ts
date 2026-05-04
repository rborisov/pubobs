import type { RequestUrlResponse } from 'obsidian';

const mockRequestUrl = jest.fn<Promise<RequestUrlResponse>, [any]>();

jest.mock('obsidian', () => ({
  requestUrl: (...args: any[]) => mockRequestUrl(...args),
}));

import { BackendClient } from '../src/client';
import { DEFAULT_SETTINGS } from '../src/types';
import type { PubObsSettings } from '../src/types';

function makeSettings(overrides: Partial<PubObsSettings> = {}): PubObsSettings {
  return {
    ...DEFAULT_SETTINGS,
    backendUrl: 'http://localhost:8080',
    accessToken: 'access-token',
    refreshToken: 'refresh-token',
    tokenExpiresAt: Math.floor(Date.now() / 1000) + 3600, // fresh
    ...overrides,
  };
}

function mockResp(status: number, json: unknown): RequestUrlResponse {
  return { status, json, headers: {}, text: JSON.stringify(json), arrayBuffer: new ArrayBuffer(0) };
}

describe('BackendClient', () => {
  let settings: PubObsSettings;
  let save: jest.Mock;
  let client: BackendClient;

  beforeEach(() => {
    settings = makeSettings();
    save = jest.fn().mockResolvedValue(undefined);
    client = new BackendClient(settings, save);
    mockRequestUrl.mockReset();
  });

  test('injects Authorization header on authenticated requests', async () => {
    mockRequestUrl.mockResolvedValue(mockResp(200, []));
    await client.listRepos();
    expect(mockRequestUrl).toHaveBeenCalledWith(
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: 'Bearer access-token' }),
      })
    );
  });

  test('skips refresh when token is fresh', async () => {
    mockRequestUrl.mockResolvedValue(mockResp(200, []));
    await client.listRepos();
    // only one call — no refresh preflight
    expect(mockRequestUrl).toHaveBeenCalledTimes(1);
  });

  test('refreshes token when near expiry', async () => {
    settings.tokenExpiresAt = Math.floor(Date.now() / 1000) + 30; // about to expire
    settings.accessToken = 'old-token';

    mockRequestUrl
      .mockResolvedValueOnce(
        mockResp(200, { access_token: 'new-token', refresh_token: 'new-refresh', expires_in: 3600 })
      )
      .mockResolvedValueOnce(mockResp(200, []));

    await client.listRepos();

    expect(settings.accessToken).toBe('new-token');
    expect(settings.refreshToken).toBe('new-refresh');
    expect(save).toHaveBeenCalledTimes(1);
    // second call uses new token
    expect(mockRequestUrl).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: 'Bearer new-token' }),
      })
    );
  });

  test('throws when refresh returns non-200', async () => {
    settings.tokenExpiresAt = 0;
    mockRequestUrl.mockResolvedValue(mockResp(401, { error: 'unauthorized' }));
    await expect(client.listRepos()).rejects.toThrow('Token refresh failed');
  });

  test('throws when no refresh token and access token expired', async () => {
    settings.tokenExpiresAt = 0;
    settings.refreshToken = '';
    await expect(client.listRepos()).rejects.toThrow('Not authenticated');
    expect(mockRequestUrl).not.toHaveBeenCalled();
  });

  test('throws with server error message on 4xx', async () => {
    mockRequestUrl
      .mockResolvedValueOnce(mockResp(200, []))  // listRepos itself is fresh token path — skip
      .mockResolvedValueOnce(mockResp(403, { error: 'forbidden' }));

    // Use a fresh client where token is valid so we skip refresh
    const c2 = new BackendClient(makeSettings(), save);
    mockRequestUrl.mockReset();
    mockRequestUrl.mockResolvedValue(mockResp(403, { error: 'forbidden' }));
    await expect(c2.getMe()).rejects.toThrow('forbidden');
  });

  test('exchangeToken posts code and verifier without auth header', async () => {
    mockRequestUrl.mockResolvedValue(
      mockResp(200, { access_token: 'tok', refresh_token: 'ref', expires_in: 3600 })
    );
    const result = await client.exchangeToken('auth-code', 'verifier');
    expect(result.access_token).toBe('tok');
    expect(mockRequestUrl).toHaveBeenCalledWith(
      expect.objectContaining({
        url: 'http://localhost:8080/auth/token',
        method: 'POST',
        body: JSON.stringify({ code: 'auth-code', code_verifier: 'verifier' }),
      })
    );
    // no Authorization header on exchange
    const call = mockRequestUrl.mock.calls[0][0];
    expect(call.headers?.Authorization).toBeUndefined();
  });

  test('exchangeToken throws when exchange returns non-200', async () => {
    mockRequestUrl.mockResolvedValue(mockResp(401, { error: 'bad code' }));
    await expect(client.exchangeToken('x', 'y')).rejects.toThrow('Token exchange failed');
  });

  test('applyTokens updates settings expiry relative to now', () => {
    const before = Math.floor(Date.now() / 1000);
    client.applyTokens({ access_token: 'a', refresh_token: 'r', expires_in: 900 });
    expect(settings.accessToken).toBe('a');
    expect(settings.refreshToken).toBe('r');
    expect(settings.tokenExpiresAt).toBeGreaterThanOrEqual(before + 900);
    expect(settings.tokenExpiresAt).toBeLessThanOrEqual(before + 901);
  });

  test('listFiles calls GET /api/repos/:id/files with auth header', async () => {
    const entries = [
      { path: 'notes/foo.md', content: '# Foo', sha: 'abc123' },
      { path: 'notes/bar.md', content: '# Bar', sha: 'def456' },
    ];
    mockRequestUrl.mockResolvedValue(mockResp(200, entries));

    const result = await client.listFiles('repo-1');

    expect(mockRequestUrl).toHaveBeenCalledWith(
      expect.objectContaining({
        url: 'http://localhost:8080/api/repos/repo-1/files',
        headers: expect.objectContaining({ Authorization: 'Bearer access-token' }),
      })
    );
    expect(result).toHaveLength(2);
    expect(result[0].path).toBe('notes/foo.md');
    expect(result[0].sha).toBe('abc123');
  });
});
