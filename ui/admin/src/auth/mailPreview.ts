// Renders the server's mail preview inside the admin page without breaking its CSP
// (style-src 'self', img-src 'self' data:, no frames): the HTML is parsed inertly,
// rebuilt from an allowlist of tags and attributes, and styling is re-applied through
// CSSOM (which CSP permits) from the <style> rules and style attributes. Scripts,
// event handlers, forms, classes, ids and remote resources never reach the page.

const keptTags = new Set([
  "a", "abbr", "b", "blockquote", "br", "caption", "center", "code", "col", "colgroup", "dd", "div", "dl", "dt", "em", "font",
  "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img", "li", "main", "ol", "p", "pre", "s", "section", "small", "span",
  "strong", "sub", "sup", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "u", "ul", "article", "header", "footer",
]);
// Dropped with everything inside; other unknown tags are unwrapped (their children kept).
const droppedTags = new Set([
  "script", "style", "mail-style", "template", "noscript", "iframe", "frame", "frameset", "object", "embed", "applet", "link", "meta",
  "title", "head", "base", "form", "input", "button", "select", "textarea", "option", "svg", "math", "video", "audio", "picture", "source", "canvas",
]);
// Layout tags with page meaning (and legacy ones) become plain boxes.
const renamed: Record<string, string> = { main: "div", section: "div", article: "div", header: "div", footer: "div", center: "div", font: "span" };
const keptAttributes = new Set(["title", "align", "valign", "width", "height", "colspan", "rowspan", "dir", "lang", "bgcolor", "border", "cellpadding", "cellspacing", "alt"]);
// Shorthands arrive expanded into longhands, so both forms are listed.
const styleProperty = /^(color|background-color|font(-family|-size|-weight|-style)?|line-height|text-(align|transform)|text-decoration(-line|-color|-style)?|letter-spacing|(padding|margin)(-(top|right|bottom|left))?|border(-(top|right|bottom|left))?(-(width|style|color))?|border(-(top|bottom)-(left|right))?-radius|border-collapse|border-spacing|(min-|max-)?(width|height)|vertical-align|white-space|word-break|overflow-wrap|display|list-style-type)$/;
const unsafeValue = /url\(|image-set|expression|var\(|attr\(/i;
const safeDisplay = /^(block|inline|inline-block|none|table|table-row|table-cell|list-item)$/;

function safeHref(value: string): string | null {
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:" || url.protocol === "mailto:" ? url.href : null;
  } catch {
    return null;
  }
}

function copyStyle(from: CSSStyleDeclaration, to: HTMLElement) {
  for (let index = 0; index < from.length; index++) {
    const name = from[index];
    const value = from.getPropertyValue(name).trim();
    if (!styleProperty.test(name) || !value || unsafeValue.test(value)) continue;
    if (name === "display" && !safeDisplay.test(value)) continue;
    to.style.setProperty(name, value, from.getPropertyPriority(name));
  }
}

/** CSS text parsed through CSSOM (a constructed sheet), which the style-src policy does not govern. */
function cssRules(text: string): CSSStyleRule[] {
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(text);
    return [...sheet.cssRules].filter((rule): rule is CSSStyleRule => "selectorText" in rule && "style" in rule);
  } catch {
    return [];
  }
}

// The parser would apply (and CSP would refuse, loudly) style attributes and <style> elements
// even in an inert document, so both are renamed before parsing and read back as plain text.
// shortcut: a tag-level regex, so "style=" inside another attribute's value is renamed too; harmless here.
const inlineStyle = "data-mail-style";
const sheetTag = "mail-style";
const neutralise = (html: string) =>
  html.replace(/(<[a-z][^>]*?\s)style(\s*=)/gi, `$1${inlineStyle}$2`).replace(/<(\/?)style\b/gi, `<$1${sheetTag}`);

/**
 * The preview HTML rebuilt as nodes of `target` (the page's document), rooted in one wrapper
 * that stands in for <body>. Nothing is serialised back to markup.
 */
export function sanitizeMailHtml(html: string, target: Document = document): HTMLElement {
  const source = new DOMParser().parseFromString(neutralise(html), "text/html");
  const page = target.createElement("div");
  const pairs: [Element, HTMLElement][] = [[source.body, page]];

  const build = (from: Node, into: HTMLElement) => {
    for (const node of from.childNodes) {
      if (node.nodeType === Node.TEXT_NODE) {
        into.append(target.createTextNode(node.textContent ?? ""));
        continue;
      }
      if (node.nodeType !== Node.ELEMENT_NODE) continue;
      const element = node as Element;
      const tag = element.localName;
      if (droppedTags.has(tag)) continue;
      if (!keptTags.has(tag)) {
        build(element, into);
        continue;
      }
      const copy = target.createElement(renamed[tag] ?? tag);
      for (const attribute of element.attributes) {
        if (keptAttributes.has(attribute.name)) copy.setAttribute(attribute.name, attribute.value);
      }
      if (tag === "center") copy.style.setProperty("text-align", "center");
      if (tag === "a") {
        const href = safeHref(element.getAttribute("href") ?? "");
        if (href) {
          copy.setAttribute("href", href);
          copy.setAttribute("target", "_blank");
          copy.setAttribute("rel", "noopener noreferrer");
        }
      }
      if (tag === "img") {
        const src = element.getAttribute("src") ?? "";
        // Remote images would be blocked by img-src (and could track the reader); data: images are shown.
        if (/^data:image\/(png|gif|jpeg|webp);/i.test(src)) copy.setAttribute("src", src);
        else {
          const stand = target.createElement("span");
          stand.textContent = `[${element.getAttribute("alt") || "image"}]`;
          stand.style.setProperty("color", "#737373");
          into.append(stand);
          continue;
        }
      }
      pairs.push([element, copy]);
      build(element, copy);
      into.append(copy);
    }
  };
  build(source.body, page);

  // Stylesheet rules first (in order, ignoring specificity), then style attributes on top.
  const targets = new Map(pairs);
  for (const style of source.querySelectorAll(sheetTag)) {
    for (const rule of cssRules(style.textContent ?? "")) {
      let matched: NodeListOf<Element>;
      try {
        matched = source.querySelectorAll(rule.selectorText);
      } catch {
        continue;
      }
      for (const element of matched) {
        const into = element === source.documentElement ? page : targets.get(element);
        if (into) copyStyle(rule.style, into);
      }
    }
  }
  const scratch = target.createElement("div");
  for (const [element, copy] of pairs) {
    const text = element.getAttribute(inlineStyle);
    if (!text) continue;
    scratch.style.cssText = text;
    copyStyle(scratch.style, copy);
  }
  return page;
}
