// Client mirrors of Go's netip.ParseAddr / ParsePrefix, enough to point at the bad line;
// the server stays the authority.

function parseV4(text: string): number[] | null {
  const parts = text.split(".");
  if (parts.length !== 4) return null;
  const bytes = parts.map((part) => (/^(0|[1-9]\d{0,2})$/.test(part) ? Number(part) : 256));
  return bytes.every((byte) => byte <= 255) ? bytes : null;
}

function parseV6(text: string): number[] | null {
  if (text.includes("%")) return null;
  let tail: number[] = [];
  const lastColon = text.lastIndexOf(":");
  if (text.slice(lastColon + 1).includes(".")) {
    const v4 = parseV4(text.slice(lastColon + 1));
    if (!v4) return null;
    tail = v4;
    const body = text.slice(0, lastColon + 1);
    text = body.endsWith("::") ? body : body.slice(0, -1);
  }
  const halves = text.split("::");
  if (halves.length > 2) return null;
  const groups = (half: string) => (half === "" ? [] : half.split(":"));
  const head = groups(halves[0]);
  const rest = halves.length === 2 ? groups(halves[1]) : [];
  const words = head.length + rest.length + tail.length / 2;
  if (halves.length === 1 ? words !== 8 : words > 7) return null;
  const all = [...head, ...Array<string>(8 - words).fill("0"), ...rest];
  const bytes: number[] = [];
  for (const group of all) {
    if (!/^[0-9a-fA-F]{1,4}$/.test(group)) return null;
    const value = parseInt(group, 16);
    bytes.push(value >> 8, value & 255);
  }
  return [...bytes, ...tail];
}

function parseAddr(text: string): number[] | null {
  return text.includes(":") ? parseV6(text) : parseV4(text);
}

function parsePrefix(text: string): { bytes: number[]; bits: number } | null {
  const slash = text.indexOf("/");
  if (slash < 0) return null;
  const bytes = parseAddr(text.slice(0, slash));
  const bitsText = text.slice(slash + 1);
  if (!bytes || !/^(0|[1-9]\d{0,2})$/.test(bitsText)) return null;
  const bits = Number(bitsText);
  return bits <= bytes.length * 8 ? { bytes, bits } : null;
}

/** A CIDR whose host bits are zero and that is narrower than /0 (trusted proxies, admin allowlist). */
export function isMaskedPrefix(text: string): boolean {
  const prefix = parsePrefix(text);
  if (!prefix || prefix.bits === 0) return false;
  return prefix.bytes.every((byte, index) => {
    const keep = Math.max(0, Math.min(8, prefix.bits - index * 8));
    return (byte & (0xff >> keep)) === 0;
  });
}

/** A bare address or any prefix (rate-limit exclusions). */
export function isAddrOrPrefix(text: string): boolean {
  return parseAddr(text) !== null || parsePrefix(text) !== null;
}

/** One entry per non-empty line, trimmed. */
export function lines(text: string): string[] {
  return text.split("\n").map((line) => line.trim()).filter(Boolean);
}
