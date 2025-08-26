package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/go-sql-driver/mysql"
)

var DB *sql.DB

func Init() error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=Asia%%2FJakarta",
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"))

	var err error
	DB, err = sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	if _, err := DB.Exec("SET time_zone = '+07:00'"); err != nil {
		return fmt.Errorf("failed to set timezone: %w", err)
	}

	return nil
}

func Close() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}

func CreateTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS internal_orders (
			id VARCHAR(255) PRIMARY KEY,
			amount INT NOT NULL,
			admin_fee INT NOT NULL,
			type VARCHAR(50) NOT NULL,
			operator VARCHAR(50) NOT NULL,
			destination_phone VARCHAR(20) NOT NULL,
			total INT NOT NULL,
			status VARCHAR(20) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS internal_payments (
			id INT AUTO_INCREMENT PRIMARY KEY,
			order_id VARCHAR(255) NOT NULL,
			paid_amount INT NOT NULL,
			paid_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE KEY unique_order (order_id)
		)`,
		`CREATE TABLE IF NOT EXISTS ext_orders (
			id INT AUTO_INCREMENT PRIMARY KEY,
			order_id VARCHAR(255) NOT NULL,
			destination_phone VARCHAR(20) NOT NULL,
			amount INT NOT NULL,
			status VARCHAR(20) NOT NULL,
			error VARCHAR(255),
			processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS fulfillment_attempts (
			id INT AUTO_INCREMENT PRIMARY KEY,
			order_id VARCHAR(255) NOT NULL,
			attempt_number INT NOT NULL DEFAULT 1,
			payload JSON,
			attempted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, query := range queries {
		if _, err := DB.Exec(query); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}

	if _, err := DB.Exec("SET time_zone = '+07:00'"); err != nil {
		return fmt.Errorf("failed to set timezone: %w", err)
	}

	return nil
}

func ResetTables() error {
	tables := []string{"fulfillment_attempts", "ext_orders", "internal_payments", "internal_orders"}

	for _, table := range tables {
		query := fmt.Sprintf("DROP TABLE IF EXISTS %s", table)
		if _, err := DB.Exec(query); err != nil {
			slog.Error("Failed to drop table", "table", table, "error", err)
		} else {
			slog.Info("Table dropped", "table", table)
		}
	}

	if err := CreateTables(); err != nil {
		return fmt.Errorf("failed to recreate tables: %w", err)
	}

	slog.Info("All tables dropped and recreated successfully")
	return nil
}
