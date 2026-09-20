package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
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

type imageCommitWriter struct {
	dst          io.Writer
	onFirstWrite func()
}

func (w *imageCommitWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 && w.onFirstWrite != nil {
		w.onFirstWrite()
		w.onFirstWrite = nil
	}
	return n, err
}

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
			if form := c.Request.MultipartForm; form != nil {
				_ = form.RemoveAll()
				c.Request.MultipartForm = nil
			}
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

		request := newRequestState(c.Request.Context(), groupName, group.ID, model.ProtocolOpenAIImage, fmt.Sprintf("<image request body omitted: %d bytes>", len(body)), c.GetInt("api_key_id"))
		ctx := request.requestCtx
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
				request.failSelection(fmt.Sprintf("group %q not found: %v", groupName, err))
				if !request.wait(ctx, model.DefaultGroupRelayConfig().MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			item := pickGroupItem(group)
			if item.ID == 0 {
				request.failSelection(noAvailableMemberReason(group))
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			grant, err := op.ChannelGrantGet(item.ChannelGrantID)
			if err != nil {
				releaseRouteProbe(group, item.ID)
				request.failSelection(fmt.Sprintf("member of group %q is not usable: %v", group.Name, err))
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			channelModel := grant.ChannelModel
			channelKey := grant.ChannelKey
			if channelModel == nil || channelKey == nil {
				releaseRouteProbe(group, item.ID)
				request.failSelection(fmt.Sprintf("member of group %q is missing its model or key", group.Name))
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			channel, err := op.ChannelGet(channelModel.ChannelID)
			if err != nil {
				releaseRouteProbe(group, item.ID)
				request.failSelection(fmt.Sprintf("channel of a member in group %q is unavailable: %v", group.Name, err))
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
				request.failSelection(fmt.Sprintf("channel %q model %q does not support image requests", channel.Name, channelModel.Name))
				recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
				continue
			}

			// JSON 和 multipart 正文都按分组成员配置改写真实模型名, 避免上游收到分组名。
			// 与聊天面同理: 本轮选路占用了探测名额时, 凡未走到成败定局点就结束请求的路径都要先归还。
			roundBody := body
			roundContentType := contentType
			if isMultipart {
				roundBody, roundContentType, err = rewriteImageMultipartModel(body, contentType, channelModel.Name)
			} else {
				roundBody, err = sjson.SetBytes(body, "model", channelModel.Name)
			}
			if err != nil {
				releaseRouteProbe(group, item.ID)
				request.markFailed(err, "", nil)
				imageReject(c, err)
				return
			}

			roundCtx, cancelRoundCause := context.WithCancelCause(ctx)
			cancelRound := func() { cancelRoundCause(context.Canceled) }
			request.startRound(cancelRound, channel.Name, channelModel.Name, model.ProtocolOpenAIImage)
			roundStartedAt := time.Now()

			httpClient, closeIdle, err := resolveUpstreamClient(channel)
			if err != nil {
				cancelRound()
				releaseRouteProbe(group, item.ID)
				request.finishRound(err.Error())
				request.markFailed(err, "", nil)
				imageReject(c, err)
				return
			}

			response, err := sendImageUpstream(roundCtx, httpClient, channel, *channelKey, roundContentType, c.Request.Header.Get("Accept"), c.Request.URL.RawQuery, c.Request.Header, roundBody, kind)
			roundWaitTime := time.Since(roundStartedAt).Milliseconds()

			if err != nil {
				if closeIdle != nil {
					closeIdle()
				}
				if ctx.Err() != nil {
					releaseRouteProbe(group, item.ID)
					request.markCanceled(ctx.Err(), "", nil)
					return
				}
				if context.Cause(roundCtx) == context.Canceled {
					releaseRouteProbe(group, item.ID)
					continue
				}
				request.finishRound(err.Error())
				cancelRound()
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
			if ctx.Err() != nil {
				response.Body.Close()
				cancelRound()
				if closeIdle != nil {
					closeIdle()
				}
				releaseRouteProbe(group, item.ID)
				request.markCanceled(ctx.Err(), "", nil)
				return
			}
			streaming := strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream")

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

			committed := false
			markCommitted := func() {
				if !committed {
					request.markCommitted(streaming)
					committed = true
				}
			}
			// 首次成功写出字节时标记已提交, 完整复制成功后才记渠道成功。
			trackedWriter := &imageCommitWriter{dst: c.Writer, onFirstWrite: markCommitted}
			written, copyErr := io.Copy(trackedWriter, response.Body)
			response.Body.Close()
			cancelRound()
			if closeIdle != nil {
				closeIdle()
			}
			if copyErr != nil {
				if streaming && committed {
					request.finishStream()
				}
				if ctx.Err() != nil {
					request.markCanceled(ctx.Err(), "", nil)
				} else {
					request.markFailed(copyErr, "", nil)
				}
				return
			}
			if !committed {
				markCommitted()
			}
			if streaming {
				request.finishStream()
			}
			metrics := model.StatsMetrics{WaitTime: roundWaitTime, RequestSuccess: 1}
			_ = op.ChannelStatsUpdate(channel.ID, metrics)
			_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
			_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
			recordRouteSuccess(group, item.ID)
			// 图片正文可达数 MB, 只登记大小而不留全文, 避免请求状态占满内存。
			request.markSucceeded(fmt.Sprintf("<image response %d bytes>", written), nil)
			return
		}
	}
}

func rewriteImageMultipartModel(body []byte, contentType, modelName string) ([]byte, string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		if err == nil {
			err = errors.New("multipart content type has no boundary")
		}
		return nil, "", fmt.Errorf("parse multipart content type: %w", err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	if err := writer.SetBoundary(params["boundary"]); err != nil {
		return nil, "", fmt.Errorf("preserve multipart boundary: %w", err)
	}
	foundModel := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read multipart body: %w", err)
		}
		partBody, err := io.ReadAll(part)
		if err != nil {
			return nil, "", fmt.Errorf("read multipart field: %w", err)
		}
		if part.FormName() == "model" {
			partBody = []byte(modelName)
			foundModel = true
		}
		if err := func() error {
			newPart, err := writer.CreatePart(part.Header)
			if err != nil {
				return err
			}
			_, err = newPart.Write(partBody)
			return err
		}(); err != nil {
			return nil, "", fmt.Errorf("write multipart body: %w", err)
		}
	}
	if !foundModel {
		return nil, "", errors.New("multipart image request has no model field")
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart body: %w", err)
	}
	return output.Bytes(), writer.FormDataContentType(), nil
}

// sendImageUpstream 按渠道配置向上游图片接口发起一次请求, 4xx/5xx 视为本轮失败。
func sendImageUpstream(ctx context.Context, client *http.Client, channel model.Channel, key model.ChannelKey, contentType, accept, rawQuery string, clientHeaders http.Header, body []byte, kind string) (*http.Response, error) {
	// BaseURL 以 ## 结尾表示地址已完整, 只取到末段目录, 不再拼渠道配置的协议路径。
	trimmed := strings.TrimSuffix(channel.BaseURL, "##")
	base := strings.TrimSuffix(trimmed, "/")
	target := base + imageUpstreamPath(channel, kind)
	if trimmed != channel.BaseURL {
		target = base + "/" + path.Base(imageUpstreamPath(channel, kind))
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}

	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upstream.Header.Set("Content-Type", contentType)
	if accept == "" {
		accept = "*/*"
	}
	upstream.Header.Set("Accept", accept)
	upstream.Header.Set("Authorization", "Bearer "+key.Key)
	for _, header := range channel.CustomHeader {
		if header.HeaderKey != "" && header.HeaderValue != "" {
			if httpclient.IsSensitiveHeader(header.HeaderKey) && upstream.Header.Get(header.HeaderKey) != "" {
				continue
			}
			value := clientHeaderPlaceholder.ReplaceAllStringFunc(header.HeaderValue, func(placeholder string) string {
				return clientHeaders.Get(placeholder[len("{client_header:") : len(placeholder)-1])
			})
			upstream.Header.Set(header.HeaderKey, value)
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
