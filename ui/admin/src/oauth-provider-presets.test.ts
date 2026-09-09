import { describe, expect, it } from "vitest";
import { providerPresets } from "./oauth-provider-presets";

describe("OAuth provider presets", () => {
  it("matches the PocketBase v0.40.3 provider registry", () => {
    expect(providerPresets.map((item) => item.value.name).sort()).toEqual([
      "apple", "bitbucket", "box", "discord", "facebook", "gitea", "gitee", "github", "gitlab", "google",
      "instagram2", "kakao", "lark", "linear", "livechat", "mailcow", "microsoft", "monday", "notion", "oidc",
      "patreon", "planningcenter", "spotify", "strava", "trakt", "twitch", "twitter", "vk", "wakatime", "yandex",
    ]);
    expect(new Set(providerPresets.map((item) => item.value.name)).size).toBe(providerPresets.length);
  });
});
