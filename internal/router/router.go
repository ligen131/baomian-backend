package router

import (
	"context"
	"net/http"
	"time"

	"github.com/baomian/baomian-backend/internal/controller"
	"github.com/baomian/baomian-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Dependencies struct {
	DB                     *gorm.DB
	DefaultUserID          string
	CORSAllowedOrigins     []string
	ProfileController      *controller.ProfileController
	TonightController      *controller.TonightController
	ConversationController *controller.ConversationController
	JournalController      *controller.JournalController
	DeviceController       *controller.DeviceController
	WebSocketController    *controller.WebSocketController
	Logger                 interface {
		InfoContext(context.Context, string, ...any)
	}
}

func New(deps Dependencies, accessLog gin.HandlerFunc) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery(), middleware.RequestID(), middleware.CORS(deps.CORSAllowedOrigins), accessLog, middleware.DemoUser(deps.DefaultUserID))

	api := engine.Group("/api/v1")
	{
		api.GET("/health", func(c *gin.Context) {
			sqlDB, err := deps.DB.DB()
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "database": "unavailable"})
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
			defer cancel()
			if err := sqlDB.PingContext(ctx); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "database": "unavailable"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "ok", "database": "ok"})
		})
		api.GET("/profile", deps.ProfileController.Get)
		api.PUT("/profile", deps.ProfileController.Update)
		api.GET("/tonight", deps.TonightController.Get)
		api.POST("/tonight/actions", deps.TonightController.Action)
		api.POST("/conversations/turn", deps.ConversationController.Turn)
		api.POST("/conversations/finalize", deps.ConversationController.Finalize)
		api.GET("/journals", deps.JournalController.List)
		api.GET("/memories", deps.JournalController.List)
		api.GET("/ws", deps.WebSocketController.Connect)
		api.POST("/device/events", deps.DeviceController.Event)
		api.GET("/device/commands/next", deps.DeviceController.NextCommand)
		api.POST("/device/commands/ack", deps.DeviceController.Ack)
	}
	return engine
}
