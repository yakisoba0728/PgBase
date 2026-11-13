import { useEffect, useRef, useState, type ReactNode } from "react";
import { EditorContent, useEditor, type Editor } from "@tiptap/react";
import { DOMSerializer } from "@tiptap/pm/model";
import StarterKit from "@tiptap/starter-kit";
import Image from "@tiptap/extension-image";
import { TableKit } from "@tiptap/extension-table";
import {
  RiArrowGoBackLine,
  RiArrowGoForwardLine,
  RiBold,
  RiCodeBoxLine,
  RiCodeSSlashLine,
  RiDeleteBinLine,
  RiDoubleQuotesL,
  RiH2,
  RiImageLine,
  RiInsertColumnRight,
  RiInsertRowBottom,
  RiItalic,
  RiLink,
  RiListOrdered,
  RiListUnordered,
  RiStrikethrough,
  RiTable2,
  RiUnderline,
  type RemixiconComponentType,
} from "@remixicon/react";
import { Button } from "../components/Button";
import { Notice } from "../components/Feedback";
import { useT } from "../i18n";
import "../styles/richtext.css";

interface RichTextProps {
  id: string;
  label: string;
  value: string;
  onChange: (html: string) => void;
  readOnly: boolean;
  invalid: boolean;
  /** Field option: same-origin links and images are stored as paths. */
  convertURLs?: boolean;
}

/** Same-origin absolute URLs become paths; other origins stay as they are. */
function portableHTML(html: string, origin: string): string {
  return html.replace(/\b(href|src)="([^"]*)"/g, (match, attribute: string, url: string) => {
    try {
      const parsed = new URL(url);
      return parsed.origin === origin ? `${attribute}="${parsed.pathname}${parsed.search}${parsed.hash}"` : match;
    } catch {
      return match;
    }
  });
}

// editor.getHTML() builds DOM nodes, and setting a style attribute on one (tables carry min-width and
// column widths) is a CSP violation even off-screen. The specs are rendered with the attribute renamed,
// then renamed back in the HTML text: same output, no inline style ever applied.
const styleStandIn = "data-pgbase-style";
type Spec = ReturnType<DOMSerializer["nodes"][string]>;

function withoutStyle(spec: Spec): Spec {
  if (!Array.isArray(spec)) return spec;
  return spec.map((part, index) => {
    if (index === 0) return part;
    if (Array.isArray(part)) return withoutStyle(part as unknown as Spec);
    if (part && typeof part === "object" && !(part instanceof Node) && "style" in part) {
      const { style, ...rest } = part as Record<string, unknown>;
      return { ...rest, [styleStandIn]: style };
    }
    return part;
  }) as unknown as Spec;
}

// Tiptap parses content with DOMParser, which also reports style attributes as violations; the editor
// recomputes the only styles it keeps (table widths), so incoming ones are renamed before parsing.
const forEditor = (html: string) => html.replace(/(<[a-z][^<>]*?\s)style(\s*=)/gi, `$1${styleStandIn}$2`);

const tagAlias: Record<string, string> = { B: "STRONG", I: "EM", STRIKE: "S", DEL: "S" };

/** True when every element, attribute and text of `original` survives in `roundTrip`: the editor may add markup, never drop it. */
export function keepsMarkup(original: string, roundTrip: string): boolean {
  const parse = (html: string) => new DOMParser().parseFromString(forEditor(html), "text/html").body;
  const inventory = (root: HTMLElement) => {
    const tags = new Map<string, number>();
    const attributes = new Set<string>();
    for (const element of root.querySelectorAll("*")) {
      const tag = tagAlias[element.tagName] ?? element.tagName;
      tags.set(tag, (tags.get(tag) ?? 0) + 1);
      for (const attribute of element.attributes) attributes.add(`${tag} ${attribute.name}=${attribute.value}`);
    }
    return { text: (root.textContent ?? "").replace(/\s/g, ""), tags, attributes };
  };
  const before = inventory(parse(original));
  const after = inventory(parse(roundTrip));
  return before.text === after.text && [...before.tags].every(([tag, count]) => (after.tags.get(tag) ?? 0) >= count) && [...before.attributes].every((item) => after.attributes.has(item));
}

let serializer: { schema: Editor["schema"]; value: DOMSerializer } | null = null;
function serialize(editor: Editor): string {
  if (serializer?.schema !== editor.schema) {
    const base = DOMSerializer.fromSchema(editor.schema);
    const wrap = (table: Record<string, (...args: never[]) => Spec>) => Object.fromEntries(Object.entries(table).map(([name, render]) => [name, (...args: never[]) => withoutStyle(render(...args))]));
    serializer = { schema: editor.schema, value: new DOMSerializer(wrap(base.nodes) as DOMSerializer["nodes"], wrap(base.marks) as DOMSerializer["marks"]) };
  }
  const container = document.createElement("div");
  container.appendChild(serializer.value.serializeFragment(editor.state.doc.content));
  return container.innerHTML.replaceAll(` ${styleStandIn}="`, ' style="');
}

