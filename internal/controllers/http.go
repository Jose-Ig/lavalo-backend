package controllers

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

func Register(r *gin.RouterGroup, s *Service) {
	// Shared bounded limiter: 4 devices polling and acknowledging every 2.5 s fit comfortably.
	type window struct {
		start time.Time
		count int
	}
	var mu sync.Mutex
	visits := map[string]window{}
	limit := func(c *gin.Context) {
		ip, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		now := time.Now()
		mu.Lock()
		for key, v := range visits {
			if now.Sub(v.start) >= time.Minute {
				delete(visits, key)
			}
		}
		v, exists := visits[ip]
		blocked := (!exists && len(visits) >= 4096) || v.count >= 600
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
			c.AbortWithStatus(429)
			return
		}
		c.Next()
	}
	handle := func(action string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
			var input struct {
				Protocol  int         `json:"protocol"`
				Ready     bool        `json:"ready"`
				Previous  *Checkpoint `json:"previous_session"`
				Report    *Checkpoint `json:"report"`
				RequestID string      `json:"request_id"`
				BootID    string      `json:"boot_id"`
				CommandID string      `json:"command_id"`
				On        *bool       `json:"on"`
			}
			if c.ShouldBindJSON(&input) != nil {
				c.JSON(400, gin.H{"error": "invalid request"})
				return
			}
			value := input.RequestID
			if action == "ack" {
				value = input.CommandID
			}
			if _, err := uuid.Parse(value); err != nil || len(value) != 36 || (action == "ack" && input.On == nil) {
				c.JSON(400, gin.H{"error": "invalid request"})
				return
			}
			if _, err := uuid.Parse(input.BootID); err != nil || len(input.BootID) != 36 {
				c.JSON(400, gin.H{"error": "invalid boot ID"})
				return
			}
			authorization := c.GetHeader("Authorization")
			if !strings.HasPrefix(authorization, "Bearer ") {
				c.JSON(401, gin.H{"error": "controller request rejected"})
				return
			}
			token := strings.TrimPrefix(authorization, "Bearer ")
			var data any
			var err error
			if action == "ack" {
				err = s.AckForBoot(c.Request.Context(), token, input.BootID, input.CommandID, *input.On, input.Report)
			} else if action == "bootstrap" {
				data, err = s.Bootstrap(c.Request.Context(), token, BootstrapInput{RequestID: input.RequestID, BootID: input.BootID, Protocol: input.Protocol, Ready: input.Ready, Previous: input.Previous})
			} else {
				data, err = s.PollReport(c.Request.Context(), token, input.RequestID, input.BootID, input.Report)
			}
			if err != nil {
				status := 503
				if errors.Is(err, ErrUnauthorized) {
					status = 401
				}
				if errors.Is(err, ErrStale) || errors.Is(err, ErrBootstrap) {
					status = 409
				}
				c.JSON(status, gin.H{"error": "controller request rejected"})
				return
			}
			if action == "ack" {
				c.Status(204)
			} else {
				c.JSON(200, gin.H{"data": data})
			}
		}
	}
	r.POST("/controllers/bootstrap", limit, handle("bootstrap"))
	r.POST("/controllers/poll", limit, handle("poll"))
	r.POST("/controllers/ack", limit, handle("ack"))
}
