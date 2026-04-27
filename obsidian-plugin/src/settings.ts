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
