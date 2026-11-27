package doubaowork

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caigee-cmd/cli2api/internal/providers"
)

// loginState tracks one short-lived Playwright login round. The browser runs
// only during login and exits as soon as cookies are exported; it is never
// kept alive per account or used for chat.
type loginState struct {
	mu        sync.Mutex
	startedAt time.Time
	done      chan struct{}
	cookies   []Cookie
	err       error
	message   string
}

type loginOutput struct {
	Cookies       []Cookie
	ClientInfo    ClientInfo
	ClientVersion string
}

const (
	loginAttemptTimeout = 180 * time.Second
	headlessLoginProbe  = 8 * time.Second
	interactiveLogin    = 158 * time.Second
)

// StartLogin launches the short-lived Playwright helper against a persistent
// Chrome profile. The operator signs in inside the Chrome window the helper
// opens; PollLogin waits for the exported session cookie and saves it as a
// doubaowork-cookie-v1 credential.
func (c *Client) StartLogin(ctx context.Context, accountID string) (providers.LoginSession, error) {
	c.loginMu.Lock()
	if c.loginScript == "" {
		c.loginMu.Unlock()
		return providers.LoginSession{}, fmt.Errorf("doubaowork browser login is not configured; paste the Cookie instead")
	}
	node := c.nodeBinary
	script := c.loginScript
	profileDir := c.profileDir
	chrome := c.chromePath
	if profileDir != "" {
		profileDir = filepath.Join(profileDir, sanitizeProfileID(accountID))
	}
	c.loginMu.Unlock()

	if profileDir != "" {
		if err := os.MkdirAll(profileDir, 0o700); err != nil {
			return providers.LoginSession{}, fmt.Errorf("create login profile: %w", err)
		}
	}

	state := &loginState{startedAt: time.Now(), done: make(chan struct{})}
	c.loginMu.Lock()
	if existing := c.logins[accountID]; existing != nil {
		select {
		case <-existing.done:
		default:
			c.loginMu.Unlock()
			return providers.LoginSession{}, fmt.Errorf("doubaowork login is already in progress")
		}
	}
	if c.logins == nil {
		c.logins = map[string]*loginState{}
	}
	c.logins[accountID] = state
	c.loginMu.Unlock()

	if node == "" {
		node = "node"
	}
	go c.runLogin(node, script, profileDir, chrome, accountID, state)

	return providers.LoginSession{
		AuthURL: BaseURL + "/chat/",
		State:   accountID,
	}, nil
}

// runLogin runs headless first (fast path when the profile already holds a
// valid session) and falls back to a visible window for interactive sign-in.
func (c *Client) runLogin(node, script, profileDir, chrome, accountID string, state *loginState) {
	defer close(state.done)
	ctx, cancel := context.WithTimeout(context.Background(), loginAttemptTimeout)
	defer cancel()
	staleProfile := false

	for _, headless := range []bool{true, false} {
		timeout := headlessLoginProbe
		if !headless {
			timeout = interactiveLogin
		}
		clearCookies := !headless && staleProfile
		attempt, err := runLoginAttempt(ctx, node, script, profileDir, chrome, headless, timeout, clearCookies)
		if err == nil && len(attempt.Cookies) > 0 {
			if verifyErr := c.validateLoginCookies(ctx, accountID, attempt.Cookies); verifyErr != nil {
				if headless {
					// The persistent profile holds an expired session. Clear it
					// before opening the visible window so the operator can
					// sign in again instead of reusing a dead Cookie.
					staleProfile = true
					state.setMessage("已保存的登录已失效，正在打开 Chrome 重新登录…")
					continue
				}
				state.setResult(nil, verifyErr, "")
				return
			}
			if err := c.saveLoginCookies(ctx, accountID, attempt); err != nil {
				state.setResult(nil, err, "")
				return
			}
			state.setResult(attempt.Cookies, nil, "登录成功")
			return
		}
		// Headless miss is expected on a cold profile; go interactive.
		if headless {
			state.setMessage("请在弹出的 Chrome 窗口中登录豆包（扫码或手机号）")
			continue
		}
		if err != nil {
			state.setResult(nil, err, "")
		} else {
			state.setResult(nil, fmt.Errorf("doubaowork login returned no cookies"), "")
		}
	}
}

