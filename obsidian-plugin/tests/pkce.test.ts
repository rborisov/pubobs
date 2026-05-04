import { generateVerifier, computeChallenge, generateState } from '../src/pkce';

describe('pkce', () => {
  test('generateVerifier returns 43-char base64url string', () => {
    const v = generateVerifier();
    expect(v).toMatch(/^[A-Za-z0-9\-_]+$/);
    expect(v.length).toBe(43);
  });

  test('computeChallenge returns base64url SHA-256 of verifier', async () => {
    // RFC 7636 test vector
    const verifier = 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk';
    const challenge = await computeChallenge(verifier);
    expect(challenge).toBe('E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM');
  });

  test('generateState returns non-empty base64url string', () => {
    const s = generateState();
    expect(s.length).toBeGreaterThan(0);
    expect(s).toMatch(/^[A-Za-z0-9\-_]+$/);
  });

  test('two calls produce different verifiers', () => {
    expect(generateVerifier()).not.toBe(generateVerifier());
  });
});
