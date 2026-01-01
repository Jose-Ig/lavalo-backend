package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
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

	slotUsecases "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/usecases"
	slotRepos "github.com/Jose-Ig/lavalo-backend/internal/slots/infrastructure/repositories"
)

const defaultDSN = "data/lavalo.db"

// dbPath stores the resolved database path for debug endpoint
var dbPath string

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
		common.Logger.Error("Failed to initialize database", zap.Error(err))
		os.Exit(1)
	}

	// Run migrations
	if err := runMigrations(db); err != nil {
		common.Logger.Error("Failed to run migrations", zap.Error(err))
		os.Exit(1)
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
		common.Logger.Error("Failed to start server", zap.Error(err))
		os.Exit(1)
	}
}

// initDatabase initializes the SQLite database connection
func initDatabase(cfg *common.Config) (*gorm.DB, error) {
	// Set default DSN if empty
	dsn := cfg.Database.DSN
	if dsn == "" {
		dsn = defaultDSN
		common.Logger.Info("Using default database path", zap.String("dsn", dsn))
	}

	// Resolve absolute path
	absPath, err := filepath.Abs(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve database path: %w", err)
	}
	dbPath = absPath

	// Ensure directory exists
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory %s: %w", dir, err)
	}

	common.Logger.Info("Database path resolved",
		zap.String("dsn", dsn),
		zap.String("absolute_path", absPath),
		zap.String("directory", dir),
	)

	// Open database connection
	db, err := gorm.Open(sqlite.Open(absPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	common.Logger.Info("Database connection established",
		zap.String("path", absPath),
		zap.Bool("file_exists", common.FileExists(absPath)),
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
		// Availability endpoint - wired with usecase
		availabilityRepo := slotRepos.NewAvailabilityRepository(db)
		availabilityUseCase := slotUsecases.NewAvailabilityUseCase(availabilityRepo)
		availabilityHandler := reservationHttp.NewAvailabilityHandler(availabilityUseCase)
		v1.GET("/availability", availabilityHandler.GetAvailability)

		// Register domain handlers
		reservationHandler := reservationHttp.NewReservationHandler()
		reservationHandler.RegisterRoutes(v1)

		slotHandler := slotHttp.NewSlotHandler()
		slotHandler.RegisterRoutes(v1)

		addressHandler := addressHttp.NewAddressHandler()
		addressHandler.RegisterRoutes(v1)

		paymentHandler := paymentHttp.NewPaymentHandler()
		paymentHandler.RegisterRoutes(v1)

		// Debug endpoints
		debug := v1.Group("/_debug")
		{
			debug.GET("/db", DebugDBHandler(db))
		}
	}
}

// healthHandler returns service health status
func healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "lavalo-api",
	})
}

// DebugDBHandler returns database debug information
func DebugDBHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tables, err := common.ListTables(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("failed to list tables: %v", err),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"dsn":         dbPath,
			"file_exists": common.FileExists(dbPath),
			"file_size":   common.FileSize(dbPath),
			"tables":      tables,
		})
	}
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
