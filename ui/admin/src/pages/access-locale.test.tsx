import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { SessionError } from "../backend/ports";
import { LoginPage } from "./login-page";
import { SetupPage } from "./setup-page";

it("translates existing login validation while preserving typed credentials and password visibility", async () => {
  const user = userEvent.setup(); const login = vi.fn();
  render(<LoginPage onLogin={login} />);
  await user.type(screen.getByLabelText("Password"), "draft password");
  await user.click(screen.getByRole("button", { name: "Show password" }));
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect(screen.getByText("Email is required.")).toBeVisible();
  await user.selectOptions(screen.getByRole("combobox"), "ko");
  expect(screen.getByRole("heading", { name: "pgBase 로그인" })).toBeVisible();
  expect(screen.getByText("이메일을 입력하세요.")).toBeVisible();
  expect(screen.getByLabelText("비밀번호")).toHaveValue("draft password");
  expect(screen.getByLabelText("비밀번호")).toHaveAttribute("type", "text");
  expect(screen.getByRole("button", { name: "비밀번호 숨기기" })).toBeVisible();
  expect(login).not.toHaveBeenCalled();
});

it("keeps the pending OTP step and code when the language changes", async () => {
  const user = userEvent.setup(); const login = vi.fn().mockRejectedValue(new SessionError("otp-required"));
  render(<LoginPage onLogin={login} />);
  await user.type(screen.getByLabelText("Email"), "admin@example.test");
  await user.type(screen.getByLabelText("Password"), "draft password");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  await user.type(await screen.findByLabelText("Authenticator code"), "123");
  await user.selectOptions(screen.getByRole("combobox"), "ko");
  expect(screen.getByLabelText("인증 코드")).toHaveValue("123");
  expect(screen.getByLabelText("비밀번호")).toHaveValue("draft password");
  await user.click(screen.getByRole("button", { name: "로그인" }));
  expect(screen.getByText("인증 앱의 6자리 코드를 입력하세요.")).toBeVisible();
  expect(login).toHaveBeenCalledOnce();
});

it("translates setup validation and keeps the setup draft, then preserves an uncertain restore lock", async () => {
  const user = userEvent.setup(); const setup = vi.fn(); const restore = vi.fn().mockRejectedValue(new Error("connection lost"));
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(<SetupPage onSetup={setup} onUploadBackup={vi.fn().mockResolvedValue({ id: "backup" })} onRestoreBackup={restore} />);
  await user.type(screen.getByLabelText("Setup token"), "draft-token");
  await user.type(screen.getByLabelText("Password", { exact: true }), "short");
  await user.click(screen.getByRole("button", { name: "Create administrator" }));
  await user.selectOptions(screen.getByRole("combobox"), "ko");
  expect(screen.getByText("비밀번호는 12~1024바이트여야 합니다.")).toBeVisible();
  expect(screen.getByLabelText("설정 토큰")).toHaveValue("draft-token");
  expect(screen.getByLabelText("비밀번호", { exact: true })).toHaveValue("short");
  await user.click(screen.getByText("초기 백업 복원"));
  await user.upload(screen.getByLabelText("Gzip 백업"), new File(["gzip"], "backup.gz"));
  await user.click(screen.getByRole("button", { name: "백업 업로드 및 복원" }));
  expect(await screen.findByRole("status")).toHaveTextContent("복원 결과를 확인할 수 없습니다.");
  expect(window.confirm).toHaveBeenCalledWith("첫 관리자를 생성하기 전에 이 백업을 복원할까요? 초기 데이터베이스 상태가 교체됩니다.");
  await user.selectOptions(screen.getByRole("combobox"), "en");
  expect(screen.getByRole("button", { name: "Waiting for backup restore…" })).toBeDisabled();
  expect(screen.getByRole("status")).toHaveTextContent("Restore outcome is unknown.");
  expect(screen.getByLabelText("Setup token")).toHaveValue("draft-token");
  expect(restore).toHaveBeenCalledOnce(); expect(setup).not.toHaveBeenCalled();
});
