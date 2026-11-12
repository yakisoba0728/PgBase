import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { setLocale } from "../i18n";
import type { OperationsPort } from "../backend/ports";
import type { SQLConsolePort } from "../backend/sql-console-backend";
import { OperationsConsole } from "./operations-console";
import { SQLConsolePage } from "./sql-console-page";

afterEach(() => { act(() => setLocale("en")); });
it("switches operation labels and retained failure notices without rewriting request data", async () => {
  const backend: OperationsPort = {
    logs: vi.fn().mockResolvedValue({ items: [{ id: 1, at: "now", method: "GET", route: "/api/records/{id}", status: 200, durationMs: 3 }], oldestId: 1 }),
    jobs: vi.fn().mockRejectedValue(new Error("unavailable")), configureJob: vi.fn(), runJob: vi.fn(),
  };
  setLocale("en"); render(<OperationsConsole port={backend} />);
  expect(await screen.findByText(/Unable to load scheduled jobs/)).toBeVisible();
  act(() => setLocale("ko"));
  expect(screen.getByRole("tab", { name: "요청" })).toBeVisible();
  expect(screen.getByRole("status")).toHaveTextContent("불러오기 실패: 예약 작업");
  expect(screen.getByText("/api/records/{id}")).toBeVisible();
  expect(screen.getByText("GET")).toBeVisible();
  act(() => setLocale("en"));
  expect(screen.getByRole("status")).toHaveTextContent("Unable to load scheduled jobs");
});
it("localizes SQL controls and retains SQL, exact returned values and opaque execution IDs", async () => {
  const backend: SQLConsolePort = {
    history: vi.fn().mockResolvedValue({ executions: [], writeEnabled: false, writeTargets: [], sessionIdentity: "session" }),
    submit: vi.fn().mockResolvedValue({ id: "opaque-execution", mode: "read", status: "succeeded", createdAt: "now", result: { rows: 1, affectedRows: 0, bytes: 19, elapsedMs: 1, statements: [{ affectedRows: 0, columns: [{ name: "raw_column", typeName: "int8", typeOid: 20 }], rows: [[{ kind: "integer", text: "9223372036854775807" }]] }] } }),
    execution: vi.fn(), cancel: vi.fn(),
  };
  setLocale("ko"); render(<SQLConsolePage port={backend} />);
  expect(screen.getByLabelText("SQL 문장 1")).toHaveValue("SELECT 1");
  await userEvent.click(screen.getByRole("button", { name: "읽기 쿼리 실행" }));
  expect(await screen.findByText("9223372036854775807")).toBeVisible();
  expect(screen.getByRole("button", { name: "1행 1열 복사" })).toBeVisible();
  expect(screen.getByText("raw_column")).toBeVisible();
  expect(screen.getByText(/opaque-execution/)).toBeVisible();
});
