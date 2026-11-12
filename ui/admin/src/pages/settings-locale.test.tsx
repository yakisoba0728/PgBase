import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import type { AdminSettings } from "../backend/ports";
import { setLocale } from "../i18n";
import { backendScript } from "../test/backend-script";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";
import { sessionWrapper } from "../test/harness";
import { SettingsPage } from "./settings-page";

const settings: AdminSettings = { batch: { enabled: true, maxRequests: 10, maxBodyBytes: 4096, timeoutMs: 1000 }, logs: { retentionDays: 30, level: "info", includeIP: false, includeAuth: false }, proxy: { trustedProxyCIDRs: [] }, backups: { schedule: "@every 24h", timezone: "UTC", enabled: true, keep: 7 }, application: { theme: "system", accent: "#1d4ed8", branding: "pgBase" }, rateLimits: { admin: { accountMaxFailures: 5, networkMaxFailures: 20, windowSeconds: 900, blockSeconds: 900 }, user: { accountMaxFailures: 5, networkMaxFailures: 20, registrationNetworkMaxFailures: 5, windowSeconds: 900, blockSeconds: 900 }, enabled: false, rules: [], excludedIPs: [] }, revision: 1 };
function showSettings() {
  const backend = memoryBackend(backendScript());
  const save = vi.fn(async (input: AdminSettings) => ({ ...input, revision: 2 }));
  backend.settings = { get: vi.fn(async () => structuredClone(settings)), save, reload: vi.fn() };
  render(<SettingsPage />, { wrapper: sessionWrapper(backend, memoryHistory()) });
  return { save, user: userEvent.setup() };
}

it("switches settings language locally while preserving application drafts", async () => {
  const { save, user } = showSettings();
  await user.type(await screen.findByLabelText("Branding"), " custom");
  await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "ko");
  expect(screen.getByRole("heading", { name: "애플리케이션 설정" })).toBeVisible();
  expect(screen.getByLabelText("애플리케이션 이름")).toHaveValue("pgBase custom");
  expect(localStorage.getItem("pgbase.locale")).toBe("ko");
  expect(save).not.toHaveBeenCalled();
  await user.selectOptions(screen.getByRole("combobox", { name: "언어" }), "en");
  expect(screen.getByLabelText("Branding")).toHaveValue("pgBase custom");
});

it("keeps dynamic validation messages bilingual after changing language", async () => {
  const { save, user } = showSettings();
  await screen.findByLabelText("Branding");
  act(() => setLocale("ko"));
  await user.click(screen.getByRole("tab", { name: "배치 요청" }));
  await user.clear(screen.getByLabelText("배치당 최대 작업 수"));
  await user.type(screen.getByLabelText("배치당 최대 작업 수"), "0");
  await user.click(screen.getByRole("button", { name: "설정 저장" }));
  expect(screen.getByText("배치 요청 수 값은 1~100 사이의 정수여야 합니다.")).toBeVisible();
  expect(save).not.toHaveBeenCalled();
  act(() => setLocale("en"));
  expect(screen.getByText("Batch request count must be a whole number from 1 through 100.")).toBeVisible();
  expect(screen.getByLabelText("Maximum operations per batch")).toHaveValue(0);
});

it("translates API rule controls without changing their stored audience or route pattern", async () => {
  const { save, user } = showSettings();
  await screen.findByLabelText("Branding"); act(() => setLocale("ko"));
  await user.click(screen.getByRole("tab", { name: "API 요청 제한" }));
  await user.click(screen.getByRole("button", { name: "API 요청 제한 규칙 추가" }));
  await user.type(screen.getByLabelText("규칙 1 경로"), "GET /api/items");
  await user.selectOptions(screen.getByLabelText("규칙 1 요청자"), "@auth");
  await user.click(screen.getByRole("button", { name: "설정 저장" }));
  expect(save).toHaveBeenCalledWith(expect.objectContaining({ rateLimits: expect.objectContaining({ rules: [{ label: "GET /api/items", audience: "@auth", duration: 60, maxRequests: 100 }] }) }), expect.any(AbortSignal));
});
