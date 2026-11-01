package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	"github.com/Jose-Ig/lavalo-backend/internal/payments"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Jose-Ig/lavalo-backend/internal/common"

	addressModels "github.com/Jose-Ig/lavalo-backend/internal/addresses/domain/models"
	paymentModels "github.com/Jose-Ig/lavalo-backend/internal/payments/domain/models"
	reservationModels "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slotModels "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"

	reservationHttp "github.com/Jose-Ig/lavalo-backend/internal/reservations/application/http"

	slotUsecases "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/usecases"
	slotRepos "github.com/Jose-Ig/lavalo-backend/internal/slots/infrastructure/repositories"
)

const defaultDSN = "data/lavalo.db"

// dbPath stores the resolved database path for debug endpoint
var dbPath string

func main() {
	// Parse command line flags
	seedSlots := flag.Int("seed-slots", 0, "Create numbered development slots (debug mode only)")
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

	if *seedSlots > 0 {
		if cfg.Server.Mode != gin.DebugMode {
			log.Fatal("seed-slots requires debug mode")
		}
		for id := 1; id <= *seedSlots; id++ {
			sl := slotModels.Slot{ID: uint(id), Label: fmt.Sprintf("Espacio %d", id), IsAvailable: true}
			if err := db.FirstOrCreate(&sl, uint(id)).Error; err != nil {
				log.Fatal(err)
			}
		}
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

	// Background provider reconciliation; no account configuration is performed.
	paymentCtx, cancelPayments := context.WithCancel(context.Background())
	defer cancelPayments()
	go payments.New(db, payments.ConfigFromEnv()).RunReconciler(paymentCtx)

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
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return gorm.Open(postgres.Open(url), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Warn)})
	}
	if cfg.Server.Mode == gin.ReleaseMode {
		return nil, fmt.Errorf("DATABASE_URL is required in release mode")
	}

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

	if err := slotModels.PreparePublicIDs(db); err != nil {
		return fmt.Errorf("failed to prepare slot public IDs: %w", err)
	}
	err := db.AutoMigrate(
		&reservationModels.Reservation{},
		&slotModels.Slot{},
		&addressModels.Address{},
		&paymentModels.Payment{},
		&bookings.NotificationIntent{},
		&payments.Checkout{},
		&payments.Receipt{},
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
		bookings.Register(v1, bookings.New(db))
		payments.Register(v1, payments.New(db, payments.ConfigFromEnv()))

		// Payments are scoped to authorized reservations. The old collection endpoint is intentionally unsupported.
		v1.Any("/payments", func(c *gin.Context) {
			c.JSON(501, gin.H{"error": "Use /reservations/:id/checkout or /reservations/:id/payment"})
		})

		// Debug endpoints
		debug := v1.Group("/_debug")
		{
			if gin.Mode() == gin.DebugMode {
				debug.GET("/db", DebugDBHandler(db))
			}
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
