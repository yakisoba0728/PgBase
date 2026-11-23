import { mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vitest/config";
import react from "@vitejs/plugin-react";
import subsetFont from "subset-font";

const root = dirname(fileURLToPath(import.meta.url));
const pretendard = join(root, "node_modules/pretendard/dist/web/variable");
const uiFontCss = join(root, "src/styles/pretendard-ui.css");

/** Every source file whose text can reach the screen: the app and its dictionaries, without tests. */
async function sourceFiles(dir: string): Promise<string[]> {
  const files: string[] = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) files.push(...(await sourceFiles(path)));
    else if (/\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)) files.push(path);
  }
  return files;
}

/** "U+20-7e, U+b7, …" for sorted code points. */
function unicodeRange(points: number[]): string {
  const ranges: string[] = [];
  for (let i = 0; i < points.length; i++) {
    const start = points[i];
    while (points[i + 1] === points[i] + 1) i++;
    ranges.push(start === points[i] ? `U+${start.toString(16)}` : `U+${start.toString(16)}-${points[i].toString(16)}`);
  }
  return ranges.join(", ");
}

/** The interface face: the subset font is written under node_modules/.cache and the CSS points at it, so Vite emits it as a hashed asset. */
async function interfaceFontCss(): Promise<string> {
  // The code points the published font covers, read from the dynamic subset's ranges.
  const subsetCss = await readFile(join(pretendard, "pretendardvariable-dynamic-subset.css"), "utf8");
  const covered = [...subsetCss.matchAll(/unicode-range:([^;]+);/g)].flatMap((match) =>
    match[1].split(",").map((range) => range.trim().slice(2).split("-").map((hex) => parseInt(hex, 16))),
  );
  const inFont = (point: number) => covered.some(([start, end = start]) => point >= start && point <= end);
  const points = new Set<number>();
  for (let point = 0x20; point < 0x7f; point++) points.add(point);
  for (const file of [...(await sourceFiles(join(root, "src"))), join(root, "index.html")]) {
    for (const char of await readFile(file, "utf8")) points.add(char.codePointAt(0)!);
  }
  // Control characters (line endings differ by checkout) never render.
  const sorted = [...points].filter((point) => point >= 0x20 && !(point >= 0x7f && point < 0xa0) && inFont(point)).sort((a, b) => a - b);
  const font = await subsetFont(await readFile(join(pretendard, "woff2/PretendardVariable.woff2")), String.fromCodePoint(...sorted), { targetFormat: "woff2" });
  const out = join(root, "node_modules/.cache/pretendard-ui/PretendardVariable.ui.woff2");
  await mkdir(dirname(out), { recursive: true });
  await writeFile(out, font);
  const url = relative(dirname(uiFontCss), out).replaceAll("\\", "/");
  return `@font-face {
  font-family: "Pretendard Variable";
  font-style: normal;
  font-display: swap;
  font-weight: 45 920;
  src: url("${url}") format("woff2-variations");
  unicode-range: ${unicodeRange(sorted)};
}
`;
}

/**
 * Pretendard's dynamic subset splits Hangul by frequency into 92 files, so the interface's own text pulls in dozens
 * of them. At build time this cuts one more face from the full variable font holding exactly the characters in the
 * source (Latin, Hangul, punctuation), declared after the dynamic subset: the last matching face is tried first, so
 * the interface needs this one file, and only characters outside it (record data) fall through to the subsets.
 * Development keeps the plain dynamic subset (pretendard-ui.css stays a comment).
 */
function pretendardInterfaceSubset(): Plugin {
  return {
    name: "pretendard-interface-subset",
    apply: "build",
    enforce: "pre",
    load: {
      filter: { id: /pretendard-ui\.css$/ },
      handler: (id) => (resolve(id) === uiFontCss ? interfaceFontCss() : null),
    },
  };
}

export default defineConfig({
  base: "/admin/",
  plugins: [react(), pretendardInterfaceSubset()],
  // font-src and img-src forbid inlined data: fonts, so every asset is emitted as a file.
  build: { assetsInlineLimit: 0 },
  server: {
    proxy: {
      "/api": "http://localhost:8090",
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: "./src/test/setup.ts",
    globals: true,
  },
});
