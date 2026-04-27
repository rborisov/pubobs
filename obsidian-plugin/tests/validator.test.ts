import * as fsp from 'fs/promises';
import { EnvironmentValidator } from '../src/validator';

jest.mock('fs/promises');
const mockReadFile = fsp.readFile as jest.MockedFunction<typeof fsp.readFile>;

function makeApp(opts: {
  version?: string;
  plugins?: Record<string, { manifest: { version: string } }>;
} = {}): any {
  return {
    vault: { adapter: { basePath: '/vault' } },
    plugins: { plugins: opts.plugins ?? {} },
    version: opts.version ?? '1.4.0',
  };
}

const VALID_MANIFEST = {
  minObsidianVersion: '1.4.0',
  requiredPlugins: [{ id: 'dataview', minVersion: '0.5.55' }],
  snapshotFormat: '1.0',
};

describe('EnvironmentValidator', () => {
  beforeEach(() => jest.clearAllMocks());

  it('passes for a valid environment', async () => {
    mockReadFile.mockResolvedValue(JSON.stringify(VALID_MANIFEST) as any);
    const v = new EnvironmentValidator(makeApp({
      plugins: { dataview: { manifest: { version: '0.5.55' } } },
    }));
    const result = await v.check('notes');
    expect(result.valid).toBe(true);
    expect(result.errors).toHaveLength(0);
  });

  it('returns missing-manifest error when workspace.json not found', async () => {
    mockReadFile.mockRejectedValue(Object.assign(new Error('ENOENT'), { code: 'ENOENT' }));
    const v = new EnvironmentValidator(makeApp());
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    expect(result.errors[0].type).toBe('missing-manifest');
    expect(result.errors[0].message).toContain('workspace.json');
  });

  it('returns obsidian-version error when app version is too old', async () => {
    mockReadFile.mockResolvedValue(JSON.stringify(VALID_MANIFEST) as any);
    const v = new EnvironmentValidator(makeApp({
      version: '1.3.2',
      plugins: { dataview: { manifest: { version: '0.5.55' } } },
    }));
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    expect(result.errors[0].type).toBe('obsidian-version');
    expect(result.errors[0].message).toContain('1.3.2');
    expect(result.errors[0].message).toContain('1.4.0');
  });

  it('returns plugin-missing error when required plugin is not installed', async () => {
    mockReadFile.mockResolvedValue(JSON.stringify(VALID_MANIFEST) as any);
    const v = new EnvironmentValidator(makeApp({ plugins: {} }));
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    expect(result.errors[0].type).toBe('plugin-missing');
    expect(result.errors[0].message).toContain('dataview');
  });

  it('returns plugin-version error when plugin version is too old', async () => {
    mockReadFile.mockResolvedValue(JSON.stringify(VALID_MANIFEST) as any);
    const v = new EnvironmentValidator(makeApp({
      plugins: { dataview: { manifest: { version: '0.4.12' } } },
    }));
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    expect(result.errors[0].type).toBe('plugin-version');
    expect(result.errors[0].message).toContain('0.4.12');
    expect(result.errors[0].message).toContain('0.5.55');
  });

  it('reports all failures, not just the first', async () => {
    const manifest = {
      minObsidianVersion: '1.4.0',
      requiredPlugins: [
        { id: 'dataview', minVersion: '0.5.55' },
        { id: 'templater-obsidian', minVersion: '2.0.0' },
      ],
      snapshotFormat: '1.0',
    };
    mockReadFile.mockResolvedValue(JSON.stringify(manifest) as any);
    const v = new EnvironmentValidator(makeApp({ version: '1.3.0', plugins: {} }));
    const result = await v.check('notes');
    expect(result.valid).toBe(false);
    // version error + 2 missing plugin errors
    expect(result.errors.length).toBeGreaterThanOrEqual(3);
  });
});
