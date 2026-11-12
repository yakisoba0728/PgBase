import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, vi } from "vitest";
import { setLocale } from "../i18n";

beforeEach(() => { setLocale("en"); localStorage.removeItem("pgbase.locale"); });

afterEach(() => { localStorage.clear(); vi.restoreAllMocks(); });
