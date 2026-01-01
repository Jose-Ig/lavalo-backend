package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SlotHandler handles HTTP requests for slots
type SlotHandler struct {
	// TODO: Add use cases dependency injection
}

// NewSlotHandler creates a new slot handler
func NewSlotHandler() *SlotHandler {
	return &SlotHandler{}
}

// RegisterRoutes registers all slot routes
func (h *SlotHandler) RegisterRoutes(rg *gin.RouterGroup) {
	slots := rg.Group("/slots")
	{
		slots.GET("", h.List)
		slots.GET("/availability", h.GetAvailability)
		slots.GET("/:id", h.GetByID)
		slots.POST("", h.Create)
		slots.PUT("/:id", h.Update)
		slots.DELETE("/:id", h.Delete)
	}
}

// List returns all slots
func (h *SlotHandler) List(c *gin.Context) {
	// TODO: Implement list slots
	c.JSON(http.StatusOK, gin.H{
		"data":    []interface{}{},
		"message": "list slots - not implemented",
	})
}

// GetAvailability returns available slots
func (h *SlotHandler) GetAvailability(c *gin.Context) {
	// TODO: Implement availability logic
	// Query params: date, start_time, end_time
	c.JSON(http.StatusOK, gin.H{
		"data":    []interface{}{},
		"message": "get availability - not implemented",
	})
}

// GetByID returns a slot by ID
func (h *SlotHandler) GetByID(c *gin.Context) {
	// TODO: Implement get slot by ID
	c.JSON(http.StatusOK, gin.H{
		"data":    nil,
		"message": "get slot - not implemented",
	})
}

// Create creates a new slot
func (h *SlotHandler) Create(c *gin.Context) {
	// TODO: Implement create slot
	c.JSON(http.StatusCreated, gin.H{
		"data":    nil,
		"message": "create slot - not implemented",
	})
}

// Update updates an existing slot
func (h *SlotHandler) Update(c *gin.Context) {
	// TODO: Implement update slot
	c.JSON(http.StatusOK, gin.H{
		"data":    nil,
		"message": "update slot - not implemented",
	})
}

// Delete deletes a slot
func (h *SlotHandler) Delete(c *gin.Context) {
	// TODO: Implement delete slot
	c.JSON(http.StatusNoContent, nil)
}

