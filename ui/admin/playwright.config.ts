import { defineConfig, devices } from "@playwright/test";

const onboardingBaseURL = process.env.PGBASE_ONBOARDING_BROWSER_BASE_URL ??
  process.env.PGBASE_BROWSER_BASE_URL ?? "http://127.0.0.1:8090";
const authenticatedBaseURL = process.env.PGBASE_AUTHENTICATED_BROWSER_BASE_URL;

if (authenticatedBaseURL !== undefined && new URL(authenticatedBaseURL).origin === new URL(onboardingBaseURL).origin) {
  throw new Error("onboarding and authenticated Playwright projects require isolated backend origins");
}

const projects = [
  {
    name: "onboarding-chromium",
    testMatch: "onboarding.spec.ts",
    use: { ...devices["Desktop Chrome"], baseURL: onboardingBaseURL },
  },
];
if (authenticatedBaseURL !== undefined) {
  projects.push({
    name: "authenticated-chromium",
    testMatch: "authenticated.spec.ts",
    use: { ...devices["Desktop Chrome"], baseURL: authenticatedBaseURL },
  });
}

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  retries: 0,
  use: {
    trace: "retain-on-failure",
  },
  projects,
});
