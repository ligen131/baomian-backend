package controller

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/baomian/baomian-backend/internal/speech"
	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/gin-gonic/gin"
)

const ttsPCMContentType = "audio/pcm;codec=pcm_s16le;rate=24000;channels=1"

type TTSController struct {
	tts        speech.TTSClient
	configured bool
}

func NewTTSController(tts speech.TTSClient, configured bool) *TTSController {
	return &TTSController{tts: tts, configured: configured}
}

func (h *TTSController) Stream(c *gin.Context) {
	if !h.configured {
		c.JSON(http.StatusServiceUnavailable, errorBody{Error: errorDetail{
			Code: voice.ErrorSpeechNotConfigured, Message: "语音合成服务尚未配置",
		}})
		return
	}
	var request struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		respondBindingError(c, err)
		return
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" || !utf8.ValidString(request.Text) || utf8.RuneCountInString(request.Text) > 500 {
		c.JSON(http.StatusBadRequest, errorBody{Error: errorDetail{
			Code: "validation_error", Message: "text 必须为 1 至 500 个 Unicode 字符",
		}})
		return
	}

	started := false
	err := h.tts.Stream(c.Request.Context(), request.Text, func(chunk []byte) error {
		if len(chunk) == 0 {
			return nil
		}
		if !started {
			started = true
			c.Header("Content-Type", ttsPCMContentType)
			c.Header("Cache-Control", "no-store")
			c.Header("X-Audio-Codec", "pcm_s16le")
			c.Header("X-Audio-Sample-Rate", "24000")
			c.Status(http.StatusOK)
		}
		if _, err := c.Writer.Write(chunk); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	})
	if err == nil {
		if !started {
			c.JSON(http.StatusBadGateway, errorBody{Error: errorDetail{Code: voice.ErrorTTSUnavailable, Message: "语音合成未返回音频"}})
		}
		return
	}
	if started || c.Request.Context().Err() != nil {
		return
	}
	c.JSON(http.StatusBadGateway, errorBody{Error: errorDetail{Code: voice.ErrorTTSUnavailable, Message: "语音合成暂时不可用"}})
}
