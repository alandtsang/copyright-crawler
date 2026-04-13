package browser

import (
	"context"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type Credentials struct {
	LoginURL string
	Username string
	Password string
}

type Options struct {
	Headless     bool
	WindowWidth  int
	WindowHeight int
	LoginTimeout time.Duration
}

type Session struct {
	Token            string
	GatewayHeaders   map[string]string
	CookieHeader     string
	AuthorizationKey string
}

func StartContext(parent context.Context, opts Options) (context.Context, func(), error) {
	_ = opts
	ctx, cancel := chromedp.NewContext(parent)
	return ctx, cancel, nil
}

func LoginAndCollect(ctx context.Context, creds Credentials) (string, map[string]string, error) {
	_ = ctx
	_ = creds
	return "", map[string]string{}, nil
}

func TryRunUIFlow(ctx context.Context) error {
	_ = ctx
	return nil
}

func CollectCookies(ctx context.Context, urls []string) ([]*network.Cookie, error) {
	_ = ctx
	_ = urls
	return nil, nil
}

func BuildSession(token string, gatewayHeaders map[string]string, cookies []*network.Cookie) Session {
	_ = cookies
	return Session{
		Token:          token,
		GatewayHeaders: gatewayHeaders,
	}
}
