import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { setLocale, t } from "../i18n";
import { draftMessage, buildInput, newField } from "../designer/collection-draft";
import { fieldOptionsInput } from "../designer/field-options";
import { renderAdmin, scriptedAdmin } from "../test/harness";

it("translates collection fields and validation help while preserving the unsaved schema across a language switch", async () => {
  setLocale("ko");
  const harness = scriptedAdmin("/admin/collections/new");
  renderAdmin(harness);
  expect(await screen.findByRole("heading", { name: "컬렉션 만들기" })).toBeVisible();
  fireEvent.change(screen.getByLabelText("컬렉션 이름"), { target: { value: "articles" } });
  fireEvent.change(screen.getByLabelText("필드 1 이름"), { target: { value: "headline" } });
  expect(screen.getByLabelText("필드 1 유형")).toHaveValue("text");
  expect(screen.getByRole("option", { name: "텍스트" })).toHaveValue("text");
  expect(screen.getByLabelText("필드 1 최소 글자 수")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("tab", { name: "옵션" }));
  expect(screen.getByLabelText("레코드 ID 종류")).toHaveValue("uuid");
  act(() => setLocale("en"));
  await waitFor(() => expect(screen.getByRole("heading", { name: "Create collection" })).toBeVisible());
  expect(screen.getByLabelText("Collection name")).toHaveValue("articles");
  fireEvent.click(screen.getByRole("tab", { name: "Fields" }));
  expect(screen.getByLabelText("Field 1 name")).toHaveValue("headline");
});

it("retains bilingual validation messages instead of freezing the language at error creation", () => {
  setLocale("ko");
  let numericError: unknown;
  try { fieldOptionsInput({ minLength: "1.5" }); } catch (error) { numericError = error; }
  const numericMessage = draftMessage(numericError, "Collection input is invalid.");
  expect(t(numericMessage)).toBe("minLength 값은 0 이상의 정수여야 합니다.");
  let relationError: unknown;
  try { buildInput("create", "notes", [{ ...newField(), name: "parent", type: "relation", relation: { targetCollection: "", cardinality: "single", minSelect: "0", maxSelect: "1", onDelete: "restrict" } }]); } catch (error) { relationError = error; }
  const relationMessage = draftMessage(relationError, "Collection input is invalid.");
  expect(t(relationMessage)).toBe("필드 1의 대상 컬렉션을 선택하세요.");
  setLocale("en");
  expect(t(numericMessage)).toBe("minLength must be a non-negative integer.");
  expect(t(relationMessage)).toBe("Choose a target collection for field 1.");
});
