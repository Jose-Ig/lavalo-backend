package bookings

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func Register(r *gin.RouterGroup, s *Service) {
	RegisterCodeRoutes(r, s)
	fail := func(c *gin.Context, err error) {
		code := 500
		message := "internal server error"
		switch {
		case errors.Is(err, ErrInvalid):
			code = 400
			message = "invalid reservation: use a future 5-minute interval, duration 3/15/30/60, within 09:00–21:00 and the next 8 days, with contact and vehicle details"
		case errors.Is(err, ErrConflict):
			code = 409
			message = "slot unavailable or reservation cannot be changed"
		case errors.Is(err, gorm.ErrRecordNotFound):
			code = 404
			message = "reservation not found"
		}
		c.JSON(code, gin.H{"error": message})
	}
	r.POST("/reservations", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
		var i Input
		if c.ShouldBindJSON(&i) != nil {
			fail(c, ErrInvalid)
			return
		}
		res, token, err := s.Create(c.Request.Context(), i)
		if err != nil {
			fail(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(201, gin.H{"data": res, "access_token": token})
	})
	auth := func(c *gin.Context) (uint, string, error) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 32)
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if err != nil || id == 0 {
			return 0, "", ErrInvalid
		}
		return uint(id), token, nil
	}
	r.GET("/reservations/:id", func(c *gin.Context) {
		id, token, err := auth(c)
		if err != nil {
			fail(c, err)
			return
		}
		res, err := s.Get(c.Request.Context(), id, token)
		if err != nil {
			fail(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"data": res})
	})
	r.DELETE("/reservations/:id", func(c *gin.Context) {
		id, token, err := auth(c)
		if err == nil {
			err = s.Cancel(c.Request.Context(), id, token)
		}
		if err != nil {
			fail(c, err)
			return
		}
		c.Status(204)
	})
	r.GET("/available-slots", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		start, err := time.Parse(time.RFC3339, c.Query("start_time"))
		duration, e := strconv.Atoi(c.Query("duration_minutes"))
		if err != nil || e != nil {
			fail(c, ErrInvalid)
			return
		}
		slots, err := s.Available(c.Request.Context(), start, duration)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"data": slots})
	})
}
