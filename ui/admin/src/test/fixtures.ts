export function activeSessionExpiresAt(): string {
  return new Date(Date.now() + 60 * 60 * 1000).toISOString();
}
