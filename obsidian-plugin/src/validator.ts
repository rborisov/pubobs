import * as fsp from 'fs/promises';
import * as path from 'path';
import { App } from 'obsidian';

export interface WorkspaceManifest {
  minObsidianVersion: string;
  requiredPlugins: Array<{ id: string; minVersion: string }>;
  snapshotFormat: string;
}

export interface ValidationError {
  type: 'missing-manifest' | 'obsidian-version' | 'plugin-missing' | 'plugin-version';
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}

export class EnvironmentValidator {
  constructor(private app: App) {}

  async check(folderPath: string): Promise<ValidationResult> {
    const vaultPath = (this.app.vault.adapter as any).basePath as string;
    const manifestPath = path.join(vaultPath, folderPath, 'workspace.json');

    let manifest: WorkspaceManifest;
    try {
      const raw = await fsp.readFile(manifestPath, 'utf-8');
      manifest = JSON.parse(raw) as WorkspaceManifest;
    } catch {
      return {
        valid: false,
        errors: [{
          type: 'missing-manifest',
          message: 'PubObs: workspace.json not found in repo root. Create it to enable sync.',
        }],
      };
    }

    const errors: ValidationError[] = [];
    const currentVersion: string =
      (this.app as any).version ??
      (this.app as any).vault?.adapter?.app?.version ??
      '';

    if (currentVersion && !semverGte(currentVersion, manifest.minObsidianVersion)) {
      errors.push({
        type: 'obsidian-version',
        message: `PubObs: Obsidian ${manifest.minObsidianVersion}+ required. You have ${currentVersion}. Please upgrade before syncing.`,
      });
    }

    const installedPlugins = (this.app as any).plugins.plugins as Record<string, { manifest: { version: string } }>;
    for (const required of manifest.requiredPlugins ?? []) {
      const installed = installedPlugins[required.id];
      if (!installed) {
        errors.push({
          type: 'plugin-missing',
          message: `PubObs: Plugin '${required.id}' is required but not installed.`,
        });
        continue;
      }
      if (!semverGte(installed.manifest.version, required.minVersion)) {
        errors.push({
          type: 'plugin-version',
          message: `PubObs: Plugin '${required.id}' ${required.minVersion}+ required. Installed: ${installed.manifest.version}. Please upgrade.`,
        });
      }
    }

    return { valid: errors.length === 0, errors };
  }
}

function semverGte(a: string, b: string): boolean {
  const pa = (a ?? '0').split('.').map(Number);
  const pb = (b ?? '0').split('.').map(Number);
  for (let i = 0; i < 3; i++) {
    if ((pa[i] ?? 0) > (pb[i] ?? 0)) return true;
    if ((pa[i] ?? 0) < (pb[i] ?? 0)) return false;
  }
  return true;
}
