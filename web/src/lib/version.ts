export type VersionInfo = {
  current_version: string;
  latest_version?: string;
  has_update: boolean;
  install_command: string;
  source_update_command: string;
  release_url: string;
};

export async function fetchVersionInfo(secret: string): Promise<VersionInfo | null> {
  try {
    const response = await fetch("/api/admin/version", {
      headers: secret ? { Authorization: `Bearer ${secret}` } : {},
    });
    if (!response.ok) {
      return null;
    }
    return (await response.json()) as VersionInfo;
  } catch {
    return null;
  }
}
