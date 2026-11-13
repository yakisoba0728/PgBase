import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import RichText, { keepsMarkup } from "./RichText";

describe("keepsMarkup", () => {
  it("accepts what the editor only adds to or renames", () => {
    expect(keepsMarkup("<p>a <b>b</b></p>", "<p>a <strong>b</strong></p>")).toBe(true);
    expect(keepsMarkup('<p><a href="/x">x</a></p>', '<p><a target="_blank" rel="noopener" href="/x">x</a></p>')).toBe(true);
  });

  it("flags dropped classes, styles, elements and text", () => {
    expect(keepsMarkup('<p class="lead">a</p>', "<p>a</p>")).toBe(false);
    expect(keepsMarkup('<p><span style="color:red">a</span></p>', "<p>a</p>")).toBe(false);
    expect(keepsMarkup('<p>a</p><iframe src="https://e.example"></iframe>', "<p>a</p>")).toBe(false);
  });
});

describe("RichText", () => {
  const props = { id: "body", label: "body", readOnly: false, invalid: false };

  it("opens HTML the editor would change lossily as source, unchanged", async () => {
    const onChange = vi.fn();
    const html = '<p class="lead">Hello <span style="color:red">red</span></p>';
    render(<RichText {...props} value={html} onChange={onChange} />);
    await waitFor(() => expect(screen.getByRole("textbox", { name: "body" })).toHaveValue(html));
    expect(screen.getByText("This HTML has markup the visual editor cannot keep")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("opens supported HTML in the visual editor", async () => {
    render(<RichText {...props} value="<p>Hello <strong>bold</strong></p>" onChange={() => undefined} />);
    await waitFor(() => expect(document.querySelector(".rich-text-doc")).not.toBeNull());
    expect(document.querySelector("textarea")).toBeNull();
    expect(screen.queryByText("This HTML has markup the visual editor cannot keep")).toBeNull();
  });
});

it("rejects external image URLs that the administrator CSP cannot display", async () => {
  const { fireEvent } = await import("@testing-library/react");
  render(<RichText id="image-test" label="image body" readOnly={false} invalid={false} value="<p>test</p>" onChange={vi.fn()} />);
  const image = await screen.findByRole("button", { name: "Image from URL" });
  fireEvent.click(image);
  const url = screen.getByRole("textbox", { name: "Image URL" });
  fireEvent.change(url, { target: { value: "https://external.example/image.png" } });
  expect(url).toHaveAttribute("aria-invalid", "true");
  expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
  fireEvent.change(url, { target: { value: "/image.png" } });
  expect(screen.getByRole("button", { name: "Apply" })).toBeEnabled();
});
