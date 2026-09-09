package userauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const maximumOAuthAvatarBytes = 2 << 20

type OAuthAvatar struct {
	Content   []byte
	MediaType string
	Width     int
	Height    int
	SHA256    []byte
}

func (s *Service) importOAuthAvatar(ctx context.Context, userID uuid.UUID, raw string, allowLoopback bool) error {
	u, err := url.Parse(raw)
	if err != nil || !avatarURLAllowed(u, allowLoopback) {
		return ErrValidation
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !publicIP(ip) && !(allowLoopback && ip.IsLoopback()) {
				return nil, errors.New("avatar address is private")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}}
	client := &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if !avatarURLAllowed(req.URL, allowLoopback) {
			return errors.New("avatar redirect rejected")
		}
		return nil
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return errors.New("avatar response rejected")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximumOAuthAvatarBytes+1))
	if err != nil || len(content) == 0 || len(content) > maximumOAuthAvatarBytes {
		return errors.New("avatar size rejected")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return errors.New("avatar image rejected")
	}
	media := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif"}[format]
	if media == "" || !strings.HasPrefix(response.Header.Get("Content-Type"), media) {
		return errors.New("avatar media type rejected")
	}
	hash := sha256.Sum256(content)
	_, err = s.pool.Exec(ctx, `insert into pgbase_meta.user_oauth_avatars(user_id,content,media_type,width,height,sha256) values($1,$2,$3,$4,$5,$6) on conflict(user_id) do nothing`, userID, content, media, config.Width, config.Height, hash[:])
	return err
}

func avatarURLAllowed(u *url.URL, allowLoopback bool) bool {
	if u == nil || u.User != nil || u.Fragment != "" || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return allowLoopback && u.Scheme == "http" && localSMTPHostForOAuth(u.Hostname())
}
func localSMTPHostForOAuth(host string) bool {
	ip := net.ParseIP(host)
	return strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") || ip != nil && ip.IsLoopback()
}
func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast())
}

func (s *Service) GetOAuthAvatar(ctx context.Context, userID uuid.UUID) (OAuthAvatar, error) {
	var a OAuthAvatar
	err := s.pool.QueryRow(ctx, `select content,media_type,width,height,sha256 from pgbase_meta.user_oauth_avatars where user_id=$1`, userID).Scan(&a.Content, &a.MediaType, &a.Width, &a.Height, &a.SHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthAvatar{}, ErrNotFound
	}
	if err != nil {
		return OAuthAvatar{}, err
	}
	return a, nil
}
