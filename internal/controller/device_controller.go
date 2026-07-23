package controller

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/repository"
	"github.com/baomian/baomian-backend/internal/service"
	"github.com/gin-gonic/gin"
)

type DeviceController struct {
	service        *service.DeviceService
	defaultTimeout time.Duration
}

func NewDeviceController(value *service.DeviceService, defaultTimeout time.Duration) *DeviceController {
	return &DeviceController{service: value, defaultTimeout: defaultTimeout}
}

func (h *DeviceController) Event(c *gin.Context) {
	var request dto.DeviceEventRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondBindingError(c, err)
		return
	}
	result, err := h.service.HandleEvent(c.Request.Context(), request)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *DeviceController) NextCommand(c *gin.Context) {
	deviceID := c.Query("deviceId")
	if deviceID == "" {
		respondBindingError(c, errors.New("deviceId is required"))
		return
	}
	timeout := h.defaultTimeout
	if value := c.Query("timeoutSec"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 0 {
			respondBindingError(c, errors.New("timeoutSec must be a non-negative integer"))
			return
		}
		timeout = time.Duration(seconds) * time.Second
	}
	result, err := h.service.NextCommand(c.Request.Context(), deviceID, timeout)
	if errors.Is(err, repository.ErrNotFound) {
		c.Status(http.StatusNoContent)
		return
	}
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *DeviceController) Ack(c *gin.Context) {
	var request dto.CommandAckRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondBindingError(c, err)
		return
	}
	result, err := h.service.Ack(c.Request.Context(), request)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
