import { defineConfig, devices, type Project } from "@playwright/test";

const onboardingBaseURL = process.env.PGBASE_ONBOARDING_BROWSER_BASE_URL ??
  process.env.PGBASE_BROWSER_BASE_URL ?? "http://127.0.0.1:8090";
const authenticatedBaseURL = process.env.PGBASE_AUTHENTICATED_BROWSER_BASE_URL;

// Onboarding needs an instance with no administrator, so it can never share a
// backend with the authenticated project.
if (authenticatedBaseURL !== undefined && new URL(authenticatedBaseURL).origin === new URL(onboardingBaseURL).origin) {
  throw new Error("onboarding and authenticated Playwright projects require isolated backend origins");
}

const projects: Project[] = [
  {
    name: "onboarding-chromium",
    testDir: "./e2e/onboarding",
    use: { ...devices["Desktop Chrome"], baseURL: onboardingBaseURL },
  },
];
if (authenticatedBaseURL !== undefined) {
  projects.push({
    name: "authenticated-chromium",
    testDir: "./e2e/authenticated",
    // Every authenticated spec mutates one catalog-versioned backend. Distinct
    // rows do not make schema plans independent, so parallel files only create
    // expected stale-plan failures instead of exercising their workflow.
    workers: 1,
    use: { ...devices["Desktop Chrome"], baseURL: authenticatedBaseURL },
  });
}

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  retries: 0,
  fullyParallel: false,
  use: {
    trace: "retain-on-failure",
  },
  projects,
});
