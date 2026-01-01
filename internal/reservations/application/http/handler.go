package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ReservationHandler handles HTTP requests for reservations
type ReservationHandler struct {
	// TODO: Add use cases dependency injection
}

// NewReservationHandler creates a new reservation handler
func NewReservationHandler() *ReservationHandler {
	return &ReservationHandler{}
}

// RegisterRoutes registers all reservation routes
func (h *ReservationHandler) RegisterRoutes(rg *gin.RouterGroup) {
	reservations := rg.Group("/reservations")
	{
		reservations.GET("", h.List)
		reservations.GET("/:id", h.GetByID)
		reservations.POST("", h.Create)
		reservations.PUT("/:id", h.Update)
		reservations.DELETE("/:id", h.Delete)
	}
}

// List returns all reservations
func (h *ReservationHandler) List(c *gin.Context) {
	// TODO: Implement list reservations
	c.JSON(http.StatusOK, gin.H{
		"data":    []interface{}{},
		"message": "list reservations - not implemented",
	})
}

// GetByID returns a reservation by ID
func (h *ReservationHandler) GetByID(c *gin.Context) {
	// TODO: Implement get reservation by ID
	c.JSON(http.StatusOK, gin.H{
		"data":    nil,
		"message": "get reservation - not implemented",
	})
}

// Create creates a new reservation
func (h *ReservationHandler) Create(c *gin.Context) {
	// TODO: Implement create reservation
	c.JSON(http.StatusCreated, gin.H{
		"data":    nil,
		"message": "create reservation - not implemented",
	})
}

// Update updates an existing reservation
func (h *ReservationHandler) Update(c *gin.Context) {
	// TODO: Implement update reservation
	c.JSON(http.StatusOK, gin.H{
		"data":    nil,
		"message": "update reservation - not implemented",
	})
}

// Delete deletes a reservation
func (h *ReservationHandler) Delete(c *gin.Context) {
	// TODO: Implement delete reservation
	c.JSON(http.StatusNoContent, nil)
}

