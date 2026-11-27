package doubaowork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/caigee-cmd/cli2api/internal/providers"
)

const (
	checkinDetailPath = "/alice/ug/campaign/v3/detail/"
	checkinClaimPath  = "/alice/ug/campaign/landing/checkin"
	checkinDefaultDay = "00:00"
	// codeCheckinSDKRequired is returned when the upstream wants the web SDK
	// runtime for a claim. It is deliberately terminal for the automatic path:
	// retrying cannot supply that environment.
	codeCheckinSDKRequired = 710010702
)

// claimGuardEntry only needs the attempt time for diagnostics; the boolean
// existence is what prevents a second claim in the same process.
type claimGuardEntry struct {
	at time.Time
}

// CheckinStatus reads the daily check-in state without writing anything.
func (c *Client) CheckinStatus(ctx context.Context, accountID string) (providers.CheckinResult, error) {
	sess := c.sessionFor(accountID)
	if err := sess.ready(ctx); err != nil {
		return providers.CheckinResult{Status: "error", Message: checkinErrorText(err)}, err
	}
	group := sess.campaignGroupCode()
	if group == "" {
		return providers.CheckinResult{
			Status:  "skipped",
			Message: "未发现每日签到活动（launch 未返回 campaign_group_code）",
		}, nil
	}
	detail, err := sess.json(ctx, http.MethodGet, checkinDetailPath, nil, map[string]string{
		"campaign_group_code": group,
	})
	if err != nil {
		return providers.CheckinResult{Status: "error", Message: checkinErrorText(err)}, err
	}
	return checkinResultFromDetail(detail), nil
}

// Checkin claims the daily check-in at most once per provider day. It reads
// the upstream state first, reserves a persistent day slot, sends the claim,
// then reads the state again. Any non-auth failure is terminal for the day:
// "attempted" is intentionally stronger than "failed", because retrying a
// write whose outcome is unknown can double-claim or trip anti-bot controls.
func (c *Client) Checkin(ctx context.Context, accountID string) (providers.CheckinResult, error) {
	status, err := c.CheckinStatus(ctx, accountID)
	if err != nil {
		return status, err
	}
	if status.Status != "pending" {
		return status, nil
	}

	day := providers.CheckinCalendarNow(time.Now()).Format("2006-01-02")
	key := accountID + "|" + day
	if !c.beginLocalClaim(key) {
		return c.readAfterAttempt(ctx, accountID, "当日签到已尝试过，未重复领取")
	}

	claimed, _, err := c.store.BeginCheckinClaim(ctx, accountID, day)
	if err != nil {
		return c.pendingAfterAttempt(ctx, accountID, "签到台账不可用，已跳过领取，请人工核对")
	}
	if !claimed {
		return c.readAfterAttempt(ctx, accountID, "当日签到已占用，未重复领取")
	}

	group := c.sessionFor(accountID).campaignGroupCode()
	claim, err := c.claimCheckin(ctx, accountID, group)
	if err != nil {
		var providerErr *providers.Error
		if errors.As(err, &providerErr) && providerErr.Kind == "auth" {
			c.completeClaimBestEffort(ctx, accountID, day, "error", checkinErrorText(err))
			return providers.CheckinResult{Status: "error", Message: checkinErrorText(err)}, err
		}
		result := providers.CheckinResult{
			Status:  "pending",
			Message: "签到请求未完成，今日不再自动重试；请稍后在官网核对",
		}
		c.completeClaimBestEffort(ctx, accountID, day, result.Status, result.Message)
		return result, nil
	}

	result := c.resultAfterClaim(ctx, accountID, claim)
	c.completeClaimBestEffort(ctx, accountID, day, result.Status, result.Message)
	return result, nil
}

func (c *Client) claimCheckin(ctx context.Context, accountID, group string) (claimResponse, error) {
	sess := c.sessionFor(accountID)
	resp, err := sess.request(ctx, http.MethodPost, checkinClaimPath, map[string]any{"scene": group}, nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return claimResponse{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return claimResponse{}, err
	}
	var envelope struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return claimResponse{}, fmt.Errorf("decode checkin claim: %w", err)
	}
	code := firstCode(envelope.Code)
	if code == 0 && len(envelope.Data) > 0 {
		var data struct {
			Code      json.RawMessage `json:"code"`
			ErrorCode json.RawMessage `json:"error_code"`
		}
		if json.Unmarshal(envelope.Data, &data) == nil {
			code = firstCode(data.ErrorCode, data.Code)
		}
	}
	message := firstNonEmpty(envelope.Message, envelope.Msg)
	return claimResponse{Code: code, Message: message}, nil
}

type claimResponse struct {
	Code    int
	Message string
}

