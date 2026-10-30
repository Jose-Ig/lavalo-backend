package payments

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func failure(c *gin.Context, err error) {
	code := http.StatusServiceUnavailable
	message := "No pudimos verificar el pago. Reintentá la consulta."
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		code = 404
		message = "Reserva o pago no encontrado."
	case errors.Is(err, ErrConflict):
		code = 409
		message = "La reserva venció o ya no admite pagos."
	case errors.Is(err, ErrMismatch):
		code = 422
		message = "El pago requiere revisión."
	}
	c.JSON(code, gin.H{"error": message})
}
func Register(r *gin.RouterGroup, s *Service) {
	handle := func(start bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil || id == 0 {
				c.Status(400)
				return
			}
			token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
			var v View
			if start {
				v, err = s.Start(c.Request.Context(), uint(id), token)
			} else {
				v, err = s.Status(c.Request.Context(), uint(id), token)
			}
			if err != nil {
				failure(c, err)
				return
			}
			c.JSON(200, gin.H{"data": v})
		}
	}
	r.POST("/reservations/:id/checkout", handle(true))
	r.GET("/reservations/:id/payment", handle(false))
	r.GET("/payments/return", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		c.Header("Referrer-Policy", "no-referrer")
		c.Data(200, "text/html; charset=utf-8", []byte(`<!doctype html><html lang="es"><meta name="viewport" content="width=device-width"><title>Volver a Uash</title><body><h1>Volvé a Uash para consultar tu pago</h1><p><a href="uash://checkout/result">Abrir Uash</a></p><p>Si no abre la app, regresá manualmente. El estado se verifica desde Uash.</p></body></html>`))
	})
}
