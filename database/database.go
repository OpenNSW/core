package database

import (
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// New creates a new database connection using the provided configuration
func New(cfg Config) (*gorm.DB, error) {
	// Configure GORM logger
	gormLogger := logger.Default.LogMode(logger.Error)

	// Open database connection
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: gormLogger,
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get underlying SQL database to configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying database: %w", err)
	}

	// Configure connection pool
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxConnLifetimeSeconds > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.MaxConnLifetimeSeconds) * time.Second)
	}

	// Test the connection
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close() // Attempt to close the connection if ping fails
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("database connection established",
		"host", cfg.Host,
		"port", cfg.Port,
		"database", cfg.Name,
	)

	return db, nil
}

// Close closes the database connection
func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database: %w", err)
	}

	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	slog.Info("database connection closed")
	return nil
}

// HealthCheck performs a health check on the database connection
func HealthCheck(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	return nil
}
