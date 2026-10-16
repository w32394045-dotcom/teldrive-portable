package api

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCookieSecureFromContext(t *testing.T) {
	t.Parallel()
	handler := &Handler{}
	insecure := handler.cookie(context.Background(), "name", "value", time.Minute).String()
	if strings.Contains(insecure, "Secure") {
		t.Fatalf("insecure cookie = %q", insecure)
	}
	secure := handler.cookie(WithCookieSecure(context.Background(), true), "name", "value", time.Minute).String()
	if !strings.Contains(secure, "Secure") {
		t.Fatalf("secure cookie = %q", secure)
	}
}

// TestCookiePersistsByDefault pins the behaviour every existing client already
// has: a request that says nothing about persistence gets a cookie that outlives
// the browser.
func TestCookiePersistsByDefault(t *testing.T) {
	t.Parallel()
	handler := &Handler{}
	for name, ctx := range map[string]context.Context{
		"nothing said":  context.Background(),
		"explicitly on": WithSessionPersistence(context.Background(), true),
	} {
		t.Run(name, func(t *testing.T) {
			cookie := handler.cookie(ctx, refreshCookieName, "value", 30*24*time.Hour)
			if cookie.MaxAge <= 0 {
				t.Fatalf("MaxAge = %d, want a persistent cookie", cookie.MaxAge)
			}
			if cookie.Expires.IsZero() {
				t.Fatal("a persistent cookie needs an expiry for browsers that ignore Max-Age")
			}
		})
	}
}

// TestCookieCanBeSessionOnly is the "do not keep me signed in" half: the browser
// drops the cookie when it closes, and the rendered header must carry no
// lifetime at all -- a Max-Age of zero is a deletion, not a session cookie.
func TestCookieCanBeSessionOnly(t *testing.T) {
	t.Parallel()
	handler := &Handler{}
	ctx := WithSessionPersistence(context.Background(), false)
	for _, name := range []string{accessCookieName, refreshCookieName} {
		cookie := handler.cookie(ctx, name, "value", 30*24*time.Hour)
		if cookie.MaxAge != 0 {
			t.Fatalf("%s: MaxAge = %d, want 0", name, cookie.MaxAge)
		}
		if !cookie.Expires.IsZero() {
			t.Fatalf("%s: Expires = %v, want none", name, cookie.Expires)
		}
		rendered := cookie.String()
		if strings.Contains(rendered, "Max-Age") || strings.Contains(rendered, "Expires") {
			t.Fatalf("%s: %q still carries a lifetime", name, rendered)
		}
		// It must still be a usable, protected cookie rather than an empty one.
		if !strings.Contains(rendered, "HttpOnly") || !strings.Contains(rendered, "Path=/") {
			t.Fatalf("%s: %q is missing its protections", name, rendered)
		}
	}
}
