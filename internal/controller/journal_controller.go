package controller

import (
	"net/http"
	"strconv"

	"github.com/baomian/baomian-backend/internal/middleware"
	"github.com/baomian/baomian-backend/internal/service"
	"github.com/gin-gonic/gin"
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
