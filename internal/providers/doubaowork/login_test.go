package doubaowork

import (
	"context"
	"testing"
)

func TestValidateLoginCookiesAcceptsUser(t *testing.T) {
	client, _ := newTestClient(t, identityHandler(map[string]any{"user_id_str": "42"}, map[string]any{}))
	err := client.validateLoginCookies(context.Background(), "acc1", []Cookie{{Name: "sessionid", Value: "fresh"}})
	if err != nil {
		t.Fatalf("valid cookie rejected: %v", err)
	}
}

func TestValidateLoginCookiesRejectsVisitor(t *testing.T) {
	client, _ := newTestClient(t, identityHandler(map[string]any{"is_visitor_account": true}, map[string]any{}))
	err := client.validateLoginCookies(context.Background(), "acc1", []Cookie{{Name: "sessionid", Value: "stale"}})
	if err == nil {
		t.Fatal("visitor cookie must be rejected")
	}
}
