package controller

import (
	"net/http"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/middleware"
	"github.com/baomian/baomian-backend/internal/service"
	"github.com/gin-gonic/gin"
)

type ProfileController struct{ service *service.ProfileService }

func NewProfileController(value *service.ProfileService) *ProfileController {
	return &ProfileController{service: value}
}

func (h *ProfileController) Get(c *gin.Context) {
	result, err := h.service.Get(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ProfileController) Update(c *gin.Context) {
	var request dto.UpdateProfileRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondBindingError(c, err)
		return
	}
	result, err := h.service.Update(c.Request.Context(), middleware.UserID(c), request)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
