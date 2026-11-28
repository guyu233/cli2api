package control

import (
	"context"
	"testing"

	"github.com/caigee-cmd/cli2api/internal/accounts"
	"github.com/caigee-cmd/cli2api/internal/providers"
)

// qrStubLogin is a scan-to-login provider: it never opens a window, it publishes
// a PNG frame that the console paints instead.
type qrStubLogin struct {
	stubLogin
	png []byte
}

func (q *qrStubLogin) LoginQR(context.Context, string) ([]byte, error) { return q.png, nil }

func TestAccountAdminLoginStatusExposesQR(t *testing.T) {
	svc, _, store, _ := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Provider: "testprovider"}
	login := &qrStubLogin{
		stubLogin: stubLogin{pollDone: false, pollMsg: "scan the code"},
		png:       []byte("png-bytes"),
	}
	svc.Accounts.Providers = providers.NewRegistry()
	svc.Accounts.Providers.Register(providers.Adapter{ID: "testprovider", Login: login})

	result, err := svc.Accounts.Admin(context.Background(), AccountAdminAction{AccountID: "acc-1", Action: "login/status"})
	if err != nil {
		t.Fatal(err)
	}
	if result.LoginStatus != "pending" || result.LoginMsg != "scan the code" {
		t.Fatalf("status=%q msg=%q", result.LoginStatus, result.LoginMsg)
	}
	if string(result.LoginQR) != "png-bytes" {
		t.Fatalf("qr=%q", result.LoginQR)
	}
}

func TestAccountAdminLoginStatusNoQRWithoutProviderSupport(t *testing.T) {
	svc, _, store, _ := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Provider: "trae"}
	login := &stubLogin{pollDone: false, pollMsg: "waiting"}
	svc.Accounts.Providers = providers.NewRegistry()
	svc.Accounts.Providers.Register(providers.Adapter{ID: "trae", Login: login})

	result, err := svc.Accounts.Admin(context.Background(), AccountAdminAction{AccountID: "acc-1", Action: "login/status"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.LoginQR) != 0 {
		t.Fatalf("unexpected qr for non-QR provider: %q", result.LoginQR)
	}
}

func TestAccountAdminLoginStatusDropsQRWhenDone(t *testing.T) {
	svc, _, store, _ := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Provider: "testprovider"}
	login := &qrStubLogin{
		stubLogin: stubLogin{pollDone: true, pollMsg: "login complete"},
		png:       []byte("png-bytes"),
	}
	svc.Accounts.Providers = providers.NewRegistry()
	svc.Accounts.Providers.Register(providers.Adapter{ID: "testprovider", Login: login})

	result, err := svc.Accounts.Admin(context.Background(), AccountAdminAction{AccountID: "acc-1", Action: "login/status"})
	if err != nil {
		t.Fatal(err)
	}
	if result.LoginStatus != "ok" || len(result.LoginQR) != 0 {
		t.Fatalf("status=%q qr=%q", result.LoginStatus, result.LoginQR)
	}
}