// runLoginAttempt executes one Playwright login attempt and decodes its JSON.
func runLoginAttempt(ctx context.Context, node, script, profileDir, chrome string, headless bool, timeout time.Duration, clearCookies bool) (loginOutput, error) {
	args := []string{
		script, "--user-data-dir", profileDir,
		"--headless", boolFlag(headless),
		"--timeout", strconv.Itoa(int(timeout / time.Millisecond)),
	}
	if clearCookies {
		args = append(args, "--clear-cookies", "1")
	}
	if chrome != "" {
		args = append(args, "--chrome", chrome)
	}
	cmd := exec.CommandContext(ctx, node, args...)
	out, err := cmd.Output()
	if err != nil {
		return loginOutput{}, fmt.Errorf("login helper: %w", err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return loginOutput{}, fmt.Errorf("login helper returned empty output")
	}
	var result struct {
		OK            bool       `json:"ok"`
		Error         string     `json:"error"`
		Message       string     `json:"message"`
		Cookies       []Cookie   `json:"cookies"`
		ClientInfo    ClientInfo `json:"client_info"`
		ClientVersion string     `json:"client_version"`
	}
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		return loginOutput{}, fmt.Errorf("decode login helper output: %w", err)
	}
	if !result.OK {
		return loginOutput{}, fmt.Errorf("login helper: %s: %s", result.Error, result.Message)
	}
	return loginOutput{
		Cookies:       result.Cookies,
		ClientInfo:    result.ClientInfo,
		ClientVersion: result.ClientVersion,
	}, nil
}

// PollLogin reports login progress without blocking: the console polls every
// couple of seconds. On success the credential is already saved by runLogin,
// so returning done=true lets the console start the account.
func (c *Client) PollLogin(ctx context.Context, accountID string) (bool, string, error) {
	state := c.loginStateOf(accountID)
	if state == nil {
		return false, "", fmt.Errorf("login not started for account %s", accountID)
	}
	select {
	case <-state.done:
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.err != nil {
			return false, state.message, state.err
		}
		return true, state.message, nil
	default:
		state.mu.Lock()
		message := state.message
		state.mu.Unlock()
		if message == "" {
			message = "等待在 Chrome 窗口中登录豆包…"
		}
		return false, message, nil
	}
}

func (c *Client) saveLoginCookies(ctx context.Context, accountID string, attempt loginOutput) error {
	cred := Credential{
		Cookies:       attempt.Cookies,
		ClientInfo:    attempt.ClientInfo,
		ClientVersion: attempt.ClientVersion,
	}
	encoded, err := cred.Encode()
	if err != nil {
		return err
	}
	if err := c.store.SaveCredentialPayload(ctx, accountID, CredentialFormat, encoded); err != nil {
		return err
	}
	c.resetSession(accountID)
	return nil
}

// validateLoginCookies performs the passport identity call before saving a
// browser-login result. A persistent Chrome profile can still hold a stale
// sessionid; without this check the helper would report success and the account
// would fail on its first real request.
func (c *Client) validateLoginCookies(ctx context.Context, accountID string, cookies []Cookie) error {
	sess := &session{
		client:     c,
		accountID:  accountID,
		jar:        newCookieJar(cookies),
		clientInfo: ClientInfoFromCookies(cookies),
	}
	identity, err := sess.json(ctx, http.MethodGet, "/passport/account/info/v2/", nil, map[string]string{
		"account_sdk_source": "web",
		"sdk_version":        "2.2.10",
	})
	if err != nil {
		return err
	}
	uid := strings.TrimSpace(firstNonEmpty(asString(identity["user_id_str"]), asString(identity["user_id"])))
	if uid == "" || uid == "0" || asBool(identity["is_visitor_account"]) {
		return newCodeError(codeLoginExpired, "doubaowork login is invalid; the saved session has expired, sign in again")
	}
	return nil
}

func (c *Client) loginStateOf(accountID string) *loginState {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.logins == nil {
		return nil
	}
	return c.logins[accountID]
}

func (s *loginState) setResult(cookies []Cookie, err error, message string) {
	s.mu.Lock()
	s.cookies, s.err, s.message = cookies, err, message
	s.mu.Unlock()
}

func (s *loginState) setMessage(message string) {
	s.mu.Lock()
	s.message = message
	s.mu.Unlock()
}

func boolFlag(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func sanitizeProfileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
