package op

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"github.com/looplj/axonhub/llm/httpclient"
)

// GroupItemTestOutcome 分组内单个模型项的对话测试结果。
type GroupItemTestOutcome struct {
	ItemID    int    `json:"item_id"`
	ChannelID int    `json:"channel_id"`
	ModelName string `json:"model_name"`
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Detail    string `json:"detail"`
	Preview   string `json:"preview"`
}

// TestGroupItemChannel 向上游渠道发送一次极小的对话请求，判定指定模型能否正常对话（返回结果，不改数据）。
func TestGroupItemChannel(ctx context.Context, channel *model.Channel, modelName string) GroupItemTestOutcome {
	out := GroupItemTestOutcome{ChannelID: channel.ID, ModelName: modelName}
	// 注意：不能复用 helper.ChannelHttpClient（helper→client→op 存在导入环），
	// 探测请求直接使用带上下文超时的普通客户端。
	client := &http.Client{}

	base := strings.TrimRight(channel.BaseURL, "/")
	var url string
	var body []byte
	switch channel.Type {
	case model.ChannelProviderAnthropic:
		url = base + "/messages"
		body, _ = json.Marshal(map[string]any{
			"model":      modelName,
			"max_tokens": 200,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
		})
	default:
		url = base + "/chat/completions"
		body, _ = json.Marshal(map[string]any{
			"model":      modelName,
			"max_tokens": 200,
			"stream":     false,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
		})
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		out.Detail = err.Error()
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// 默认使用浏览器 UA，规避 Cloudflare 对 Go/axonhub 机器人 UA 的 403 拦截；
	// 下方渠道自定义 Header 仍可覆盖。
	req.Header.Set("User-Agent", model.DefaultUserAgent)
	if channel.Type == model.ChannelProviderAnthropic {
		req.Header.Set("x-api-key", channel.Key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	}
	// 应用渠道自定义 Header（跳过已设置的敏感 Header，避免覆盖鉴权）
	for _, h := range channel.CustomHeader {
		if req.Header.Get(h.HeaderKey) != "" && httpclient.IsSensitiveHeader(h.HeaderKey) {
			continue
		}
		req.Header.Set(h.HeaderKey, h.HeaderValue)
	}

	resp, err := client.Do(req)
	out.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		out.Detail = truncateStr(err.Error(), 200)
		return out
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

	if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
		out.Detail = parseErrDetail(resp.StatusCode, raw)
		return out
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		out.Detail = "对话接口未返回 JSON: " + truncateStr(string(raw), 120)
		return out
	}
	if parsed.Error != nil {
		out.Detail = truncateStr(parsed.Error.Message, 200)
		return out
	}
	if len(parsed.Choices) == 0 {
		out.Detail = "对话接口缺少 choices 字段"
		return out
	}
	msg := parsed.Choices[0].Message
	if msg.Content == "" && msg.Reasoning == "" {
		out.Detail = "对话正常但无内容/推理文本"
		return out
	}
	out.OK = true
	out.Preview = truncateStr(msg.Content, 120)
	if out.Preview == "" {
		out.Preview = "（推理模型）" + truncateStr(msg.Reasoning, 120)
	}
	return out
}

// TestGroupItemsAndPrune 测试分组内所有项；失败的项从分组移除，仅在成功项上保留负载均衡。
// 返回每个项的结果、保留数、移除数。
func TestGroupItemsAndPrune(ctx context.Context, groupID int) ([]GroupItemTestOutcome, int, int, error) {
	group, err := GroupGet(groupID, ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	results := make([]GroupItemTestOutcome, 0, len(group.Items))
	var deleteIDs []int
	for _, item := range group.Items {
		channel, err := ChannelGet(item.ChannelID, ctx)
		if err != nil {
			continue
		}
		res := TestGroupItemChannel(ctx, channel, item.ModelName)
		res.ItemID = item.ID
		results = append(results, res)
		if !res.OK {
			deleteIDs = append(deleteIDs, item.ID)
		}
	}
	if len(deleteIDs) > 0 {
		if _, err := GroupUpdate(&model.GroupUpdateRequest{ID: groupID, ItemsToDelete: deleteIDs}, ctx); err != nil {
			return results, 0, 0, err
		}
	}
	kept := len(results) - len(deleteIDs)
	return results, kept, len(deleteIDs), nil
}

// TestAllGroupsAndPrune 测试并收敛全部分组的负载均衡池（失败项移出）。供定时任务/批量调用。
func TestAllGroupsAndPrune(ctx context.Context) error {
	groups, err := GroupList(ctx)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if len(g.Items) == 0 {
			continue
		}
		_, kept, removed, err := TestGroupItemsAndPrune(ctx, g.ID)
		if err != nil {
			log.Warnf("group health: group=%s err=%v", g.Name, err)
			continue
		}
		log.Infof("group health: tested=%d kept=%d removed=%d (group=%s)",
			len(g.Items), kept, removed, g.Name)
	}
	return nil
}

func parseErrDetail(code int, raw []byte) string {
	var e struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != nil && e.Error.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", code, truncateStr(e.Error.Message, 180))
	}
	return fmt.Sprintf("HTTP %d: %s", code, truncateStr(string(raw), 120))
}

func truncateStr(s string, n int) string {
	if n < 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}