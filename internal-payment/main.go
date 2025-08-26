package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"substack-idempotency/pkg/database"
	"substack-idempotency/pkg/models"
	"substack-idempotency/pkg/nats"
	"substack-idempotency/pkg/utils"

	"github.com/joho/godotenv"
)

var timeoutMs int

func main() {
	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found")
	}

	if err := database.Init(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := nats.Init(); err != nil {
		slog.Error("Failed to initialize NATS", "error", err)
		os.Exit(1)
	}
	defer nats.Close()

	if err := database.CreateTables(); err != nil {
		slog.Error("Failed to create tables", "error", err)
		os.Exit(1)
	}

	timeoutStr := os.Getenv("PAYMENT_TIMEOUT_MS")
	if timeoutStr == "" {
		timeoutStr = "200"
	}
	var err error
	timeoutMs, err = strconv.Atoi(timeoutStr)
	if err != nil {
		timeoutMs = 200
	}

	slog.Info("Internal Payment Service configuration", "payment_timeout_ms", timeoutMs)

	http.HandleFunc("/trigger-payment-paid", triggerPaymentPaid)
	http.HandleFunc("/health", healthCheck)

	slog.Info("Internal Payment Service starting on port 8001")
	if err := http.ListenAndServe(":8001", nil); err != nil {
		slog.Error("Failed to start server", "error", err)
	}
}

func triggerPaymentPaid(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.PaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.CorrelationID == "" {
		req.CorrelationID = utils.GenerateCorrelationID()
	}

	correlationID := req.CorrelationID
	logPrefix := "[" + correlationID + "] "
	
	slog.Info(logPrefix+"Processing payment", "order_id", req.OrderID, "amount", req.PaidAmount)

	time.Sleep(time.Duration(timeoutMs) * time.Millisecond)

	slog.Info(logPrefix+"Calling internal order api for validation, result: success")

	publishCount := utils.DeterminePublishCount()

	paidAt := time.Now()
	message := models.PaymentPaidMessage{
		OrderID:       req.OrderID,
		PaidAmount:    req.PaidAmount,
		PaidAt:        paidAt,
		CorrelationID: correlationID,
	}

	slog.Info(logPrefix+"Publish count", "count", publishCount)

	for i := 0; i < publishCount; i++ {
		messageData, _ := json.Marshal(message)
		if err := nats.Publish("payment.paid", messageData); err != nil {
			slog.Error(logPrefix+"Failed to publish message", "error", err)
		} else {
			slog.Info(logPrefix+"Published to payment.paid channel", "order_id", req.OrderID, "paid_amount", req.PaidAmount, "paid_at", paidAt)
		}
	}

	query := `INSERT INTO internal_payments (order_id, paid_amount, paid_at) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE order_id = order_id`
	if _, err := database.DB.Exec(query, req.OrderID, req.PaidAmount, paidAt); err != nil {
		slog.Error(logPrefix+"Failed to store payment", "error", err)
	}

	response := models.PaymentResponse{Status: "success"}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