func (c *Client) resultAfterClaim(ctx context.Context, accountID string, claim claimResponse) providers.CheckinResult {
	switch claim.Code {
	case 0:
		// A zero code is not proof of landing; the daily flag is the authority.
	case codeCheckinSDKRequired:
		return providers.CheckinResult{
			Status:  "pending",
			Message: "签到接口要求网页 SDK 环境，已停止自动领取；请在官网手动签到一次",
		}
	case codeCaptcha:
		return providers.CheckinResult{
			Status:  "pending",
			Message: "豆包要求完成一次官网验证，自动签到已暂停",
		}
	default:
		return providers.CheckinResult{
			Status:  "pending",
			Message: fmt.Sprintf("签到接口返回错误码 %d，本次未重试", claim.Code),
		}
	}

	read, err := c.CheckinStatus(ctx, accountID)
	if err != nil {
		return providers.CheckinResult{
			Status:  "pending",
			Message: "领取请求已返回，但回查失败；今日不再自动重试",
		}
	}
	if read.Status == "already" {
		read.Status = "success"
		read.Message = "签到成功（回查已到账）"
	}
	if read.Status == "pending" {
		read.Message = "领取请求已返回，但回查仍显示未签到；今日不再自动重试"
	}
	return read
}

func (c *Client) readAfterAttempt(ctx context.Context, accountID, message string) (providers.CheckinResult, error) {
	read, err := c.CheckinStatus(ctx, accountID)
	if err != nil {
		return read, err
	}
	switch read.Status {
	case "already", "success":
		read.Message = "今日已签到，未重复领取"
	default:
		read.Status = "pending"
		read.Message = message
	}
	return read, nil
}

func (c *Client) pendingAfterAttempt(ctx context.Context, accountID, message string) (providers.CheckinResult, error) {
	read, err := c.CheckinStatus(ctx, accountID)
	if err != nil {
		return providers.CheckinResult{Status: "pending", Message: message}, nil
	}
	if read.Status == "already" || read.Status == "success" {
		read.Message = "今日已签到"
		return read, nil
	}
	return providers.CheckinResult{Status: "pending", Message: message}, nil
}

func (c *Client) beginLocalClaim(key string) bool {
	c.checkinMu.Lock()
	defer c.checkinMu.Unlock()
	if c.checkins == nil {
		c.checkins = map[string]claimGuardEntry{}
	}
	if _, ok := c.checkins[key]; ok {
		return false
	}
	c.checkins[key] = claimGuardEntry{at: time.Now()}
	return true
}

func (c *Client) completeClaimBestEffort(ctx context.Context, accountID, day, status, message string) {
	_ = c.store.CompleteCheckinClaim(ctx, accountID, day, status, message, time.Now())
}

func checkinResultFromDetail(detail map[string]any) providers.CheckinResult {
	task, ok := findDailyCheckin(detail)
	if !ok {
		return providers.CheckinResult{Status: "skipped", Message: "未发现每日签到任务"}
	}
	daily, _ := task["daily_checkin"].(map[string]any)
	checked, _ := boolOf(daily["checked_in_today"])
	streak, _ := numOf(daily["continuous_days"])
	reward := 0.0
	if rewardMap, ok := daily["reward"].(map[string]any); ok {
		reward, _ = numOf(rewardMap["reward_amount"])
	}
	if checked {
		return providers.CheckinResult{
			Status:          "already",
			Message:         "今日已签到",
			RewardCredits:   reward,
			Streak:          int(streak),
			EarliestCheckin: checkinDefaultDay,
		}
	}
	if state, ok := daily["can_checkin"]; ok {
		if can, ok := boolOf(state); ok && !can {
			return providers.CheckinResult{Status: "skipped", Message: "签到活动当前不可领取", Streak: int(streak), EarliestCheckin: checkinDefaultDay}
		}
	}
	taskStatus := 0.0
	if value, ok := numOf(task["task_status"]); ok {
		taskStatus = value
	}
	if taskStatus <= 0 {
		return providers.CheckinResult{Status: "skipped", Message: "签到任务当前不可领取", Streak: int(streak), EarliestCheckin: checkinDefaultDay}
	}
	return providers.CheckinResult{
		Status:          "pending",
		Message:         "上游显示今日未签到",
		Streak:          int(streak),
		EarliestCheckin: checkinDefaultDay,
	}
}

func findDailyCheckin(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		// The campaign payload puts the real task list under "tasks"; check it
		// before walking example/milestone subtrees so a sample task cannot win.
		if tasks, ok := typed["tasks"].([]any); ok {
			for _, child := range tasks {
				if task, ok := findDailyCheckin(child); ok {
					return task, true
				}
			}
		}
		if daily, ok := typed["daily_checkin"].(map[string]any); ok {
			if _, ok := daily["checked_in_today"]; ok {
				return typed, true
			}
		}
		for key, child := range typed {
			if key == "tasks" {
				continue
			}
			if task, ok := findDailyCheckin(child); ok {
				return task, true
			}
		}
	case []any:
		for _, child := range typed {
			if task, ok := findDailyCheckin(child); ok {
				return task, true
			}
		}
	}
	return nil, false
}

func boolOf(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

func checkinErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *session) campaignGroupCode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.campaignGroup
}
