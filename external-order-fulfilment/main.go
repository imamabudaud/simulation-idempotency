package main

import (
	"encoding/json"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"time"

	"substack-idempotency/pkg/database"
	"substack-idempotency/pkg/models"
	"substack-idempotency/pkg/utils"

	"github.com/joho/godotenv"
)

var externalIdempotencyCheck bool

func main() {
	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found")
	}

	externalIdempotencyCheck = os.Getenv("EXTERNAL_IDEMPOTENCY_CHECK") == "true"

	if err := database.Init(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := database.CreateTables(); err != nil {
		slog.Error("Failed to create tables", "error", err)
		os.Exit(1)
	}

	http.HandleFunc("/process-order", processOrder)
	http.HandleFunc("/health", healthCheck)

	slog.Info("External Order Fulfillment Service starting on port 9000", "external_idempotency_check", externalIdempotencyCheck)
	if err := http.ListenAndServe(":9000", nil); err != nil {
		slog.Error("Failed to start server", "error", err)
	}
}

func processOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.ExternalFulfillmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	correlationID := req.CorrelationID
	if correlationID == "" {
		correlationID = utils.GenerateCorrelationID()
		slog.Info("Generated new correlation ID for request", "correlation_id", correlationID, "order_id", req.OrderID)
	} else {
		slog.Info("Using correlation ID from request", "correlation_id", correlationID, "order_id", req.OrderID)
	}

	logPrefix := "[" + correlationID + "] "

	slog.Info(logPrefix+"Processing external fulfillment request", "order_id", req.OrderID, "amount", req.Amount, "external_idempotency_check", externalIdempotencyCheck)

	if externalIdempotencyCheck {
		var existingOrder models.ExtOrder
		query := `SELECT id, order_id, destination_phone, amount, status, error, processed_at FROM ext_orders WHERE order_id = ?`
		err := database.DB.QueryRow(query, req.OrderID).Scan(
			&existingOrder.ID,
			&existingOrder.OrderID,
			&existingOrder.DestinationPhone,
			&existingOrder.Amount,
			&existingOrder.Status,
			&existingOrder.Error,
			&existingOrder.ProcessedAt,
		)

		if err == nil {
			slog.Info(logPrefix+"External idempotency: Duplicate request detected, returning existing result", "order_id", req.OrderID, "existing_status", existingOrder.Status)

			var responseData interface{}
			if existingOrder.Status == "success" {
				responseData = models.SuccessData{
					OrderID:       existingOrder.OrderID,
					VendorOrderID: existingOrder.ID,
					ProcessedAt:   existingOrder.ProcessedAt.Format("2006-01-02 15:04:05.000 -07:00"),
				}
			}

			response := models.ExternalFulfillmentResponse{
				Status: existingOrder.Status,
				Error:  existingOrder.Error,
				Data:   responseData,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	} else {
		slog.Warn(logPrefix + "External idempotency check is disabled, processing all requests")
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	chance := rng.Intn(100)

	var status string
	var errorMsg string
	var responseData interface{}

	if chance < 10 {
		status = "error"
		errorMsg = "Random error occurred"
		slog.Info(logPrefix+"Random error generated", "order_id", req.OrderID)
	} else {
		status = "success"
		responseData = models.SuccessData{
			OrderID:       req.OrderID,
			VendorOrderID: 0,
			ProcessedAt:   time.Now().Format("2006-01-02 15:04:05.000 -07:00"),
		}
	}

	query := `INSERT INTO ext_orders (order_id, destination_phone, amount, status, error, processed_at) VALUES (?, ?, ?, ?, ?, ?)`
	processedAt := time.Now()

	slog.Info(logPrefix+"Storing order in database", "order_id", req.OrderID, "processed_at", processedAt.Format("2006-01-02 15:04:05 -0700"), "timezone", processedAt.Location().String())

	result, err := database.DB.Exec(query, req.OrderID, req.DestinationPhone, req.Amount, status, errorMsg, processedAt)
	if err != nil {
		slog.Error(logPrefix+"Failed to insert order", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if status == "success" {
		id, _ := result.LastInsertId()
		responseData = models.SuccessData{
			OrderID:       req.OrderID,
			VendorOrderID: int(id),
			ProcessedAt:   time.Now().Format("2006-01-02 15:04:05.000 -07:00"),
		}
	}

	slog.Info(logPrefix+"External fulfillment processed", "order_id", req.OrderID, "status", status, "external_idempotency_check", externalIdempotencyCheck)

	response := models.ExternalFulfillmentResponse{
		Status: status,
		Error:  errorMsg,
		Data:   responseData,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
