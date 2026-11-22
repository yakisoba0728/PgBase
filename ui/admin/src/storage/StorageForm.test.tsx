import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { setLanguage } from "../i18n";
import { StorageFields } from "./StorageForm";
import type { StorageDraft } from "./model";

beforeEach(() => setLanguage("en"));
afterEach(cleanup);

const draft: StorageDraft = { driver: "s3", bucket: "b", prefix: "", region: "us-east-1", endpoint: "", accessKey: "", secretKey: "", clearSecret: false, usePathStyle: true, retention: "0" };
const warning = /travel unencrypted/;

it("warns under a backup endpoint that sends archives over plain http", () => {
  render(<StorageFields draft={{ ...draft, endpoint: "http://minio:9000" }} onChange={() => {}} errors={{}} secretConfigured={false} backups />);
  expect(screen.getByRole("status")).toHaveTextContent(warning);
});

it("does not warn for https, private IP literals or file storage", () => {
  const { rerender } = render(<StorageFields draft={{ ...draft, endpoint: "https://s3.example.com" }} onChange={() => {}} errors={{}} secretConfigured={false} backups />);
  expect(screen.queryByText(warning)).toBeNull();
  rerender(<StorageFields draft={{ ...draft, endpoint: "http://10.0.0.5:9000" }} onChange={() => {}} errors={{}} secretConfigured={false} backups />);
  expect(screen.queryByText(warning)).toBeNull();
  rerender(<StorageFields draft={{ ...draft, endpoint: "http://minio:9000" }} onChange={() => {}} errors={{}} secretConfigured={false} />);
  expect(screen.queryByText(warning)).toBeNull();
});
