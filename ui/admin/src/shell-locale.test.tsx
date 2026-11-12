import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { useSession } from "./auth/session";
import { CollectionCatalog } from "./backend/ports";
import { WorkspaceNavigation } from "./components/workspace-chrome";
import { setLocale } from "./i18n";
import { draftLedger } from "./routing/unsaved-draft";
import { scriptedAdmin, sessionWrapper } from "./test/harness";

afterEach(() => { act(() => setLocale("en")); });

it("switches shell labels and Korean search without translating collection identifiers", async () => {
  const h = scriptedAdmin();
  h.backend.schema.catalog = vi.fn(async () => new CollectionCatalog(1, [{ name: "Settings", tableName: "Settings", fields: [] }]));
  render(<WorkspaceNavigation route={{ kind: "collections" }} open={false} navigate={vi.fn()} onClose={vi.fn()} extensions={[]} sqlEnabled={false} />, { wrapper: sessionWrapper(h.backend, h.history) });
  expect(await screen.findByRole("link", { name: "Open Settings collection" })).toBeVisible();
  act(() => setLocale("ko"));
  expect(screen.getByRole("link", { name: "Settings 컬렉션 열기" })).toHaveTextContent("Settings");
  expect(screen.getByRole("link", { name: "새 컬렉션" })).toHaveAttribute("href", "/admin/collections/new");
  const search = screen.getByRole("searchbox", { name: "페이지 찾기" });
  await userEvent.type(search, "사용자");
  expect(screen.getByRole("link", { name: "사용자" })).toBeVisible();
  act(() => setLocale("en"));
  expect(screen.getByRole("searchbox", { name: "Find a page" })).toHaveValue("사용자");
});

it("uses the current language when asking to discard an existing draft", () => {
  const confirm = vi.fn(() => false);
  const ledger = draftLedger(confirm);
  ledger.hold("record Settings", true);
  act(() => setLocale("ko"));
  expect(ledger.mayLeave()).toBe(false);
  expect(confirm).toHaveBeenLastCalledWith("이 페이지를 나가시겠습니까? 저장하지 않은 변경 사항은 사라집니다.");
  act(() => setLocale("en"));
  expect(ledger.mayLeave()).toBe(false);
  expect(confirm).toHaveBeenLastCalledWith("Leave this page? The changes you have not saved will be lost.");
});


it("translates an existing session failure when the locale changes", async () => {
  const h = scriptedAdmin();
  h.script.resume[0] = { value: { kind: "unreachable" } };
  function SessionFailure() { const { sessionError } = useSession(); return sessionError ? <p role="alert">{sessionError}</p> : null; }
  render(<SessionFailure />, { wrapper: sessionWrapper(h.backend, h.history) });
  expect(await screen.findByRole("alert")).toHaveTextContent("Unable to check the current session. Please sign in.");
  act(() => setLocale("ko"));
  expect(screen.getByRole("alert")).toHaveTextContent("현재 세션을 확인할 수 없습니다. 로그인하세요.");
  act(() => setLocale("en"));
  expect(screen.getByRole("alert")).toHaveTextContent("Unable to check the current session. Please sign in.");
});
