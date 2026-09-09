package userauth

// Provider profile mappings are small adaptations of PocketBase's MIT-licensed
// tools/auth provider mappings (https://github.com/pocketbase/pocketbase).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type profileRule struct {
	subject, email, verified, avatar string
	trustedEmail                     bool
}

var providerProfileRules = map[string]profileRule{
	"google":         {"sub", "email", "email_verified", "picture", false},
	"oidc":           {"sub", "email", "email_verified", "picture", false},
	"discord":        {"id", "email", "verified", "", false},
	"facebook":       {"id", "email", "", "picture.data.url", true},
	"bitbucket":      {"uuid", "", "", "links.avatar.href", false},
	"box":            {"id", "login", "", "avatar_url", true},
	"gitea":          {"id", "", "", "avatar_url", false},
	"gitee":          {"id", "email", "", "avatar_url", true},
	"github":         {"id", "", "", "avatar_url", false},
	"gitlab":         {"id", "email", "", "avatar_url", true},
	"instagram2":     {"id", "", "", "profile_picture_url", false},
	"kakao":          {"id", "kakao_account.email", "kakao_account.is_email_verified", "properties.profile_image", false},
	"lark":           {"data.union_id", "", "", "data.avatar_url", false},
	"linear":         {"data.viewer.id", "data.viewer.email", "", "data.viewer.avatarUrl", true},
	"livechat":       {"id", "email", "", "avatar", true},
	"mailcow":        {"id", "email", "", "", true},
	"microsoft":      {"id", "mail", "", "", true},
	"monday":         {"data.me.id", "data.me.email", "data.me.is_verified", "data.me.photo_thumb", false},
	"notion":         {"bot.owner.user.id", "bot.owner.user.person.email", "", "bot.owner.user.avatar_url", true},
	"patreon":        {"data.id", "data.attributes.email", "data.attributes.is_email_verified", "data.attributes.image_url", false},
	"planningcenter": {"data.id", "data.attributes.primary_contact_email", "", "data.attributes.avatar", true},
	"spotify":        {"id", "", "", "images.0.url", false},
	"strava":         {"id", "", "", "profile", false},
	"trakt":          {"user.ids.slug", "", "", "", false},
	"twitch":         {"data.0.id", "data.0.email", "", "data.0.profile_image_url", true},
	"twitter":        {"data.id", "data.confirmed_email", "", "data.profile_image_url", true},
	"vk":             {"response.0.id", "email", "", "response.0.photo_max", true},
	"wakatime":       {"data.id", "data.email", "data.is_email_confirmed", "data.photo", false},
	"yandex":         {"id", "default_email", "", "", true},
}

func normalizeProviderProfile(name string, raw map[string]json.RawMessage, accessToken string, provider *HTTPOAuthProvider) (OAuthIdentity, bool, error) {
	rule, ok := providerProfileRules[name]
	if !ok {
		return OAuthIdentity{}, false, nil
	}
	var profile any
	encoded, _ := json.Marshal(raw)
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&profile); err != nil {
		return OAuthIdentity{}, true, err
	}
	identity := OAuthIdentity{Subject: scalarString(pathValue(profile, rule.subject)), AvatarURL: scalarString(pathValue(profile, rule.avatar))}
	if identity.Subject == "" {
		return OAuthIdentity{}, true, errors.New("OAuth subject is invalid")
	}
	if name == "discord" {
		hash := scalarString(pathValue(profile, "avatar"))
		if hash != "" {
			identity.AvatarURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", identity.Subject, hash)
		}
	}
	if name == "yandex" {
		if id := scalarString(pathValue(profile, "default_avatar_id")); id != "" {
			identity.AvatarURL = "https://avatars.yandex.net/get-yapic/" + id + "/islands-200"
		}
	}
	email := scalarString(pathValue(profile, rule.email))
	verified := rule.trustedEmail || boolValue(pathValue(profile, rule.verified))
	if name == "kakao" {
		verified = verified && boolValue(pathValue(profile, "kakao_account.is_email_valid"))
	}
	if name == "github" || name == "gitea" || name == "bitbucket" {
		var err error
		email, err = provider.fetchVerifiedEmail(name, accessToken)
		if err != nil {
			return OAuthIdentity{}, true, err
		}
		verified = email != ""
	}
	if verified && email != "" {
		identity.Email, identity.EmailVerified = email, true
	}
	return identity, true, nil
}

func pathValue(value any, path string) any {
	if path == "" {
		return nil
	}
	for _, part := range strings.Split(path, ".") {
		switch node := value.(type) {
		case map[string]any:
			value = node[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			value = node[i]
		default:
			return nil
		}
	}
	return value
}

func scalarString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
	}
	return ""
}

func boolValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true") || v == "1"
	case float64:
		return v == 1
	}
	return false
}

func (p *HTTPOAuthProvider) fetchVerifiedEmail(name, token string) (string, error) {
	endpoint := ""
	switch name {
	case "github":
		u, _ := http.NewRequest(http.MethodGet, p.config.UserInfoURL, nil)
		if u != nil && u.URL != nil {
			endpoint = u.URL.Scheme + "://" + u.URL.Host + "/user/emails"
		}
	case "gitea":
		u, _ := http.NewRequest(http.MethodGet, p.config.UserInfoURL, nil)
		if u != nil && u.URL != nil {
			endpoint = u.URL.Scheme + "://" + u.URL.Host + "/api/v1/user/emails"
		}
	case "bitbucket":
		endpoint = "https://api.bitbucket.org/2.0/user/emails"
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var payload any
	if err := p.doJSON(req, &payload); err != nil {
		return "", err
	}
	items := payload
	if m, ok := payload.(map[string]any); ok {
		items = m["values"]
	}
	list, _ := items.([]any)
	for _, item := range list {
		m, _ := item.(map[string]any)
		if boolValue(m["verified"]) && (boolValue(m["primary"]) || name == "bitbucket") {
			return scalarString(m["email"]), nil
		}
	}
	return "", nil
}
