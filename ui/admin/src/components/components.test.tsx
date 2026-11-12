import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Field } from "./Field";
import { ConfirmDialog } from "./Dialog";
import { SaveBar } from "./SaveBar";
import { toast, ToastHost } from "./Toast";

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

describe("Toast", () => {
  it("stays while focused and leaves after the delay once focus moves on", () => {
    vi.useFakeTimers();
    try {
      render(<ToastHost />);
      act(() => toast("Disabled", "info", { label: "Undo", run: () => {} }));
      const undo = screen.getByRole("button", { name: "Undo" });
      act(() => undo.focus());
      act(() => vi.advanceTimersByTime(20_000));
      expect(screen.getByText("Disabled")).toBeInTheDocument();
      act(() => undo.blur());
      act(() => vi.advanceTimersByTime(8_000));
      expect(screen.queryByText("Disabled")).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows only the newest; an error underneath waits without timing out, a plain note is replaced", () => {
    vi.useFakeTimers();
    try {
      render(<ToastHost />);
      act(() => toast("Failed", "danger"));
      act(() => toast("Saved"));
      act(() => toast("Saved again"));
      expect(screen.queryByText("Failed")).not.toBeInTheDocument();
      expect(screen.queryByText("Saved")).not.toBeInTheDocument();
      act(() => vi.advanceTimersByTime(5_000));
      // The error had no time running while hidden, so it now shows for its full time.
      expect(screen.getByText("Failed")).toBeInTheDocument();
      act(() => vi.advanceTimersByTime(4_000));
      expect(screen.getByText("Failed")).toBeInTheDocument();
      act(() => vi.advanceTimersByTime(1_000));
      expect(screen.queryByText("Failed")).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("SaveBar", () => {
  // fireEvent returns false once the shortcut called preventDefault (no browser "Save page as…").
  const press = () => fireEvent.keyDown(document, { key: "s", ctrlKey: true });

  it("saves with Cmd/Ctrl+S, and under a modal dialog only keeps the browser from saving the page", () => {
    const onSave = vi.fn();
    render(<SaveBar dirty onDiscard={() => {}} onSave={onSave} />);
    expect(press()).toBe(false);
    expect(onSave).toHaveBeenCalledTimes(1);
    const dialog = document.body.appendChild(document.createElement("dialog"));
    dialog.showModal();
    expect(press()).toBe(false);
    expect(onSave).toHaveBeenCalledTimes(1);
    dialog.remove();
  });
});
