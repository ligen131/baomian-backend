package controller

import (
	"errors"
	"net/http"

	"github.com/baomian/baomian-backend/internal/service"
	"github.com/gin-gonic/gin"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func respondError(c *gin.Context, err error) {
	var serviceErr *service.Error
	if !errors.As(err, &serviceErr) {
		c.JSON(http.StatusInternalServerError, errorBody{Error: errorDetail{Code: "internal_error", Message: "服务暂时不可用"}})
		return
	}
	status := http.StatusInternalServerError
	switch serviceErr.Code {
	case "validation_error":
		status = http.StatusBadRequest
	case "invalid_transition", "conversation_limit", "conversation_incomplete", "conversation_expired", "request_in_progress", "journal_not_deletable":
		status = http.StatusConflict
	case "not_found":
		status = http.StatusNotFound
	case "ai_error":
		status = http.StatusBadGateway
	}
	c.JSON(status, errorBody{Error: errorDetail{Code: serviceErr.Code, Message: serviceErr.Message, Details: serviceErr.Details}})
}

func respondBindingError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, errorBody{Error: errorDetail{Code: "validation_error", Message: "请求参数无效", Details: map[string]any{"reason": err.Error()}}})
}
