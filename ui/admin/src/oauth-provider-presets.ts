import type { OAuthProviderSettings } from "./backend/ports";

type Preset = { label: string; value: Partial<OAuthProviderSettings> & Pick<OAuthProviderSettings, "name"> };
const p = (label: string, name: string, authorizationUrl: string, tokenUrl: string, userInfoUrl: string, scopes: string[], subjectField = "id"): Preset => ({ label, value: { name, authorizationUrl, tokenUrl, userInfoUrl, scopes, subjectField } });

// Defaults track PocketBase's built-in OAuth provider catalog. Provider-aware
// response normalization is selected by name on the server; URLs remain editable
// for self-hosted and regional installations.
export const providerPresets: Preset[] = [
  p("Apple", "apple", "https://appleid.apple.com/auth/authorize", "https://appleid.apple.com/auth/token", "https://appleid.apple.com/auth/keys", ["name", "email"], "sub"),
  p("Bitbucket", "bitbucket", "https://bitbucket.org/site/oauth2/authorize", "https://bitbucket.org/site/oauth2/access_token", "https://api.bitbucket.org/2.0/user", ["account", "email"]),
  p("Box", "box", "https://account.box.com/api/oauth2/authorize", "https://api.box.com/oauth2/token", "https://api.box.com/2.0/users/me", ["root_readonly"]),
  p("Discord", "discord", "https://discord.com/api/oauth2/authorize", "https://discord.com/api/oauth2/token", "https://discord.com/api/users/@me", ["identify", "email"]),
  p("Facebook", "facebook", "https://www.facebook.com/dialog/oauth", "https://graph.facebook.com/oauth/access_token", "https://graph.facebook.com/me?fields=name,email,picture.type(large)", ["email"]),
  p("Gitea / Forgejo", "gitea", "https://gitea.com/login/oauth/authorize", "https://gitea.com/login/oauth/access_token", "https://gitea.com/api/v1/user", ["read:user", "user:email"]),
  p("Gitee", "gitee", "https://gitee.com/oauth/authorize", "https://gitee.com/oauth/token", "https://gitee.com/api/v5/user", ["user_info", "emails"]),
  p("GitHub", "github", "https://github.com/login/oauth/authorize", "https://github.com/login/oauth/access_token", "https://api.github.com/user", ["read:user", "user:email"]),
  p("GitLab", "gitlab", "https://gitlab.com/oauth/authorize", "https://gitlab.com/oauth/token", "https://gitlab.com/api/v4/user", ["read_user"]),
  p("Google", "google", "https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token", "https://www.googleapis.com/oauth2/v3/userinfo", ["openid", "email", "profile"], "sub"),
  p("Instagram", "instagram2", "https://www.instagram.com/oauth/authorize", "https://api.instagram.com/oauth/access_token", "https://graph.instagram.com/me?fields=id,username,name,profile_picture_url", ["instagram_business_basic"]),
  p("Kakao", "kakao", "https://kauth.kakao.com/oauth/authorize", "https://kauth.kakao.com/oauth/token", "https://kapi.kakao.com/v2/user/me", ["account_email", "profile_nickname", "profile_image"]),
  p("Lark", "lark", "https://accounts.feishu.cn/open-apis/authen/v1/authorize", "https://open.feishu.cn/open-apis/authen/v2/oauth/token", "https://open.feishu.cn/open-apis/authen/v1/user_info", []),
  p("Linear", "linear", "https://linear.app/oauth/authorize", "https://api.linear.app/oauth/token", "https://api.linear.app/graphql", ["read"]),
  p("LiveChat", "livechat", "https://accounts.livechat.com/", "https://accounts.livechat.com/v2/token", "https://accounts.livechat.com/v2/info", []),
  p("Mailcow", "mailcow", "https://mailcow.example/oauth/authorize", "https://mailcow.example/oauth/token", "https://mailcow.example/oauth/profile", ["profile"]),
  p("Microsoft", "microsoft", "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", "https://login.microsoftonline.com/common/oauth2/v2.0/token", "https://graph.microsoft.com/v1.0/me", ["User.Read"]),
  p("monday.com", "monday", "https://auth.monday.com/oauth2/authorize", "https://auth.monday.com/oauth2/token", "https://api.monday.com/v2", ["me:read"]),
  p("Notion", "notion", "https://api.notion.com/v1/oauth/authorize", "https://api.notion.com/v1/oauth/token", "https://api.notion.com/v1/users/me", []),
  p("OIDC", "oidc", "https://identity.example/authorize", "https://identity.example/token", "https://identity.example/userinfo", ["openid", "email", "profile"], "sub"),
  p("Patreon", "patreon", "https://www.patreon.com/oauth2/authorize", "https://www.patreon.com/api/oauth2/token", "https://www.patreon.com/api/oauth2/v2/identity?fields%5Buser%5D=full_name,email,image_url,is_email_verified", ["identity", "identity[email]"]),
  p("Planning Center", "planningcenter", "https://api.planningcenteronline.com/oauth/authorize", "https://api.planningcenteronline.com/oauth/token", "https://api.planningcenteronline.com/people/v2/me", ["people"]),
  p("Spotify", "spotify", "https://accounts.spotify.com/authorize", "https://accounts.spotify.com/api/token", "https://api.spotify.com/v1/me", ["user-read-private"]),
  p("Strava", "strava", "https://www.strava.com/oauth/authorize", "https://www.strava.com/api/v3/oauth/token", "https://www.strava.com/api/v3/athlete", ["read"]),
  p("Trakt", "trakt", "https://trakt.tv/oauth/authorize", "https://api.trakt.tv/oauth/token", "https://api.trakt.tv/users/settings", []),
  p("Twitch", "twitch", "https://id.twitch.tv/oauth2/authorize", "https://id.twitch.tv/oauth2/token", "https://api.twitch.tv/helix/users", ["user:read:email"]),
  p("X / Twitter", "twitter", "https://x.com/i/oauth2/authorize", "https://api.x.com/2/oauth2/token", "https://api.x.com/2/users/me?user.fields=id,name,username,profile_image_url,confirmed_email", ["users.read", "users.email", "tweet.read"]),
  p("VK", "vk", "https://oauth.vk.com/authorize", "https://oauth.vk.com/access_token", "https://api.vk.com/method/users.get?fields=photo_max,screen_name&v=5.131", ["email"]),
  p("WakaTime", "wakatime", "https://wakatime.com/oauth/authorize", "https://wakatime.com/oauth/token", "https://wakatime.com/api/v1/users/current", ["email"]),
  p("Yandex", "yandex", "https://oauth.yandex.com/authorize", "https://oauth.yandex.com/token", "https://login.yandex.ru/info", ["login:email", "login:avatar", "login:info"]),
];
