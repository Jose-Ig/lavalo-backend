package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Jose-Ig/lavalo-backend/internal/common"
	"github.com/Jose-Ig/lavalo-backend/internal/slots/domain/usecases"
)

// AvailabilityHandler handles HTTP requests for availability
type AvailabilityHandler struct {
	useCase *usecases.AvailabilityUseCase
}

// NewAvailabilityHandler creates a new availability handler
func NewAvailabilityHandler(useCase *usecases.AvailabilityUseCase) *AvailabilityHandler {
	return &AvailabilityHandler{
		useCase: useCase,
	}
}

// GetAvailability returns availability for the next 7 days
// @Summary Get weekly availability
// @Description Returns availability for all slots and hours for the next 7 days
// @Tags availability
// @Produce json
// @Success 200 {object} models.AvailabilityResponse
// @Failure 404 {object} common.APIError "No slots configured"
// @Failure 500 {object} common.APIError "Internal server error"
// @Router /api/v1/availability [get]
func (h *AvailabilityHandler) GetAvailability(c *gin.Context) {
	ctx := c.Request.Context()

	availability, err := h.useCase.GetWeekAvailability(ctx)
	if err != nil {
		statusCode := common.MapErrorToHTTPStatus(err)

		var apiErr *common.APIError
		if errors.As(err, &apiErr) {
			c.JSON(statusCode, apiErr)
			return
		}

		c.JSON(statusCode, common.NewAPIError(statusCode, err.Error(), ""))
		return
	}

	c.JSON(http.StatusOK, availability)
}