const safeURL = (text: string, image: boolean) => {
  try {
    const url = new URL(text, location.href);
    if (image && url.origin !== location.origin) return null;
    return ["http:", "https:", ...(image ? [] : ["mailto:", "tel:"])].includes(url.protocol) ? text : null;
  } catch {
    return null;
  }
};

/** HTML rich-text field: a formatting toolbar over Tiptap, with a raw HTML source mode. Lazy-loaded. */
export default function RichText({ id, label, value, onChange, readOnly, invalid, convertURLs }: RichTextProps) {
  const t = useT();
  const [source, setSource] = useState(false);
  const [urlFor, setUrlFor] = useState<"link" | "image" | null>(null);
  const [url, setUrl] = useState("");
  // The stored HTML holds markup the visual editor would drop: it opens as source until the user accepts the loss.
  const [lossy, setLossy] = useState(false);
  const check = (current: Editor, html: string) => {
    const loses = html !== "" && !keepsMarkup(html, serialize(current));
    setLossy(loses);
    if (loses) setSource(true);
  };
  // The HTML this editor last produced: a value equal to it is our own echo, anything else came from outside.
  const emitted = useRef(value);
  const touched = useRef(false);
  const touch = () => {
    touched.current = true;
  };
  const editor = useEditor({
    extensions: [StarterKit.configure({ link: { openOnClick: false } }), Image, TableKit.configure({ table: { resizable: true } })],
    content: forEditor(value),
    editable: !readOnly,
    // The CSP forbids injected <style>; the ProseMirror rules ship in richtext.css instead.
    injectCSS: false,
    shouldRerenderOnTransaction: true,
    editorProps: { attributes: { id, role: "textbox", "aria-multiline": "true", "aria-label": label, class: "rich-text-doc" } },
    onCreate: ({ editor: current }) => check(current, value),
    onFocus: touch,
    onUpdate: ({ editor: current }) => {
      // Plugins normalise some documents on load (e.g. table fixes); only edits made by the user count.
      if (!touched.current) return;
      const html = current.isEmpty ? "" : serialize(current);
      emitted.current = convertURLs ? portableHTML(html, location.origin) : html;
      onChange(emitted.current);
    },
  });

  // Opening a document must not mark it changed: outside values are loaded without an update event.
  useEffect(() => {
    if (editor && value !== emitted.current) {
      emitted.current = value;
      editor.commands.setContent(forEditor(value), { emitUpdate: false });
      check(editor, value);
    }
  }, [editor, value]);
  useEffect(() => editor?.setEditable(!readOnly, false), [editor, readOnly]);
  useEffect(() => {
    editor?.setOptions({ editorProps: { attributes: { id, role: "textbox", "aria-multiline": "true", "aria-label": label, "aria-invalid": String(invalid), class: "rich-text-doc" } } });
  }, [editor, id, label, invalid]);

  const applyUrl = () => {
    if (!editor || !urlFor) return;
    const checked = safeURL(url.trim(), urlFor === "image");
    if (!checked) return;
    if (urlFor === "image") editor.chain().focus().setImage({ src: checked, alt: "" }).run();
    else editor.chain().focus().extendMarkRange("link").setLink({ href: checked }).run();
    setUrl("");
    setUrlFor(null);
  };

  return (
    // Any pointer or key use of the field (toolbar included) counts as the user editing; Tiptap focuses lazily.
    <div className="rich-text" data-invalid={invalid ? "" : undefined} data-disabled={readOnly ? "" : undefined} onPointerDownCapture={touch} onKeyDownCapture={touch}>
      <div className="rich-text-bar" role="toolbar" aria-label={t("editor.toolbar", { field: label })}>
        {!source && editor && <Tools editor={editor} disabled={readOnly} onUrl={(kind) => setUrlFor(urlFor === kind ? null : kind)} />}
        <button type="button" className="rt-btn rt-source" aria-pressed={source} disabled={lossy && source} onClick={() => setSource(!source)} aria-label={t("editor.source")} title={t("editor.source")}>
          <RiCodeSSlashLine size={16} aria-hidden />
          <span>HTML</span>
        </button>
      </div>
      {lossy && (
        <Notice title={t("editor.lossyTitle")} actions={source && !readOnly ? <Button size="sm" variant="secondary" onClick={() => setSource(false)}>{t("editor.lossyVisual")}</Button> : undefined}>
          {t(source ? "editor.lossyText" : "editor.lossyVisualText")}
        </Notice>
      )}
      {urlFor && !source && (
        <div className="rich-text-url">
          <input
            className="field-control"
            autoFocus
            value={url}
            placeholder={urlFor === "image" ? "https://…/image.png" : "https://…"}
            aria-label={urlFor === "image" ? t("editor.imageUrl") : t("editor.linkUrl")}
            aria-invalid={url.trim() !== "" && !safeURL(url.trim(), urlFor === "image") ? true : undefined}
            onChange={(event) => setUrl(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                applyUrl();
              } else if (event.key === "Escape") {
                event.preventDefault();
                setUrlFor(null);
              }
            }}
          />
          <Button size="sm" variant="secondary" onClick={applyUrl} disabled={!safeURL(url.trim(), urlFor === "image")}>{t("editor.apply")}</Button>
          {urlFor === "link" && editor?.isActive("link") && (
            <Button size="sm" variant="transparent" onClick={() => { editor.chain().focus().unsetLink().run(); setUrlFor(null); }}>{t("editor.unlink")}</Button>
          )}
        </div>
      )}
      {source ? (
        <textarea id={id} className="field-control mono rich-text-source" aria-label={label} aria-invalid={invalid || undefined} rows={12} spellCheck={false} value={value} disabled={readOnly} onChange={(event) => onChange(event.target.value)} />
      ) : (
        <EditorContent editor={editor} className="rich-text-body" />
      )}
    </div>
  );
}

