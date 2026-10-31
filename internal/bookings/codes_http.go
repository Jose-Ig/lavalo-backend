package bookings

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Code endpoints use POST bodies so customer access codes never enter URLs or access logs.
func RegisterCodeRoutes(r *gin.RouterGroup, s *Service) {
	type window struct {
		start time.Time
		count int
	}
	var mu sync.Mutex
	visits := map[string]window{}
	limit := func(c *gin.Context) {
		ip, _, _ := net.SplitHostPort(c.Request.RemoteAddr) // Do not trust arbitrary forwarded headers.
		now := time.Now()
		mu.Lock()
		for key, v := range visits {
			if now.Sub(v.start) >= time.Minute {
				delete(visits, key)
			}
		}
		v, exists := visits[ip]
		blocked := (!exists && len(visits) >= 4096) || v.count >= 60
		if !blocked {
			if !exists {
				v.start = now
			}
			v.count++
			visits[ip] = v
		}
		mu.Unlock()
		if blocked {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(429, gin.H{"error": "Esperá un minuto antes de intentar nuevamente."})
			return
		}
		c.Next()
	}
	handle := func(verify bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
			var input struct {
				Code string `json:"code"`
				Date string `json:"date"`
				QR   string `json:"qr"`
			}
			if c.ShouldBindJSON(&input) != nil || len(input.Date) > 10 || len(input.Code) > 64 || len(input.QR) > 256 {
				c.JSON(400, gin.H{"error": "Revisá el código y el QR."})
				return
			}
			var access ReservationAccess
			var err error
			if verify {
				access, err = s.VerifySlotQR(c.Request.Context(), input.Code, input.QR, input.Date)
			} else {
				access, err = s.LookupCode(c.Request.Context(), input.Code, input.Date)
			}
			if err != nil {
				status, message := 500, "No pudimos verificar la reserva. Reintentá."
				switch {
				case errors.Is(err, gorm.ErrRecordNotFound):
					status = 404
					message = "No encontramos una reserva con ese código."
				case errors.Is(err, ErrInvalid):
					status = 400
					message = "El QR no corresponde a un espacio de Uash."
				case errors.Is(err, ErrConflict):
					status = 409
					message = "Verificá que sea el espacio reservado, dentro del horario de tu turno y con el pago confirmado."
				}
				c.JSON(status, gin.H{"error": message})
				return
			}
			c.JSON(200, gin.H{"data": access})
		}
	}
	r.POST("/reservations/lookup", limit, handle(false))
	r.POST("/reservations/verify-slot", limit, handle(true))
}
