import { describe, expect, it } from "vitest";
import { sanitizeMailHtml } from "./mailPreview";

const layout = `<!doctype html><html><head><style>body{background:#f5f5f5;color:#171717}.card{padding:28px;background:#fff}.button{color:#fff!important;background:url(https://evil.test/x.png)}</style></head>
<body><main class="card" id="x" onclick="steal()"><p style="color:red;position:fixed;background-image:url(https://t.test/p.gif)">Hi <a class="button" href="https://example.com/a?token=1" onmouseover="x()">go</a>
<a href="javascript:alert(1)">bad</a></p><script>alert(1)</script><form action="https://evil.test"><input name="p"></form>
<img src="https://t.test/pixel.gif" alt="logo"><blink>kept text</blink></main></body></html>`;

describe("sanitizeMailHtml", () => {
  const page = sanitizeMailHtml(layout);
  const html = page.innerHTML;

  it("drops scripts, handlers, forms, classes and ids", () => {
    expect(html).not.toMatch(/script|onclick|onmouseover|<form|<input|class=|id=/);
    expect(page.textContent).not.toContain("alert(1)");
  });

  it("keeps safe links only, opening outside the admin", () => {
    const links = page.querySelectorAll("a");
    expect(links[0].getAttribute("href")).toBe("https://example.com/a?token=1");
    expect(links[0].getAttribute("rel")).toBe("noopener noreferrer");
    expect(links[1].hasAttribute("href")).toBe(false);
  });

  it("re-applies allowed styles from rules and attributes, never remote resources", () => {
    const card = page.firstElementChild as HTMLElement;
    expect(card.localName).toBe("div");
    expect(card.style.getPropertyValue("padding")).toBe("28px");
    const paragraph = page.querySelector("p") as HTMLElement;
    expect(paragraph.style.color).toBe("red");
    expect(paragraph.style.position).toBe("");
    expect(paragraph.style.backgroundImage).toBe("");
    expect(page.style.getPropertyValue("background-color") || page.style.background).toContain("245");
    const button = page.querySelector("a") as HTMLElement;
    expect(button.style.getPropertyPriority("color")).toBe("important");
    expect(html).not.toContain("evil.test");
  });

  it("replaces remote images with their alt text and unwraps unknown tags", () => {
    expect(page.querySelector("img")).toBeNull();
    expect(page.textContent).toContain("[logo]");
    expect(page.textContent).toContain("kept text");
    expect(html).not.toContain("blink");
  });
});
