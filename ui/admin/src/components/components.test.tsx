import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Field } from "./Field";
import { ConfirmDialog } from "./Dialog";

describe("Field", () => {
  it("labels the control and marks it required", () => {
    render(<Field label="Email" name="email" type="email" required />);
    const input = screen.getByLabelText(/Email/);
    expect(input).toHaveAttribute("type", "email");
    expect(input).toBeRequired();
  });

  it("links an error to the control", () => {
    render(<Field label="Name" error="Too short" />);
    const input = screen.getByLabelText("Name");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("Too short");
  });

  it("renders select options and textarea kinds", () => {
    render(
      <>
        <Field kind="select" label="Type" options={[{ value: "a", label: "Alpha" }, { value: "b", label: "Beta" }]} defaultValue="b" />
        <Field kind="textarea" label="Notes" />
      </>,
    );
    expect(screen.getByLabelText("Type")).toHaveValue("b");
    expect(screen.getByLabelText("Notes").tagName).toBe("TEXTAREA");
  });
});

describe("ConfirmDialog", () => {
  it("opens as a modal dialog and reports the choice", async () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(<ConfirmDialog open title="Delete 2 records?" confirmLabel="Delete" danger onConfirm={onConfirm} onCancel={onCancel} />);
    expect(screen.getByRole("dialog", { name: "Delete 2 records?" })).toHaveAttribute("open");
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(onConfirm).toHaveBeenCalledOnce();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("renders nothing inside while closed", () => {
    render(<ConfirmDialog open={false} title="Hidden" message="secret" onConfirm={() => {}} onCancel={() => {}} />);
    expect(screen.queryByText("secret")).not.toBeInTheDocument();
  });
});
