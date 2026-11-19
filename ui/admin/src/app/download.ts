import { stringify } from "lossless-json";

/** Saves `value` as a pretty-printed JSON file (exact numbers kept). */
export function downloadJson(filename: string, value: unknown): void {
  downloadBlob(filename, new Blob([stringify(value, null, 2) ?? "null"], { type: "application/json" }));
}

export function downloadBlob(filename: string, blob: Blob): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  // Safari starts the download after click() returns; revoking at once can cancel it.
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
}
