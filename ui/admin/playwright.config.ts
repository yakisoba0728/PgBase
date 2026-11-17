import { defineConfig, devices, type Project } from "@playwright/test";

const onboardingBaseURL = process.env.PGBASE_ONBOARDING_BROWSER_BASE_URL ??
  process.env.PGBASE_BROWSER_BASE_URL ?? "http://127.0.0.1:8090";
const authenticatedBaseURL = process.env.PGBASE_AUTHENTICATED_BROWSER_BASE_URL;
const managedBackupBaseURL = process.env.PGBASE_MANAGED_BACKUP_BROWSER_BASE_URL;

// Onboarding needs an instance with no administrator, so it can never share a
// backend with the authenticated project.
if (authenticatedBaseURL !== undefined && new URL(authenticatedBaseURL).origin === new URL(onboardingBaseURL).origin) {
  throw new Error("onboarding and authenticated Playwright projects require isolated backend origins");
}
if (managedBackupBaseURL !== undefined && [onboardingBaseURL, authenticatedBaseURL].some((base) => base !== undefined && new URL(base).origin === new URL(managedBackupBaseURL).origin)) {
  throw new Error("managed backup restore requires its own disposable backend origin");
}

const projects: Project[] = [
  {
    name: "browser-contracts-chromium",
    testDir: "./e2e/contracts",
    use: { ...devices["Desktop Chrome"] },
  },
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
if (managedBackupBaseURL !== undefined) {
  projects.push({
    name: "managed-backups-chromium",
    testDir: "./e2e/managed-backups",
    workers: 1,
    use: { ...devices["Desktop Chrome"], baseURL: managedBackupBaseURL },
  });
}

// Extra engines are an explicit smoke opt-in. Keep the normal Chromium suite
// intact, and require both disposable fixtures so a partial smoke cannot pass.
const smokeEngines = (process.env.PGBASE_BROWSER_SMOKE_ENGINES ?? "")
  .split(",").map((engine) => engine.trim()).filter(Boolean)
  .map((engine) => {
    if (engine !== "firefox" && engine !== "webkit") {
      throw new Error(`unsupported PGBASE_BROWSER_SMOKE_ENGINES value: ${engine}`);
    }
    return engine;
  });
if (new Set(smokeEngines).size !== smokeEngines.length) {
  throw new Error("PGBASE_BROWSER_SMOKE_ENGINES must not contain duplicate engines");
}
if (smokeEngines.length > 0 && (!authenticatedBaseURL || !managedBackupBaseURL)) {
  throw new Error("browser smoke requires isolated authenticated and managed-backup fixtures");
}
for (const engine of smokeEngines) {
  const device = engine === "firefox" ? devices["Desktop Firefox"] : devices["Desktop Safari"];
  projects.push({
    name: `authenticated-smoke-${engine}`,
    testDir: "./e2e/authenticated",
    // Include the collection-creation prerequisite for the serial record flow.
    grep: /(?:rejects bad credentials, restores the session after a reload, and signs out|creates a base collection through the schema designer plan and apply|creates, edits, filters and deletes records|the command palette jumps to a screen|mobile drawer moves, contains and restores keyboard focus)$/,
    use: { ...device, baseURL: authenticatedBaseURL },
  }, {
    name: `managed-backups-smoke-${engine}`,
    testDir: "./e2e/managed-backups",
    use: { ...device, baseURL: managedBackupBaseURL },
  });
}

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  retries: 0,
  fullyParallel: false,
  // All opted-in projects share the two disposable backends. Serialize across
  // engines as well as within each project to avoid schema/restore interference.
  workers: smokeEngines.length > 0 ? 1 : undefined,
  use: {
    trace: "retain-on-failure",
    // The UI follows navigator.language; specs assert English text unless they switch language themselves.
    locale: "en-US",
  },
  projects,
});
