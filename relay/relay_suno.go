package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/common"
	"one-api/constant"
	"one-api/dto"
	"one-api/model"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"time"
)

func RelaySunoGenerate(c *gin.Context, relayMode int) (taskErr *dto.TaskError) {
	platform := constant.TaskPlatform(c.GetString("platform"))
	relayInfo := relaycommon.GenTaskRelayInfo(c)

	adaptor := GetSunoAIAdaptor(platform)
	if adaptor == nil {
		return service.TaskErrorWrapperLocal(fmt.Errorf("invalid api platform: %s", platform), "invalid_api_platform", http.StatusBadRequest)
	}
	adaptor.Init(relayInfo)
	// get & validate taskRequest 获取并验证文本请求
	//taskErr = adaptor.ValidateRequestAndSetAction(c, relayInfo)
	//if taskErr != nil {
	//	return
	//}

	modelName := c.GetString("original_model")
	modelPrice, success := common.GetModelPrice(modelName, true)
	if !success {
		defaultPrice, ok := common.GetDefaultModelRatioMap()[modelName]
		if !ok {
			modelPrice = 0.15
		} else {
			modelPrice = defaultPrice
		}
	}

	// 预扣
	groupRatio := common.GetGroupRatio(relayInfo.Group)
	ratio := modelPrice * groupRatio
	userQuota, err := model.CacheGetUserQuota(relayInfo.UserId)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "get_user_quota_failed", http.StatusInternalServerError)
		return
	}
	quota := int(ratio * common.QuotaPerUnit)
	if userQuota-quota < 0 {
		taskErr = service.TaskErrorWrapperLocal(errors.New("user quota is not enough"), "quota_not_enough", http.StatusForbidden)
		return
	}

	if relayInfo.OriginTaskID != "" {
		originTask, exist, err := model.GetByTaskId(relayInfo.UserId, relayInfo.OriginTaskID)
		if err != nil {
			taskErr = service.TaskErrorWrapper(err, "get_origin_task_failed", http.StatusInternalServerError)
			return
		}
		if !exist {
			taskErr = service.TaskErrorWrapperLocal(errors.New("task_origin_not_exist"), "task_not_exist", http.StatusBadRequest)
			return
		}
		if originTask.ChannelId != relayInfo.ChannelId {
			channel, err := model.GetChannelById(originTask.ChannelId, true)
			if err != nil {
				taskErr = service.TaskErrorWrapperLocal(err, "channel_not_found", http.StatusBadRequest)
				return
			}
			if channel.Status != common.ChannelStatusEnabled {
				return service.TaskErrorWrapperLocal(errors.New("该任务所属渠道已被禁用"), "task_channel_disable", http.StatusBadRequest)
			}
			c.Set("base_url", channel.GetBaseURL())
			c.Set("channel_id", originTask.ChannelId)
			c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))

			relayInfo.BaseUrl = channel.GetBaseURL()
			relayInfo.ChannelId = originTask.ChannelId
		}
	}

	// build body
	//requestBody, err := adaptor.BuildRequestBody(c, relayInfo)
	//if err != nil {
	//	taskErr = service.TaskErrorWrapper(err, "build_request_failed", http.StatusInternalServerError)
	//	return
	//}
	// do request
	resp, err := adaptor.DoRequest(c, relayInfo, c.Request.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
		return
	}
	// handle response
	if resp != nil && resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		taskErr = service.TaskErrorWrapper(fmt.Errorf(string(responseBody)), "fail_to_fetch_task", resp.StatusCode)
		return
	}

	defer func(ctx context.Context) {
		// release quota
		if relayInfo.ConsumeQuota && taskErr == nil {
			err := model.PostConsumeTokenQuota(relayInfo.TokenId, userQuota, quota, 0, true)
			if err != nil {
				common.SysError("error consuming token remain quota: " + err.Error())
			}
			err = model.CacheUpdateUserQuota(relayInfo.UserId)
			if err != nil {
				common.SysError("error update user quota cache: " + err.Error())
			}
			if quota != 0 {
				tokenName := c.GetString("token_name")
				logContent := fmt.Sprintf("模型固定价格 %.2f，分组倍率 %.2f，操作 %s", modelPrice, groupRatio, relayInfo.Action)
				other := make(map[string]interface{})
				other["model_price"] = modelPrice
				other["group_ratio"] = groupRatio
				model.RecordConsumeLog(ctx, relayInfo.UserId, relayInfo.ChannelId, 0, 0, modelName, tokenName, quota, logContent, relayInfo.TokenId, userQuota, 0, false, other)
				model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
				model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
			}
		}
	}(c.Request.Context())

	taskID, taskData, taskErr := adaptor.DoResponse(c, resp, relayInfo)
	if taskErr != nil {
		return
	}
	relayInfo.ConsumeQuota = true
	// insert task
	task := model.InitTask(constant.TaskPlatformSuno, relayInfo)
	task.TaskID = taskID
	task.Quota = quota
	task.Data = taskData
	if relayMode == relayconstant.RelayModeSunoGenerate {
		task.Action = "sunoai_music"
	} else if relayMode == relayconstant.RelayModeSunoGenerateLyrics {
		task.Action = "sunoai_lyrics"
	}
	task.SubmitTime = time.Now().UnixNano() / int64(time.Millisecond)
	task.StartTime = task.SubmitTime
	err = task.Insert()
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "insert_task_failed", http.StatusInternalServerError)
		return
	}
	return nil
}

func RelaySunoFeed(c *gin.Context, relayMode int) (taskResp *dto.TaskError) {
	respBuilder, ok := fetchRespBuilders[relayMode]
	if !ok {
		taskResp = service.TaskErrorWrapperLocal(errors.New("invalid_relay_mode"), "invalid_relay_mode", http.StatusBadRequest)
	}

	respBody, taskErr := respBuilder(c)
	if taskErr != nil {
		return taskErr
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	_, err := io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError)
		return
	}
	return
}
