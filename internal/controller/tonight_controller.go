package controller

import (
	"net/http"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/middleware"
	"github.com/baomian/baomian-backend/internal/service"
	"github.com/gin-gonic/gin"
)

type TonightController struct{ service *service.TonightService }

func NewTonightController(value *service.TonightService) *TonightController {
	return &TonightController{service: value}
}

func (h *TonightController) Get(c *gin.Context) {
	result, err := h.service.Get(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *TonightController) Action(c *gin.Context) {
	var request dto.TonightActionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondBindingError(c, err)
		return
	}
	result, err := h.service.Action(c.Request.Context(), middleware.UserID(c), request)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
