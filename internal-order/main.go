package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"substack-idempotency/pkg/database"
	"substack-idempotency/pkg/httpclient"
	"substack-idempotency/pkg/models"
	"substack-idempotency/pkg/nats"
	"substack-idempotency/pkg/utils"

	"github.com/joho/godotenv"
	natspkg "github.com/nats-io/nats.go"
)

var idempotencyCheck bool

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

	idempotencyCheck = os.Getenv("IDEMPOTENCY_CHECK") == "true"

	slog.Info("Internal Order Service configuration", "idempotency_check", idempotencyCheck)

	sub, err := nats.Subscribe("payment.paid", handlePaymentPaid)
	if err != nil {
		slog.Error("Failed to subscribe to payment.paid", "error", err)
		os.Exit(1)
	}
	defer sub.Unsubscribe()

	http.HandleFunc("/create-order", createOrder)
	http.HandleFunc("/health", healthCheck)

	slog.Info("Internal Order Service starting on port 8000")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		slog.Error("Failed to start server", "error", err)
	}
}

func createOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	correlationID := utils.GenerateCorrelationID()
	logPrefix := "[" + correlationID + "] "
	order := utils.GenerateRandomOrder()

	slog.Info(logPrefix+"Creating order", "order", order)

	query := `INSERT INTO internal_orders (id, amount, admin_fee, type, operator, destination_phone, total, status, created_at) 
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := database.DB.Exec(query, order.ID, order.Amount, order.AdminFee, order.Type,
		order.Operator, order.DestinationPhone, order.Total, order.Status, order.CreatedAt); err != nil {
		slog.Error(logPrefix+"Failed to insert order", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	slog.Info(logPrefix+"Order created successfully", "order_id", order.ID)

	response := models.CreateOrderResponse{
		ID:               order.ID,
		Status:           order.Status,
		Amount:           order.Amount,
		AdminFee:         order.AdminFee,
		Total:            order.Total,
		Operator:         order.Operator,
		DestinationPhone: order.DestinationPhone,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func handlePaymentPaid(msg *natspkg.Msg) {
	var paymentMsg models.PaymentPaidMessage
	if err := json.Unmarshal(msg.Data, &paymentMsg); err != nil {
		slog.Error("Failed to unmarshal payment message", "error", err)
		return
	}

	correlationID := paymentMsg.CorrelationID
	if correlationID == "" {
		correlationID = utils.GenerateCorrelationID()
	}

	logPrefix := "[" + correlationID + "] "

	slog.Info(logPrefix+"Received payment.paid message", "order_id", paymentMsg.OrderID)

	query := `UPDATE internal_orders SET status = 'paid' WHERE id = ?`
	if _, err := database.DB.Exec(query, paymentMsg.OrderID); err != nil {
		slog.Error(logPrefix+"Failed to update order status", "error", err)
		return
	}

	slog.Info(logPrefix+"Order status updated to paid", "order_id", paymentMsg.OrderID)

	fulfillmentPayload := fmt.Sprintf(`{"order_id":"%s","amount":%d,"destination_phone":"%s"}`,
		paymentMsg.OrderID, 0, "")

	query = `INSERT INTO fulfillment_attempts (order_id, attempt_number, payload) VALUES (?, ?, ?)`
	if _, err := database.DB.Exec(query, paymentMsg.OrderID, 1, fulfillmentPayload); err != nil {
		slog.Error(logPrefix+"Failed to insert fulfillment attempt", "error", err)
	}

	if idempotencyCheck {
		var attemptCount int
		query := `SELECT COUNT(*) FROM fulfillment_attempts WHERE order_id = ?`
		err := database.DB.QueryRow(query, paymentMsg.OrderID).Scan(&attemptCount)

		if err == nil && attemptCount > 1 {
			slog.Info(logPrefix+"Order already processed for fulfillment", "order_id", paymentMsg.OrderID, "attempt_count", attemptCount)
			return
		}
	} else {
		slog.Warn(logPrefix + "Idempotency check is disabled, release the kraken!!")
	}

	go processFulfillment(paymentMsg.OrderID, correlationID)
}

func processFulfillment(orderID string, correlationID string) {
	logPrefix := "[" + correlationID + "] "

	var order models.Order
	query := `SELECT id, amount, destination_phone FROM internal_orders WHERE id = ?`
	if err := database.DB.QueryRow(query, orderID).Scan(&order.ID, &order.Amount, &order.DestinationPhone); err != nil {
		slog.Error(logPrefix+"Failed to get order for fulfillment", "error", err)
		return
	}

	fulfillmentReq := models.ExternalFulfillmentRequest{
		OrderID:          order.ID,
		DestinationPhone: order.DestinationPhone,
		Amount:           order.Amount,
		CorrelationID:    correlationID,
	}

	var attemptNumber int
	query = `SELECT COUNT(*) FROM fulfillment_attempts WHERE order_id = ?`
	if err := database.DB.QueryRow(query, orderID).Scan(&attemptNumber); err != nil {
		attemptNumber = 1
	} else {
		attemptNumber++
	}

	slog.Info(logPrefix+"Processing fulfillment attempt no. "+strconv.Itoa(attemptNumber), "order_id", orderID)

	fulfillmentPayload, _ := json.Marshal(fulfillmentReq)
	payloadStr := string(fulfillmentPayload)

	query = `INSERT INTO fulfillment_attempts (order_id, attempt_number, payload) VALUES (?, ?, ?)`
	if _, err := database.DB.Exec(query, orderID, attemptNumber, payloadStr); err != nil {
		slog.Error(logPrefix+"Failed to insert fulfillment attempt", "error", err)
	}

	slog.Info(logPrefix+"Calling external fulfillment service", "order_id", orderID)

	client := httpclient.NewClient(5 * time.Second)
	resp, err := client.PostJSONWithTimeout("http://localhost:9000/process-order", fulfillmentReq, 5*time.Second)
	if err != nil {
		slog.Error(logPrefix+"Failed to call external fulfillment", "error", err, "attempt_number", attemptNumber)
		return
	}

	if resp.StatusCode == http.StatusOK {
		query := `UPDATE internal_orders SET status = 'fulfilment' WHERE id = ?`
		if _, err := database.DB.Exec(query, orderID); err != nil {
			slog.Error(logPrefix+"Failed to update order status to fulfilment", "error", err)
		}
		slog.Info(logPrefix+"Order fulfillment processed", "order_id", orderID, "attempt_number", attemptNumber)
	}
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
