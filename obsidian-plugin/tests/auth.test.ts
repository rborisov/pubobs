jest.mock('obsidian', () => ({}));

import { AuthFlow } from '../src/auth';
import type { BackendClient } from '../src/client';
import type { TokenResponse } from '../src/types';

function makeClient(result: TokenResponse | Error): jest.Mocked<Pick<BackendClient, 'exchangeToken' | 'applyTokens'>> {
  return {
    exchangeToken: jest.fn().mockImplementation(() =>
      result instanceof Error ? Promise.reject(result) : Promise.resolve(result)
    ),
    applyTokens: jest.fn(),
  };
}

const goodTokens: TokenResponse = { access_token: 'a', refresh_token: 'r', expires_in: 3600 };

describe('AuthFlow', () => {
  beforeEach(() => {
    (global as any).window = { open: jest.fn() };
  });

  test('handleCallback errors when no pending session', async () => {
    const flow = new AuthFlow(makeClient(goodTokens) as any, () => 'http://localhost:8080');
    const onError = jest.fn();
    await flow.handleCallback({ code: 'c', state: 's' }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('No pending auth session');
  });

  test('handleCallback errors on state mismatch', async () => {
    const flow = new AuthFlow(makeClient(goodTokens) as any, () => 'http://localhost:8080');
    await flow.beginAuth();
    const onError = jest.fn();
    await flow.handleCallback({ code: 'c', state: 'wrong' }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('State mismatch — possible CSRF');
  });

  test('handleCallback errors when code is missing', async () => {
    const flow = new AuthFlow(makeClient(goodTokens) as any, () => 'http://localhost:8080');
    await flow.beginAuth();
    const state = captureState();
    const onError = jest.fn();
    await flow.handleCallback({ state }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('Missing code in callback');
  });

  test('successful callback calls applyTokens and onSuccess', async () => {
    const client = makeClient(goodTokens);
    const flow = new AuthFlow(client as any, () => 'http://localhost:8080');
    await flow.beginAuth();
    const state = captureState();

    const onSuccess = jest.fn().mockResolvedValue(undefined);
    const onError = jest.fn();
    await flow.handleCallback({ code: 'auth-code', state }, onSuccess, onError);

    expect(client.exchangeToken).toHaveBeenCalledWith('auth-code', expect.any(String));
    expect(client.applyTokens).toHaveBeenCalledWith(goodTokens);
    expect(onSuccess).toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
  });

  test('pending is cleared after successful callback', async () => {
    const flow = new AuthFlow(makeClient(goodTokens) as any, () => 'http://localhost:8080');
    await flow.beginAuth();
    const state = captureState();

    await flow.handleCallback({ code: 'c', state }, async () => {}, jest.fn());

    // second callback with same state fails — pending was cleared
    const onError = jest.fn();
    await flow.handleCallback({ code: 'c', state }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('No pending auth session');
  });

  test('pending is cleared after state mismatch', async () => {
    const flow = new AuthFlow(makeClient(goodTokens) as any, () => 'http://localhost:8080');
    await flow.beginAuth();

    const onError = jest.fn();
    await flow.handleCallback({ code: 'c', state: 'bad' }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('State mismatch — possible CSRF');

    // session is gone — second attempt also fails
    onError.mockClear();
    await flow.handleCallback({ code: 'c', state: 'bad' }, async () => {}, onError);
    expect(onError).toHaveBeenCalledWith('No pending auth session');
  });

  test('exchange failure calls onError and does not call onSuccess', async () => {
    const client = makeClient(new Error('network error'));
    const flow = new AuthFlow(client as any, () => 'http://localhost:8080');
    await flow.beginAuth();
    const state = captureState();

    const onSuccess = jest.fn();
    const onError = jest.fn();
    await flow.handleCallback({ code: 'c', state }, onSuccess, onError);

    expect(onError).toHaveBeenCalledWith('network error');
    expect(onSuccess).not.toHaveBeenCalled();
  });

  test('beginAuth opens URL with correct params', async () => {
    const flow = new AuthFlow(makeClient(goodTokens) as any, () => 'http://localhost:8080');
    await flow.beginAuth();

    const url: string = (window.open as jest.Mock).mock.calls[0][0];
    expect(url).toContain('http://localhost:8080/auth/plugin');
    expect(url).toContain('code_challenge_method=S256');
    expect(url).toContain(encodeURIComponent('obsidian://pubobs-callback'));
    expect(url).toMatch(/code_challenge=[A-Za-z0-9\-_.~%]+/);
    expect(url).toMatch(/state=[A-Za-z0-9\-_.~%]+/);
  });
});

function captureState(): string {
  const url: string = (window.open as jest.Mock).mock.calls.at(-1)[0];
  const match = url.match(/state=([^&]+)/);
  return decodeURIComponent(match![1]);
}
