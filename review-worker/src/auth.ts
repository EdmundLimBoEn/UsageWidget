export function bearerToken(header: string | null): string {
  if (header == null) {
    return "";
  }
  const prefix = "Bearer ";
  return header.startsWith(prefix) ? header.slice(prefix.length) : header;
}

export async function tokensEqual(provided: string, expected: string): Promise<boolean> {
  const encoder = new TextEncoder();
  const [providedHash, expectedHash] = await Promise.all([
    crypto.subtle.digest("SHA-256", encoder.encode(provided)),
    crypto.subtle.digest("SHA-256", encoder.encode(expected)),
  ]);
  return crypto.subtle.timingSafeEqual(providedHash, expectedHash);
}
