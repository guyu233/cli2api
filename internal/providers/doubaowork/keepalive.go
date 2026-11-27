package doubaowork

import (
	"context"
	"fmt"

	"github.com/caigee-cmd/cli2api/internal/accounts"
	"github.com/caigee-cmd/cli2api/internal/providers"
)

// Keepalive performs a health check for one account and persists any cookies
// the upstream refreshed during that check. Doubao does not slide sessionid on
// ordinary requests, so this cannot extend a session past its sid_guard
// expiry; it does, however, persist sliding device cookies (ttwid) and any
// Set-Cookie the upstream does issue, so a gateway restart no longer falls
// back to the exact cookies captured at import time.
//
// It is registered with the runtime maintenance sweep via
// Manager.SetSessionKeepalive("doubaowork", client).
func (c *Client) Keepalive(ctx context.Context, accountID string) error {
	sess := c.sessionFor(accountID)
	if err := sess.ready(ctx); err != nil {
		if typed, ok := err.(*providers.Error); ok && typed.Kind == "auth" {
			_ = c.store.Observe(ctx, accountID, "", "login_required", typed.Message, accounts.KindAuth)
		}
		return err
	}

	cookies := sess.snapshotCookies()
	if len(cookies) == 0 {
		return fmt.Errorf("doubaowork keepalive: no cookies to persist")
	}

	// Preserve the confirmed identity/bot and device info from the live session
	// while refreshing the cookie set.
	sess.mu.Lock()
	cred := Credential{
		UID:           sess.uid,
		Bot:           sess.bot,
		ClientInfo:    sess.clientInfo,
		ClientVersion: sess.clientVersion,
		Cookies:       cookies,
	}
	sess.mu.Unlock()

	encoded, err := cred.Encode()
	if err != nil {
		return err
	}
	if err := c.store.SaveCredentialPayload(ctx, accountID, CredentialFormat, encoded); err != nil {
		return err
	}
	_ = c.store.Observe(ctx, accountID, sess.uid, "ready", "", "")
	return nil
}
