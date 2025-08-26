package models

import "time"

type Order struct {
	ID               string    `json:"id"`
	Amount           int       `json:"amount"`
	AdminFee         int       `json:"admin_fee"`
	Type             string    `json:"type"`
	Operator         string    `json:"operator"`
	DestinationPhone string    `json:"destination_phone"`
	Total            int       `json:"total"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
}

type CreateOrderResponse struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	Amount           int    `json:"amount"`
	AdminFee         int    `json:"admin_fee"`
	Total            int    `json:"total"`
	Operator         string `json:"operator"`
	DestinationPhone string `json:"destination_phone"`
}

type PaymentRequest struct {
	OrderID       string `json:"order-id"`
	PaidAmount    int    `json:"paid-amount"`
	CorrelationID string `json:"correlation-id"`
}

type PaymentResponse struct {
	Status string `json:"status"`
}

type PaymentPaidMessage struct {
	OrderID       string    `json:"order_id"`
	PaidAmount    int       `json:"paid_amount"`
	PaidAt        time.Time `json:"paid_at"`
	CorrelationID string    `json:"correlation_id"`
}

type ExternalFulfillmentRequest struct {
	OrderID          string `json:"order-id"`
	DestinationPhone string `json:"destination-phone-no"`
	Amount           int    `json:"amount"`
	CorrelationID    string `json:"correlation-id"`
}

type ExternalFulfillmentResponse struct {
	Status string      `json:"status"`
	Error  string      `json:"error"`
	Data   interface{} `json:"data"`
}

type SuccessData struct {
	OrderID       string `json:"order-id"`
	VendorOrderID int    `json:"vendor-order-id"`
	ProcessedAt   string `json:"processed_at"`
}

type ExtOrder struct {
	ID               int       `json:"id"`
	OrderID          string    `json:"order_id"`
	DestinationPhone string    `json:"destination_phone"`
	Amount           int       `json:"amount"`
	Status           string    `json:"status"`
	Error            string    `json:"error"`
	ProcessedAt      time.Time `json:"processed_at"`
}

type InternalPayment struct {
	ID         int       `json:"id"`
	OrderID    string    `json:"order_id"`
	PaidAmount int       `json:"paid_amount"`
	PaidAt     time.Time `json:"paid_at"`
}

type FulfillmentAttempt struct {
	ID            int       `json:"id"`
	OrderID       string    `json:"order_id"`
	AttemptNumber int       `json:"attempt_number"`
	Payload       string    `json:"payload"`
	AttemptedAt   time.Time `json:"attempted_at"`
}
