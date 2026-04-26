export interface App {
  vault: any;
  workspace: any;
  plugins: any;
  version: string;
}

export class TFile {
  path = '';
  name = '';
  extension = '';
  basename = '';
  parent: any = null;
}

export class Plugin {
  app: any;
  manifest: any;
  constructor(app: any, manifest: any) {
    this.app = app;
    this.manifest = manifest;
  }
  async loadData(): Promise<any> { return {}; }
  async saveData(_data: any): Promise<void> {}
  addCommand(_cmd: any): void {}
  addSettingTab(_tab: any): void {}
  registerEvent(_ref: any): void {}
}

export class PluginSettingTab {
  app: any;
  plugin: any;
  containerEl: any = {
    empty: () => {},
    createEl: (_tag: string, _opts?: any) => ({ setText: () => {}, createEl: () => ({}) }),
  };
  constructor(app: any, plugin: any) {
    this.app = app;
    this.plugin = plugin;
  }
  display(): void {}
  hide(): void {}
}

export class Setting {
  constructor(_el: any) {}
  setName(_n: string): this { return this; }
  setDesc(_d: string): this { return this; }
  addText(_cb: any): this { return this; }
  addToggle(_cb: any): this { return this; }
  addButton(_cb: any): this { return this; }
  addExtraButton(_cb: any): this { return this; }
}

export class Notice {
  constructor(_msg: string, _timeout?: number) {}
}

export function normalizePath(path: string): string {
  return path;
}
