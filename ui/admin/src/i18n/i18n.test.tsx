import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { getLocale, message, setLocale, t, useLocale } from ".";
import { LanguagePicker } from "./language-picker";

describe("administrator language", () => {
  it("updates the document and translated feedback without losing a draft", () => {
    function Editor() {
      useLocale();
      const [draft, setDraft] = useState("user-owned title");
      const [notice] = useState(() => message("Saved 2 records.", "레코드 2개를 저장했습니다."));
      return <><LanguagePicker /><input aria-label="draft" value={draft} onChange={event => setDraft(event.target.value)} /><p>{t(notice)}</p></>;
    }
    render(<Editor />);
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsubmitted value" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Language" }), { target: { value: "ko" } });
    expect(document.documentElement.lang).toBe("ko");
    expect(screen.getByText("레코드 2개를 저장했습니다.")).toBeVisible();
    expect(screen.getByLabelText("draft")).toHaveValue("unsubmitted value");
    fireEvent.change(screen.getByRole("combobox", { name: "언어" }), { target: { value: "en" } });
    expect(screen.getByText("Saved 2 records.")).toBeVisible();
  });
  it("keeps interpolation literal and leaves unknown technical text unchanged", () => {
    setLocale("ko");
    expect(t(message("Open {name}", "{name} 열기"), { name: "$& <script> {id}" })).toBe("$& <script> {id} 열기");
    expect(t("SELECT id FROM app.posts")).toBe("SELECT id FROM app.posts");
    expect(t(message("{missing}", "{missing}"), {})).toBe("{missing}");
  });
  it("still switches when browser storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("denied"); });
    setLocale("ko");
    expect(getLocale()).toBe("ko");
    expect(t("Save")).toBe("저장");
  });
  it("applies another tab's language selection while subscribed", () => {
    render(<LanguagePicker />);
    fireEvent(window, new StorageEvent("storage", { key: "pgbase.locale", newValue: "ko" }));
    expect(screen.getByRole("combobox", { name: "언어" })).toHaveValue("ko");
  });
});