function Tools({ editor, disabled, onUrl }: { editor: Editor; disabled: boolean; onUrl: (kind: "link" | "image") => void }) {
  const t = useT();
  const chain = () => editor.chain().focus();
  const tool = (key: string, icon: RemixiconComponentType, label: string, run: () => void, active = false, enabled = true): ReactNode => {
    const Icon = icon;
    return (
      <button key={key} type="button" className="rt-btn" aria-label={label} title={label} aria-pressed={active} disabled={disabled || !enabled} onMouseDown={(event) => event.preventDefault()} onClick={run}>
        <Icon size={16} aria-hidden />
      </button>
    );
  };
  const inTable = editor.isActive("table");
  return (
    <>
      {tool("b", RiBold, t("editor.bold"), () => chain().toggleBold().run(), editor.isActive("bold"))}
      {tool("i", RiItalic, t("editor.italic"), () => chain().toggleItalic().run(), editor.isActive("italic"))}
      {tool("u", RiUnderline, t("editor.underline"), () => chain().toggleUnderline().run(), editor.isActive("underline"))}
      {tool("s", RiStrikethrough, t("editor.strike"), () => chain().toggleStrike().run(), editor.isActive("strike"))}
      <span className="rt-sep" aria-hidden />
      {tool("h", RiH2, t("editor.heading"), () => chain().toggleHeading({ level: 2 }).run(), editor.isActive("heading", { level: 2 }))}
      {tool("ul", RiListUnordered, t("editor.bullets"), () => chain().toggleBulletList().run(), editor.isActive("bulletList"))}
      {tool("ol", RiListOrdered, t("editor.numbers"), () => chain().toggleOrderedList().run(), editor.isActive("orderedList"))}
      {tool("q", RiDoubleQuotesL, t("editor.quote"), () => chain().toggleBlockquote().run(), editor.isActive("blockquote"))}
      {tool("code", RiCodeBoxLine, t("editor.code"), () => chain().toggleCodeBlock().run(), editor.isActive("codeBlock"))}
      <span className="rt-sep" aria-hidden />
      {tool("link", RiLink, t("editor.link"), () => onUrl("link"), editor.isActive("link"))}
      {tool("img", RiImageLine, t("editor.image"), () => onUrl("image"))}
      {tool("table", RiTable2, t("editor.table"), () => chain().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run(), false, !inTable)}
      {inTable && tool("row", RiInsertRowBottom, t("editor.addRow"), () => chain().addRowAfter().run())}
      {inTable && tool("col", RiInsertColumnRight, t("editor.addColumn"), () => chain().addColumnAfter().run())}
      {inTable && tool("deltable", RiDeleteBinLine, t("editor.deleteTable"), () => chain().deleteTable().run())}
      <span className="rt-sep" aria-hidden />
      {tool("undo", RiArrowGoBackLine, t("editor.undo"), () => chain().undo().run(), false, editor.can().undo())}
      {tool("redo", RiArrowGoForwardLine, t("editor.redo"), () => chain().redo().run(), false, editor.can().redo())}
    </>
  );
}
