import { useEffect, useRef, useState } from "react";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import Image from "@tiptap/extension-image";
import { TableKit } from "@tiptap/extension-table";

interface Props {
  id: string; label: string; value: string; disabled: boolean;
  describedBy?: string; invalid?: boolean; convertURLs?: boolean;
  onChange(value: string): void;
}

/** Match the field's portable-link option without rewriting external origins. */
export function portableHTML(html: string, base: string): string {
  const document = new DOMParser().parseFromString(html, "text/html");
  for (const node of document.querySelectorAll("[href], [src]")) {
    for (const attribute of ["href", "src"]) {
      const value = node.getAttribute(attribute);
      if (!value || !/^(https?:)?\/\//i.test(value)) continue;
      try {
        const url = new URL(value, base);
        if (url.origin === new URL(base).origin) node.setAttribute(attribute, url.pathname + url.search + url.hash);
      } catch { /* Invalid URLs remain text in the source editor. */ }
    }
  }
  return document.body.innerHTML;
}

export function RichTextField({ id, label, value, disabled, describedBy, invalid, convertURLs, onChange }: Props) {
  const [source, setSource] = useState(false);
  const [url, setURL] = useState("");
  const [urlError, setURLError] = useState("");
  const emitted = useRef(value);
  const editor = useEditor({
    extensions: [StarterKit.configure({ link: { openOnClick: false } }), Image, TableKit],
    content: value, editable: !disabled, shouldRerenderOnTransaction: true,
    editorProps: { attributes: { id, role: "textbox", "aria-label": label, "aria-multiline": "true" } },
    onUpdate: ({ editor }) => {
      const html = editor.isEmpty ? "" : editor.getHTML();
      emitted.current = convertURLs ? portableHTML(html, location.href) : html;
      onChange(emitted.current);
    },
  });
  useEffect(() => {
    if (editor && value !== emitted.current) { editor.commands.setContent(value, { emitUpdate: false }); emitted.current = value; }
  }, [editor, value]);
  useEffect(() => { editor?.setEditable(!disabled, false); }, [disabled, editor]);
  useEffect(() => {
    editor?.setOptions({ editorProps: { attributes: { id, role: "textbox", "aria-label": label, "aria-multiline": "true", "aria-invalid": String(Boolean(invalid)), "aria-describedby": describedBy ?? "" } } });
  }, [describedBy, editor, id, invalid, label]);
  function insertURL(image: boolean) {
    if (!editor) return;
    try {
      const parsed = new URL(url, location.href);
      if (!["http:", "https:", ...(image ? [] : ["mailto:", "tel:"])].includes(parsed.protocol) || !url.trim()) throw new Error("url");
      const href = convertURLs && parsed.origin === location.origin ? parsed.pathname + parsed.search + parsed.hash : url;
      if (image) editor.chain().focus().setImage({ src: href, alt: "" }).run();
      else editor.chain().focus().extendMarkRange("link").setLink({ href }).run();
      setURL(""); setURLError("");
    } catch { setURLError("Enter a valid HTTP/HTTPS URL; links may also use mailto or tel."); }
  }
  return <div className="rich-text-field">
    <button type="button" disabled={disabled} aria-pressed={source} onClick={() => setSource(!source)}>HTML source</button>
    {source ? <><textarea id={id} aria-label={label} aria-describedby={describedBy} aria-invalid={invalid} disabled={disabled} rows={12} value={value} onChange={event => onChange(event.target.value)} /><p>Source is stored as entered. Visual editing uses supported document formatting.</p></> : <>
      <div role="toolbar" aria-label={`${label} formatting`}>
        {(["bold", "italic", "underline", "strike"] as const).map(mark => <button key={mark} type="button" disabled={disabled || !editor} aria-pressed={editor?.isActive(mark) ?? false} onClick={() => editor?.chain().focus().toggleMark(mark).run()}>{mark}</button>)}
        <button type="button" disabled={disabled} onClick={() => editor?.chain().focus().toggleHeading({ level: 2 }).run()}>Heading</button>
        <button type="button" disabled={disabled} onClick={() => editor?.chain().focus().toggleBulletList().run()}>Bullet list</button>
        <button type="button" disabled={disabled} onClick={() => editor?.chain().focus().toggleOrderedList().run()}>Numbered list</button>
        <button type="button" disabled={disabled} onClick={() => editor?.chain().focus().toggleBlockquote().run()}>Quote</button>
        <button type="button" disabled={disabled} onClick={() => editor?.chain().focus().toggleCodeBlock().run()}>Code block</button>
        <button type="button" disabled={disabled || !editor?.can().undo()} onClick={() => editor?.chain().focus().undo().run()}>Undo</button>
        <button type="button" disabled={disabled || !editor?.can().redo()} onClick={() => editor?.chain().focus().redo().run()}>Redo</button>
        <button type="button" disabled={disabled} onClick={() => editor?.chain().focus().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run()}>Insert table</button>
        {editor?.isActive("table") && <><button type="button" disabled={disabled} onClick={() => editor.chain().focus().addRowAfter().run()}>Add row</button><button type="button" disabled={disabled} onClick={() => editor.chain().focus().addColumnAfter().run()}>Add column</button><button type="button" disabled={disabled} onClick={() => editor.chain().focus().deleteTable().run()}>Remove table</button></>}
      </div>
      <div><label>{label} link or image URL<input disabled={disabled} type="text" value={url} onChange={event => setURL(event.target.value)} /></label><button type="button" disabled={disabled} onClick={() => insertURL(false)}>Set link</button><button type="button" disabled={disabled} onClick={() => editor?.chain().focus().unsetLink().run()}>Remove link</button><button type="button" disabled={disabled} onClick={() => insertURL(true)}>Insert image</button></div>
      {urlError && <p role="alert">{urlError}</p>}
      <EditorContent editor={editor} />
    </>}
  </div>;
}
