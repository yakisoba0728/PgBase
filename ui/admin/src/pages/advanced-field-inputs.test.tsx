import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { GeoPointField } from "./geo-point-field";
import { portableHTML, RichTextField } from "./rich-text-field";

describe("advanced field controls", () => {
  it("strips only the local origin when portable editor links are enabled", () => {
    const html = '<p><a href="https://app.example/files/a?q=1#part">local</a><img src="https://cdn.example/a.png"><a href="mailto:a@example.com">mail</a></p>';
    expect(portableHTML(html, "https://app.example/admin/")).toBe('<p><a href="/files/a?q=1#part">local</a><img src="https://cdn.example/a.png"><a href="mailto:a@example.com">mail</a></p>');
  });
  it("opens saved HTML without silently rewriting it and preserves source changes", () => {
    const changed = vi.fn();
    function Editor() { const [value, set] = useState('<p data-custom="yes">Saved <strong>HTML</strong></p>'); return <RichTextField id="body" label="Body" value={value} disabled={false} onChange={value => { changed(value); set(value); }} />; }
    render(<Editor />);
    expect(screen.getByRole("textbox", { name: "Body" })).toHaveTextContent("Saved HTML");
    expect(changed).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "HTML source" }));
    const source = screen.getByRole("textbox", { name: "Body" });
    expect(source).toHaveValue('<p data-custom="yes">Saved <strong>HTML</strong></p>');
    fireEvent.change(source, { target: { value: "<p>Changed</p>" } });
    expect(changed).toHaveBeenLastCalledWith("<p>Changed</p>");
    fireEvent.click(screen.getByRole("button", { name: "HTML source" }));
    expect(screen.getByRole("textbox", { name: "Body" })).toHaveTextContent("Changed");
  });
  it("lets users select coordinates with the keyboard and keeps a blank coordinate invalid", () => {
    const changed = vi.fn();
    render(<GeoPointField id="place" label="Place" value='{"lon":127,"lat":37}' disabled={false} onChange={changed} />);
    fireEvent.keyDown(screen.getByRole("group", { name: "Place coordinate map" }), { key: "ArrowUp" });
    expect(changed).toHaveBeenLastCalledWith('{"lon":127,"lat":38}');
    fireEvent.change(screen.getByRole("spinbutton", { name: "Place longitude" }), { target: { value: "" } });
    expect(changed).toHaveBeenLastCalledWith('{"lon":null,"lat":38}');
  });
});
