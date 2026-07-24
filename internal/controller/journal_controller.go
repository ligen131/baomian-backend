package controller

import (
	"net/http"
	"strconv"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/middleware"
	"github.com/baomian/baomian-backend/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type JournalController struct{ service *service.JournalService }

func NewJournalController(value *service.JournalService) *JournalController {
	return &JournalController{service: value}
}

func (h *JournalController) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "7"))
	result, err := h.service.List(c.Request.Context(), middleware.UserID(c), limit)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *JournalController) Get(c *gin.Context) {
	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBindingError(c, err)
		return
	}
	result, err := h.service.Get(c.Request.Context(), middleware.UserID(c), cardID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *JournalController) Update(c *gin.Context) {
	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBindingError(c, err)
		return
	}
	var request dto.UpdateMemoryCardRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondBindingError(c, err)
		return
	}
	result, err := h.service.Update(c.Request.Context(), middleware.UserID(c), cardID, request)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *JournalController) Delete(c *gin.Context) {
	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondBindingError(c, err)
		return
	}
	if err := h.service.Delete(c.Request.Context(), middleware.UserID(c), cardID); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
