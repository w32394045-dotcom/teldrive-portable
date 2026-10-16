package api

import (
	"context"
	"net/http"
	"time"

	"github.com/tgdrive/teldrive/v2/internal/api/gen"
	"github.com/tgdrive/teldrive/v2/internal/authn"
)

const (
	accessCookieName  = "teldrive_access"
	refreshCookieName = "teldrive_refresh"
)

type cookieSecureContextKey struct{}

func WithCookieSecure(ctx context.Context, secure bool) context.Context {
	return context.WithValue(ctx, cookieSecureContextKey{}, secure)
}

func cookieSecure(ctx context.Context) bool {
	secure, _ := ctx.Value(cookieSecureContextKey{}).(bool)
	return secure
}

type sessionPersistContextKey struct{}

// WithSessionPersistence carries the "keep me signed in" choice from the request
// to the code that issues the cookies.
//
// It travels in the context rather than as a handler argument because the login
// handlers are generated from the TypeSpec contract, and adding a field there
// would mean regenerating the whole API surface for one checkbox. A middleware
// reads the request and injects it, exactly as the secure-cookie flag does.
func WithSessionPersistence(ctx context.Context, persist bool) context.Context {
	return context.WithValue(ctx, sessionPersistContextKey{}, persist)
}

// sessionPersists reports whether the session cookies should outlive the
// browser. The zero value is true: a client that says nothing gets the
// persistent session, which is what a personal drive on its own machine wants,
// and what every other client already had.
func sessionPersists(ctx context.Context) bool {
	persist, ok := ctx.Value(sessionPersistContextKey{}).(bool)
	if !ok {
		return true
	}
	return persist
}

func (h *Handler) CookieTelegramLoginVerifyCode(ctx context.Context, req *gen.TelegramCodeVerifyRequest, _ gen.CookieTelegramLoginVerifyCodeParams) (gen.CookieTelegramLoginVerifyCodeRes, error) {
	if h.Auth == nil || req == nil {
		return nil, mapServiceError(ErrOperationUnavailable)
	}
	result, err := h.Auth.VerifyCode(ctx, googleUUID(req.FlowId), req.Code)
	if err != nil {
		return nil, mapServiceError(err)
	}
	if result.Flow != nil {
		response := loginFlowResponse(result.Flow)
		return &response, nil
	}
	return h.cookieSessionResponse(ctx, result.Tokens)
}

func (h *Handler) CookieTelegramLoginVerifyPassword(ctx context.Context, req *gen.TelegramPasswordVerifyRequest, _ gen.CookieTelegramLoginVerifyPasswordParams) (gen.CookieTelegramLoginVerifyPasswordRes, error) {
	if h.Auth == nil || req == nil {
		return nil, mapServiceError(ErrOperationUnavailable)
	}
	result, err := h.Auth.VerifyPassword(ctx, googleUUID(req.FlowId), req.Password)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return h.cookieSessionResponse(ctx, result.Tokens)
}

func (h *Handler) CookieTelegramQRLoginPoll(ctx context.Context, req *gen.TelegramQRLoginPollRequest, _ gen.CookieTelegramQRLoginPollParams) (gen.CookieTelegramQRLoginPollRes, error) {
	if h.Auth == nil || req == nil {
		return nil, mapServiceError(ErrOperationUnavailable)
	}
	result, err := h.Auth.PollQR(ctx, googleUUID(req.FlowId))
	if err != nil {
		return nil, mapServiceError(err)
	}
	if result.QRFlow != nil {
		response := qrLoginFlowResponse(result.QRFlow)
		return &response, nil
	}
	return h.cookieSessionResponse(ctx, result.Tokens)
}

func (h *Handler) RefreshCookieSession(ctx context.Context, params gen.RefreshCookieSessionParams) (gen.RefreshCookieSessionRes, error) {
	if h.Auth == nil {
		return nil, mapServiceError(ErrOperationUnavailable)
	}
	tokens, err := h.Auth.Refresh(ctx, params.TeldriveRefresh)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return h.cookieSessionResponse(ctx, tokens)
}

func (h *Handler) LogoutCookieSession(ctx context.Context) (gen.LogoutCookieSessionRes, error) {
	if h.Auth == nil {
		return nil, mapServiceError(ErrOperationUnavailable)
	}
	identity, ok := IdentityFromContext(ctx)
	if !ok {
		return nil, mapServiceError(ErrUnauthenticated)
	}
	if err := h.Auth.Logout(ctx, identity); err != nil {
		return nil, mapServiceError(err)
	}
	return &gen.LogoutCookieSessionNoContent{SetCookie: h.expiredCookies(ctx)}, nil
}

func (h *Handler) cookieSessionResponse(ctx context.Context, tokens *authn.TokenPair) (*gen.CookieSessionHeaders, error) {
	if h == nil || h.Auth == nil || tokens == nil || tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.ExpiresIn <= 0 {
		return nil, ErrOperationUnavailable
	}
	now := time.Now().UTC()
	accessTTL := time.Duration(tokens.ExpiresIn) * time.Second
	refreshTTL := h.Auth.RefreshTokenTTL()
	return &gen.CookieSessionHeaders{
		SetCookie: []string{
			h.cookie(ctx, accessCookieName, tokens.AccessToken, accessTTL).String(),
			h.cookie(ctx, refreshCookieName, tokens.RefreshToken, refreshTTL).String(),
		},
		Response: gen.CookieSession{
			Authenticated: gen.CookieSessionAuthenticatedTrue,
			ExpiresAt:     now.Add(accessTTL),
		},
	}, nil
}

func (h *Handler) cookie(ctx context.Context, name, value string, ttl time.Duration) *http.Cookie {
	maxAge := int(ttl / time.Second)
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieSecure(ctx),
		SameSite: http.SameSiteLaxMode,
	}
	if sessionPersists(ctx) {
		cookie.MaxAge = maxAge
		cookie.Expires = time.Now().UTC().Add(ttl)
	}
	// Without MaxAge and Expires the browser keeps the cookie for the browsing
	// session and drops it when it closes, which is the whole meaning of "do not
	// keep me signed in". The server-side session still lives for its full
	// lifetime: this decides how long the browser holds the key, not how long
	// the key is valid.
	return cookie
}

func (h *Handler) expiredCookies(ctx context.Context) []string {
	expires := time.Unix(1, 0).UTC()
	cookies := make([]string, 0, 2)
	for _, name := range []string{accessCookieName, refreshCookieName} {
		cookies = append(cookies, (&http.Cookie{
			Name:     name,
			Path:     "/",
			MaxAge:   -1,
			Expires:  expires,
			HttpOnly: true,
			Secure:   cookieSecure(ctx),
			SameSite: http.SameSiteLaxMode,
		}).String())
	}
	return cookies
}
