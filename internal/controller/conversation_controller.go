package controller

import (
	"net/http"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/middleware"
	"github.com/baomian/baomian-backend/internal/service"
	"github.com/gin-gonic/gin"
)

type ConversationController struct{ service *service.ConversationService }

func NewConversationController(value *service.ConversationService) *ConversationController {
	return &ConversationController{service: value}
}

func (h *ConversationController) History(c *gin.Context) {
	result, err := h.service.History(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ConversationController) Activity(c *gin.Context) {
	var request dto.ConversationActivityRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondBindingError(c, err)
		return
	}
	result, err := h.service.Activity(c.Request.Context(), middleware.UserID(c), request)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ConversationController) Turn(c *gin.Context) {
	var request dto.ConversationTurnRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondBindingError(c, err)
		return
	}
	result, err := h.service.Turn(c.Request.Context(), middleware.UserID(c), request)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ConversationController) Finalize(c *gin.Context) {
	result, err := h.service.Finalize(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
