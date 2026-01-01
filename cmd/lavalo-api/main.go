package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Jose-Ig/lavalo-backend/internal/common"

	addressModels "github.com/Jose-Ig/lavalo-backend/internal/addresses/domain/models"
	paymentModels "github.com/Jose-Ig/lavalo-backend/internal/payments/domain/models"
	reservationModels "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slotModels "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"

	addressHttp "github.com/Jose-Ig/lavalo-backend/internal/addresses/application/http"
	paymentHttp "github.com/Jose-Ig/lavalo-backend/internal/payments/application/http"
	reservationHttp "github.com/Jose-Ig/lavalo-backend/internal/reservations/application/http"
	slotHttp "github.com/Jose-Ig/lavalo-backend/internal/slots/application/http"
)

func main() {
	// Parse command line flags
	migrateOnly := flag.Bool("migrate-only", false, "Run migrations and exit")
	flag.Parse()

	// Initialize logger
	if err := common.InitLogger(); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer common.SyncLogger()

	// Load configuration
	cfg := common.LoadConfig()
	common.Logger.Info("Configuration loaded",
		zap.String("port", cfg.Server.Port),
		zap.String("mode", cfg.Server.Mode),
	)

	// Initialize database
	db, err := initDatabase(cfg)
	if err != nil {
		common.Logger.Fatal("Failed to initialize database", zap.Error(err))
	}

	// Run migrations
	if err := runMigrations(db); err != nil {
		common.Logger.Fatal("Failed to run migrations", zap.Error(err))
	}

	// Exit if migrate-only flag is set
	if *migrateOnly {
		common.Logger.Info("Migrations completed successfully")
		os.Exit(0)
	}

	// Setup Gin
	gin.SetMode(cfg.Server.Mode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(ginLogger())

	// Register routes
	setupRoutes(router, db)

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Server.Port)
	common.Logger.Info("Starting server", zap.String("address", addr))

	if err := router.Run(addr); err != nil {
		common.Logger.Fatal("Failed to start server", zap.Error(err))
	}
}

// initDatabase initializes the SQLite database connection
func initDatabase(cfg *common.Config) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	common.Logger.Info("Database connection established",
		zap.String("dsn", cfg.Database.DSN),
	)

	return db, nil
}

// runMigrations runs auto-migrations for all models
func runMigrations(db *gorm.DB) error {
	common.Logger.Info("Running database migrations...")

	err := db.AutoMigrate(
		&reservationModels.Reservation{},
		&slotModels.Slot{},
		&addressModels.Address{},
		&paymentModels.Payment{},
	)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	common.Logger.Info("Database migrations completed successfully")
	return nil
}

// setupRoutes configures all API routes
func setupRoutes(router *gin.Engine, db *gorm.DB) {
	// Health check
	router.GET("/health", healthHandler)

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Availability endpoint (skeleton)
		v1.GET("/availability", availabilityHandler)

		// Register domain handlers
		reservationHandler := reservationHttp.NewReservationHandler()
		reservationHandler.RegisterRoutes(v1)

		slotHandler := slotHttp.NewSlotHandler()
		slotHandler.RegisterRoutes(v1)

		addressHandler := addressHttp.NewAddressHandler()
		addressHandler.RegisterRoutes(v1)

		paymentHandler := paymentHttp.NewPaymentHandler()
		paymentHandler.RegisterRoutes(v1)
	}
}

// healthHandler returns service health status
func healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "lavalo-api",
	})
}

// availabilityHandler returns available slots (skeleton)
func availabilityHandler(c *gin.Context) {
	// TODO: Implement availability logic
	// Query params: date, location, service_type
	c.JSON(http.StatusOK, gin.H{
		"data":    []interface{}{},
		"message": "availability endpoint - not implemented yet",
	})
}

// ginLogger returns a gin middleware for logging with zap
func ginLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		common.Logger.Info("HTTP Request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}

