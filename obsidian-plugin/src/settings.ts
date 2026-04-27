export interface FolderRepo {
  folderPath: string;
  remoteUrl: string;
  username?: string;
  pat?: string;
  branch?: string;
}

export interface PubObsSettings {
  defaultUsername: string;
  defaultPat: string;
  defaultBranch: string;
  autoSync: boolean;
  repos: FolderRepo[];
}

export const DEFAULT_SETTINGS: PubObsSettings = {
  defaultUsername: '',
  defaultPat: '',
  defaultBranch: 'main',
  autoSync: false,
  repos: [],
};

import { Plugin } from 'obsidian';

export class SettingsManager {
  settings: PubObsSettings = { ...DEFAULT_SETTINGS, repos: [] };

  constructor(private plugin: Plugin) {}

  async load(): Promise<void> {
    const saved = await this.plugin.loadData();
    Object.assign(this.settings, DEFAULT_SETTINGS, saved ?? {});
  }

  async save(): Promise<void> {
    await this.plugin.saveData(this.settings);
  }

  async update(changes: Partial<PubObsSettings>): Promise<void> {
    Object.assign(this.settings, changes);
    await this.save();
  }
}
