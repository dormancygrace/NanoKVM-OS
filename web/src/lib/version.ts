// Keep SemVer in API/package data; format release labels only for display.
export function formatVersion(version: string): string {
  return version.replace(/-beta\.(\d+)(?=$|[.+-])/, ' beta-$1');
}
