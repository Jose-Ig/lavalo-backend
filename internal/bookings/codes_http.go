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

	reschedule := func(availability bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
			var input struct {
				Code      string    `json:"code"`
				Date      string    `json:"date"`
				StartTime time.Time `json:"start_time"`
				SlotID    uint      `json:"slot_id"`
			}
			if c.ShouldBindJSON(&input) != nil || len(input.Code) > 64 || len(input.Date) > 10 {
				c.JSON(400, gin.H{"error": "Revisá los datos del turno."})
				return
			}
			var data any
			var err error
			if availability {
				data, err = s.RescheduleAvailability(c.Request.Context(), input.Code, input.Date, input.StartTime)
			} else {
				data, err = s.Reschedule(c.Request.Context(), input.Code, input.Date, input.SlotID, input.StartTime)
			}
			if err != nil {
				status, message := 500, "No pudimos cambiar el turno. Reintentá."
				switch {
				case errors.Is(err, gorm.ErrRecordNotFound):
					status, message = 404, "No encontramos la reserva con ese código y fecha."
				case errors.Is(err, ErrInvalid):
					status, message = 400, "Elegí un turno futuro, dentro de los próximos 8 días y entre 09:00 y 21:00."
				case errors.Is(err, ErrConflict):
					status, message = 409, "El turno ya no está disponible o la reserva ya comenzó o fue validada. Actualizá la reserva y elegí otro turno."
				}
				c.JSON(status, gin.H{"error": message})
				return
			}
			c.JSON(200, gin.H{"data": data})
		}
	}
	extension := func(create bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
			var input struct {
				Code    string `json:"code"`
				Date    string `json:"date"`
				Minutes int    `json:"minutes"`
			}
			if c.ShouldBindJSON(&input) != nil || len(input.Code) > 64 || len(input.Date) > 10 {
				c.JSON(400, gin.H{"error": "Revisá los datos de la reserva."})
				return
			}
			var data any
			var token string
			var err error
			if create {
				data, token, err = s.CreateExtension(c.Request.Context(), input.Code, input.Date, input.Minutes)
			} else {
				data, err = s.ExtensionOptions(c.Request.Context(), input.Code, input.Date)
			}
			if err != nil {
				status, message := 500, "No pudimos consultar el tiempo adicional. Reintentá."
				switch {
				case errors.Is(err, gorm.ErrRecordNotFound):
					status, message = 404, "No encontramos la reserva."
				case errors.Is(err, ErrInvalid):
					status, message = 400, "Elegí 3, 15, 30 o 60 minutos."
				case errors.Is(err, ErrConflict):
					status, message = 409, "No hay disponibilidad o el turno ya terminó. Consultá nuevamente la reserva."
				}
				c.JSON(status, gin.H{"error": message})
				return
			}
			if create {
				c.JSON(201, gin.H{"data": data, "access_token": token})
			} else {
				c.JSON(200, gin.H{"data": data})
			}
		}
	}
	r.POST("/reservations/extensions/availability", limit, extension(false))
	r.POST("/reservations/extensions", limit, extension(true))
	r.POST("/reservations/reschedule/availability", limit, reschedule(true))
	r.POST("/reservations/reschedule", limit, reschedule(false))
	r.POST("/reservations/lookup", limit, handle(false))
	r.POST("/reservations/verify-slot", limit, handle(true))
}
