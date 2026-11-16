// Mirrors the JSON emitted by internal/shares (Go).

export type SmbAccess = "public" | "readonly" | "private";

export interface ProtoStatus {
  state: "off" | "active" | "error";
  message?: string;
}

export interface SmbConfig {
  enabled: boolean;
  access: SmbAccess;
  users: string[];
}

export interface NfsConfig {
  enabled: boolean;
  clients: string;
  readOnly: boolean;
}

export interface ShareInput {
  name: string;
  comment: string;
  smb: SmbConfig;
  nfs: NfsConfig;
}

export interface Share extends ShareInput {
  createdAt: string;
  path: string;
  smbStatus: ProtoStatus;
  nfsStatus: ProtoStatus;
}

export interface ServiceStatus {
  installed: boolean;
  active: boolean;
  canInstall: boolean;
  error?: string;
}

export interface Overview {
  shares: Share[];
  root: string;
  users: string[];
  services: {
    privileged: boolean;
    smb: ServiceStatus;
    nfs: ServiceStatus;
  };
  suggestedClients: string;
}

export const ACCESS_LABEL: Record<SmbAccess, string> = {
  public: "Public",
  readonly: "Public, read-only",
  private: "Private",
};
