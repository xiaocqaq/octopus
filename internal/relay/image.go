package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/tidwall/sjson"
)

// imageMaxAttempts 限定单个图片请求的最大上游尝试轮次。
const imageMaxAttempts = 6

// ForwardImage 承载 /v1/images/generations 与 /v1/images/edits 的转发。
// added 20260908: axonhub llm 库没有图片接口的 APIFormat, 图片接口也没有跨协议
// 转换的余地, 故不复用 Forward 的转换管线, 只做原始正文透传, 但沿用同一套
// 分组选路, 授权校验, 冷却重试, 模型名改写与渠道/模型/凭据统计。
// 目标地址取渠道 BaseURL 拼 /v1/images/{generations,edits}; 仅 OpenAI Chat 协议
// 位的授权参与选路, 因为图片接口是 OpenAI 家族的私有扩展。
func ForwardImage(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		contentType := c.Request.Header.Get("Content-Type")
		isMultipart := strings.HasPrefix(contentType, "multipart/form-data")

		// 客户端请求的模型名即分组名; JSON 与 multipart 两种正文分别取。
		var groupName string
		var body []byte
		if isMultipart {
			raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 256<<20))
			if err != nil {
				imageReject(c, err)
				return
			}
			body = raw
			// 复用 multipart 解析只为读出 model 字段, 正文本身仍按原始字节透传。
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
			if err := c.Request.ParseMultipartForm(64 << 20); err != nil {
				imageReject(c, fmt.Errorf("parse multipart form: %w", err))
				return
			}
			groupName = strings.TrimSpace(c.Request.FormValue("model"))
		} else {
			raw, err := httpclient.ReadHTTPRequest(c.Request)
			if err != nil {
				imageReject(c, err)
				return
			}
			var metadata struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(raw.Body, &metadata); err != nil {
				imageReject(c, err)
				return
			}
			groupName = strings.TrimSpace(metadata.Model)
			body = raw.Body
		}
		if groupName == "" {
			imageReject(c, errors.New("model is required"))
			return
		}

		// API Key 限定了模型范围时只放行范围内的模型, 为空表示不限制。
		if allowed, ok := c.Get("supported_models"); ok {
			if names, _ := allowed.([]string); len(names) > 0 && !slices.Contains(names, groupName) {
				imageReject(c, errors.New("model not supported by this api key"))
				return
			}
		}

		group, err := op.GroupGetByName(groupName)
		if err != nil {
			imageReject(c, errors.New("model not found"))
			return
		}

		request := newRequestState(c.Request.Context(), groupName, group.ID, model.ProtocolOpenAIImage, string(body), c.GetInt("api_key_id"))
		ctx := c.Request.Context()
		failedItemID := 0
		failures := 0
		// 图片请求没有流式首包可作为“已开始”的信号, 客户端只能干等, 故限定尝试轮次:
		// 全部成员都不可用时要尽快以错误收敛, 而不是像聊天转发那样无限等待配置修好。
		attempts := 0

		for {
			if ctx.Err() != nil {
				request.markCanceled(ctx.Err(), "", nil)
				return
			}
			attempts++
			if attempts > imageMaxAttempts {
				err := errors.New("no available upstream for image request")
				request.markFailed(err, "", nil)
				imageReject(c, err)
				return
			}

			group, err = op.GroupGetByName(groupName)
			if err != nil {
				if !request.wait(ctx, model.DefaultGroupRelayConfig().MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			item := pickGroupItem(group)
			if item.ID == 0 {
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			grant, err := op.ChannelGrantGet(item.ChannelGrantID)
			if err != nil {
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			channelModel := grant.ChannelModel
			channelKey := grant.ChannelKey
			if channelModel == nil || channelKey == nil {
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			channel, err := op.ChannelGet(channelModel.ChannelID)
			if err != nil {
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			// 图片接口现已是一个独立协议位, 只有登记了它的授权才参与选路: 上游是否提供 /v1/images/*
			// 与它支持哪种聊天协议无关, 由用户在渠道的授权矩阵上明确勾选。
			// 未登记该协议位的成员直接打入冷却让位给下一个成员, 且一次即冷却而不消耗尝试次数:
			// 这是配置事实而非偶发失败, 重试同一个成员不会有别的结果。
			// 分组内没有任何成员登记时, 尝试轮次很快耗尽并以错误收敛。
			if grant.Protocols&model.ProtocolOpenAIImage == 0 {
				recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
				continue
			}

			// JSON 正文按分组成员配置改写真实模型名; multipart 正文原样透传。
			roundBody := body
			if !isMultipart {
				roundBody, err = sjson.SetBytes(body, "model", channelModel.Name)
				if err != nil {
					request.markFailed(err, "", nil)
					imageReject(c, err)
					return
				}
			}

			roundCtx, cancelRound := context.WithCancel(ctx)
			request.startRound(cancelRound, channel.Name, channelModel.Name, model.ProtocolOpenAIImage)
			roundStartedAt := time.Now()

			httpClient, closeIdle, err := resolveUpstreamClient(channel)
			if err != nil {
				cancelRound()
				request.finishRound(err.Error())
				request.markFailed(err, "", nil)
				imageReject(c, err)
				return
			}

			response, err := sendImageUpstream(roundCtx, httpClient, channel, *channelKey, contentType, roundBody, kind)
			roundWaitTime := time.Since(roundStartedAt).Milliseconds()

			if err != nil {
				cancelRound()
				if closeIdle != nil {
					closeIdle()
				}
				request.finishRound(err.Error())
				if ctx.Err() != nil {
					request.markCanceled(ctx.Err(), "", nil)
					return
				}
				metrics := model.StatsMetrics{WaitTime: roundWaitTime, RequestFailed: 1}
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
				// 同一成员连续失败才累加计数, 换成员后重新计数。
				if failedItemID == item.ID {
					failures++
				} else {
					failedItemID = item.ID
					failures = 1
				}
				recordRouteFailure(group, item.ID, failures)
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			// 取得可提交响应: 记成功, 落统计, 回给客户端。
			request.finishRound("")
			recordRouteSuccess(group, item.ID)
			metrics := model.StatsMetrics{WaitTime: roundWaitTime, RequestSuccess: 1}
			_ = op.ChannelStatsUpdate(channel.ID, metrics)
			_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
			_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
			request.markCommitted()

			for key, values := range response.Header {
				if strings.EqualFold(key, "Content-Length") ||
					strings.EqualFold(key, "Transfer-Encoding") ||
					strings.EqualFold(key, "Connection") {
					continue
				}
				c.Writer.Header()[key] = values
			}
			if c.Writer.Header().Get("Content-Type") == "" {
				c.Header("Content-Type", "application/json")
			}
			c.Writer.WriteHeader(response.StatusCode)

			// 上游可能以 SSE 分片下发图片, 边读边写以免整份缓存在内存里。
			written, copyErr := io.Copy(c.Writer, response.Body)
			response.Body.Close()
			cancelRound()
			if closeIdle != nil {
				closeIdle()
			}
			if copyErr != nil {
				if ctx.Err() != nil {
					request.markCanceled(ctx.Err(), "", nil)
				} else {
					request.markFailed(copyErr, "", nil)
				}
				return
			}
			// 图片正文可达数 MB, 只登记大小而不留全文, 避免请求状态占满内存。
			request.markSucceeded(fmt.Sprintf("<image response %d bytes>", written), nil)
			return
		}
	}
}

// sendImageUpstream 按渠道配置向上游图片接口发起一次请求, 4xx/5xx 视为本轮失败。
func sendImageUpstream(ctx context.Context, client *http.Client, channel model.Channel, key model.ChannelKey, contentType string, body []byte, kind string) (*http.Response, error) {
	// BaseURL 以 ## 结尾表示地址已完整, 只取到末段目录, 不再拼渠道配置的协议路径。
	trimmed := strings.TrimSuffix(channel.BaseURL, "##")
	base := strings.TrimSuffix(trimmed, "/")
	target := base + imageUpstreamPath(channel, kind)
	if trimmed != channel.BaseURL {
		target = base + "/" + path.Base(imageUpstreamPath(channel, kind))
	}

	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upstream.Header.Set("Content-Type", contentType)
	upstream.Header.Set("Accept", "text/event-stream")
	upstream.Header.Set("Authorization", "Bearer "+key.Key)
	for _, header := range channel.CustomHeader {
		if header.HeaderKey != "" && header.HeaderValue != "" {
			upstream.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}

	response, err := client.Do(upstream)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		failure, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("upstream responded %s", response.Status)
		}
		return nil, fmt.Errorf("upstream responded %s: %s", response.Status, failure)
	}
	return response, nil
}

// imageUpstreamPath 返回图片接口在该渠道上的请求路径, 取自渠道配置。
// 落库时已由 normalizeChannelConfig 补齐默认值并保证以 / 开头, 故此处无需再兜底。
func imageUpstreamPath(channel model.Channel, kind string) string {
	if kind == "edits" {
		return channel.OpenAIImageEditPath
	}
	return channel.OpenAIImageGenerationPath
}

// imageReject 以 OpenAI 风格 JSON 返回请求级失败。
func imageReject(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{
		"error": gin.H{
			"message": err.Error(),
			"type":    "invalid_request_error",
		},
	})
	c.Abort()
}
