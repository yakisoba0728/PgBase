import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import * as collectionDraft from "../designer/collection-draft";
import { AuthCollectionSchema } from "../components/auth-collection-schema";
import { setLocale } from "../i18n";
import { AuthAdministrationPanel } from "../components/auth-administration-panel";
import { UsersPage } from "./users-page";
import { scriptedAdmin, sessionWrapper } from "../test/harness";

afterEach(() => { setLocale("en"); vi.restoreAllMocks(); });

it("switches auth settings language without replacing provider or policy drafts", async () => {
  const h = scriptedAdmin();
  h.backend.authCollections.getAdministration = vi.fn(async () => ({ policy: { passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 1024, passwordPattern: "", identityFields: ["email"], mfaEnabled: false, mfaDurationSeconds: 300, mfaRule: "", otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5 }, providers: [], templates: [] }));
  render(<AuthAdministrationPanel collectionId="members" />, { wrapper: sessionWrapper(h.backend, h.history) });
  fireEvent.click(await screen.findByText("Authentication settings"));
  fireEvent.change(screen.getByLabelText("Password pattern"), { target: { value: "[A-Z]" } });
  fireEvent.click(screen.getByRole("tab", { name: "OAuth providers" }));
  fireEvent.change(screen.getByLabelText("OIDC issuer"), { target: { value: "https://issuer.example.test" } });
  act(() => setLocale("ko"));
  expect(screen.getByRole("tab", { name: "OAuth 제공자" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByLabelText("OIDC 발급자")).toHaveValue("https://issuer.example.test");
  fireEvent.click(screen.getByRole("tab", { name: "인증 방법" }));
  expect(screen.getByLabelText("비밀번호 패턴")).toHaveValue("[A-Z]");
  expect(screen.getByRole("button", { name: "인증 설정 다시 불러오기" })).toBeDisabled();
});

it("translates an existing async account notice after a language switch", async () => {
  vi.spyOn(window, "confirm").mockReturnValue(true);
  const user = { id: "0199ab10-0000-7000-8000-000000000001", email: "owner@example.test", active: true, verified: true, createdAt: "2026-11-12T00:00:00Z", updatedAt: "2026-11-12T00:00:00Z" };
  const h = scriptedAdmin();
  h.script.users.push({ value: { items: [user] } });
  h.script.setUserActive.push({ value: { ...user, active: false } });
  render(<UsersPage />, { wrapper: sessionWrapper(h.backend, h.history) });
  await screen.findByText(user.email);
  fireEvent.click(screen.getByRole("button", { name: "Deactivate" }));
  expect(await screen.findByRole("status")).toHaveTextContent("owner@example.test is now inactive.");
  act(() => setLocale("ko"));
  expect(screen.getByRole("status")).toHaveTextContent("owner@example.test 사용자를 비활성화했습니다.");
  expect(screen.getByRole("button", { name: "활성화" })).toBeEnabled();
  expect(screen.getByText(user.email)).toBeVisible();
});


it("keeps a schema validation descriptor translatable after the failed save", async () => {
  const h = scriptedAdmin();
  const collection = { id: "members", name: "members", registrationEnabled: false, profileSchema: {}, fields: [{ name: "nickname", type: "text" as const, required: false, indexed: false, unique: false }] };
  render(<AuthCollectionSchema collection={collection} onSaved={vi.fn()} />, { wrapper: sessionWrapper(h.backend, h.history) });
  fireEvent.click(screen.getByText("Fields, access rules and record identity"));
  await screen.findByRole("button", { name: "Save fields and rules" });
  vi.spyOn(collectionDraft, "buildInput").mockImplementationOnce(() => { throw new collectionDraft.DraftError({ en: "Invalid nickname field", ko: "nickname 필드가 올바르지 않습니다." }); });
  fireEvent.click(screen.getByRole("button", { name: "Save fields and rules" }));
  await screen.findByText("Invalid nickname field");
  act(() => setLocale("ko"));
  expect(screen.getByText("nickname 필드가 올바르지 않습니다.")).toBeVisible();
});
