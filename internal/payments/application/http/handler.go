package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// PaymentHandler handles HTTP requests for payments
type PaymentHandler struct {
	// TODO: Add use cases dependency injection
}

// NewPaymentHandler creates a new payment handler
func NewPaymentHandler() *PaymentHandler {
	return &PaymentHandler{}
}

// RegisterRoutes registers all payment routes
func (h *PaymentHandler) RegisterRoutes(rg *gin.RouterGroup) {
	payments := rg.Group("/payments")
	{
		payments.GET("", h.List)
		payments.GET("/:id", h.GetByID)
		payments.POST("", h.Create)
		payments.POST("/webhook", h.Webhook)
	}
}

// List returns all payments
func (h *PaymentHandler) List(c *gin.Context) {
	// TODO: Implement list payments
	c.JSON(http.StatusOK, gin.H{
		"data":    []interface{}{},
		"message": "list payments - not implemented",
	})
}

// GetByID returns a payment by ID
func (h *PaymentHandler) GetByID(c *gin.Context) {
	// TODO: Implement get payment by ID
	c.JSON(http.StatusOK, gin.H{
		"data":    nil,
		"message": "get payment - not implemented",
	})
}

// Create initiates a new payment
func (h *PaymentHandler) Create(c *gin.Context) {
	// TODO: Implement create payment
	c.JSON(http.StatusCreated, gin.H{
		"data":    nil,
		"message": "create payment - not implemented",
	})
}

// Webhook handles payment provider webhooks
func (h *PaymentHandler) Webhook(c *gin.Context) {
	// TODO: Implement webhook handling
	c.JSON(http.StatusOK, gin.H{
		"message": "webhook received",
	})
}

