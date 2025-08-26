package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"substack-idempotency/pkg/database"
	"substack-idempotency/pkg/httpclient"
	"substack-idempotency/pkg/models"
	"substack-idempotency/pkg/utils"

	"github.com/joho/godotenv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <command> [args]")
		fmt.Println("Commands:")
		fmt.Println("  resetdb                    - Reset all database tables")
		fmt.Println("  simulator <count>          - Run simulation with specified count")
		fmt.Println("  settlement                 - Print today's external settlement")
		fmt.Println("  internal-settlement        - Print today's internal settlement")
		fmt.Println("  full-settlement            - Print comprehensive settlement (both)")
		fmt.Println("  fulfillment-audit          - Print fulfillment attempts audit log")
		os.Exit(1)
	}

	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found")
	}

	if err := database.Init(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	command := os.Args[1]
	switch command {
	case "resetdb":
		resetDB()
	case "simulator":
		if len(os.Args) < 3 {
			fmt.Println("Usage: go run main.go simulator <count>")
			os.Exit(1)
		}
		count, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Println("Invalid count:", os.Args[2])
			os.Exit(1)
		}
		runSimulator(count)
	case "settlement":
		printSettlement()
	case "internal-settlement":
		printInternalSettlement()
	case "full-settlement":
		printFullSettlement()
	case "attempt":
		printAttempt()
	default:
		fmt.Println("Unknown command:", command)
		os.Exit(1)
	}
}

func resetDB() {
	if err := database.ResetTables(); err != nil {
		slog.Error("Failed to reset database", "error", err)
		return
	}
	fmt.Println("Database reset completed")
}

func runSimulator(count int) {
	fmt.Printf("Starting simulation with %d iterations using 4 goroutines\n", count)

	chunkSize := count / 4
	if count%4 != 0 {
		chunkSize++
	}

	var wg sync.WaitGroup
	results := make(chan string, count)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		start := i * chunkSize
		end := start + chunkSize
		if end > count {
			end = count
		}

		go func(start, end int) {
			defer wg.Done()
			for j := start; j < end; j++ {
				runSimulationIteration(j+1, results)
			}
		}(start, end)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	successCount := 0
	timeoutCount := 0

	for result := range results {
		if strings.Contains(result, "SUCCESS") {
			successCount++
		} else if strings.Contains(result, "TIMEOUT") {
			timeoutCount++
		}
		fmt.Println(result)
	}

	fmt.Printf("\nSimulation completed. Success: %d, Timeouts: %d\n", successCount, timeoutCount)
}

func runSimulationIteration(iteration int, results chan<- string) {
	correlationID := utils.GenerateCorrelationID()
	logPrefix := "[" + correlationID + "] "

	orderResp, err := createOrder()
	if err != nil {
		results <- fmt.Sprintf("Iteration %d [%s]: FAILED to create order - %v", iteration, correlationID, err)
		return
	}

	slog.Info(logPrefix+"Order created for simulation", "iteration", iteration, "order_id", orderResp.ID)

	time.Sleep(100 * time.Millisecond)

	success := false
	for retry := 0; retry < 3; retry++ {
		if retry > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		err := triggerPayment(orderResp.ID, orderResp.Total)
		if err == nil {
			success = true
			results <- fmt.Sprintf("Iteration %d [%s]: SUCCESS - Order: %s, Amount: %d", iteration, correlationID, orderResp.ID, orderResp.Total)
			break
		}

		if strings.Contains(err.Error(), "timeout") {
			if retry == 2 {
				results <- fmt.Sprintf("Iteration %d [%s]: TIMEOUT after 3 retries - Order: %s", iteration, correlationID, orderResp.ID)
			}
		} else {
			results <- fmt.Sprintf("Iteration %d [%s]: FAILED payment - %v", iteration, correlationID, err)
			break
		}
	}

	if !success {
		results <- fmt.Sprintf("Iteration %d [%s]: FAILED - Order: %s", iteration, correlationID, orderResp.ID)
	}
}

func createOrder() (*models.CreateOrderResponse, error) {
	correlationID := utils.GenerateCorrelationID()
	logPrefix := "[" + correlationID + "] "

	client := httpclient.NewClient(500 * time.Millisecond)
	resp, err := client.PostJSONWithTimeout("http://localhost:8000/create-order", nil, 500*time.Millisecond)
	if err != nil {
		slog.Error(logPrefix+"Failed to create order", "error", err)
		return nil, err
	}

	var orderResp models.CreateOrderResponse
	if err := client.DecodeJSONResponse(resp, &orderResp); err != nil {
		slog.Error(logPrefix+"Failed to decode order response", "error", err)
		return nil, err
	}

	slog.Info(logPrefix+"Order created successfully", "order_id", orderResp.ID)
	return &orderResp, nil
}

func triggerPayment(orderID string, amount int) error {
	correlationID := utils.GenerateCorrelationID()
	logPrefix := "[" + correlationID + "] "

	paymentReq := models.PaymentRequest{
		OrderID:       orderID,
		PaidAmount:    amount,
		CorrelationID: correlationID,
	}

	slog.Info(logPrefix+"Triggering payment", "order_id", orderID, "amount", amount)

	timeoutMs := 200
	if envTimeout := os.Getenv("PAYMENT_TIMEOUT_MS"); envTimeout != "" {
		if t, err := strconv.Atoi(envTimeout); err == nil {
			timeoutMs = t
		}
	}

	client := httpclient.NewClient(time.Duration(timeoutMs) * time.Millisecond)
	resp, err := client.PostJSONWithTimeout("http://localhost:8001/trigger-payment-paid", paymentReq, time.Duration(timeoutMs)*time.Millisecond)
	if err != nil {
		if client.IsTimeoutError(err) {
			slog.Error(logPrefix+"Payment timeout", "order_id", orderID)
			return fmt.Errorf("timeout")
		}
		slog.Error(logPrefix+"Payment failed", "order_id", orderID, "error", err)
		return err
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error(logPrefix+"Payment service error", "order_id", orderID, "status", resp.StatusCode)
		return fmt.Errorf("payment service returned status: %d", resp.StatusCode)
	}

	slog.Info(logPrefix+"Payment triggered successfully", "order_id", orderID)
	return nil
}

func printSettlement() {
	checkDatabaseTimezone()
	today, startOfDay, endOfDay := getTodayDateRange()
	jakartaLoc := getJakartaLocation()

	slog.Info("Printing settlement", "date", today, "timezone", jakartaLoc.String(), "local_time", time.Now().Format("2006-01-02 15:04:05 -0700"))
	slog.Info("Date range for query", "start", startOfDay.Format("2006-01-02 15:04:05 -0700"), "end", endOfDay.Format("2006-01-02 15:04:05 -0700"))

	query := `SELECT id, order_id, amount, destination_phone, processed_at
			  FROM ext_orders
			  WHERE processed_at >= ? AND processed_at < ?
			  ORDER BY processed_at ASC`

	rows, err := database.DB.Query(query, startOfDay, endOfDay)
	if err != nil {
		slog.Error("Failed to query settlement", "error", err)
		return
	}
	defer rows.Close()

	fmt.Printf("Settlement for %s (%s):\n", today, jakartaLoc.String())
	fmt.Println("┌─────┬──────────────────────────────────────┬────────┬──────────────────┬─────────────────────────────┐")
	fmt.Println("│ ID  │ Order ID                             │ Amount │ Destination      │ Processed At                │")
	fmt.Println("├─────┼──────────────────────────────────────┼────────┼──────────────────┼─────────────────────────────┤")

	var orders []models.ExtOrder
	for rows.Next() {
		var order models.ExtOrder
		if err := rows.Scan(&order.ID, &order.OrderID, &order.Amount, &order.DestinationPhone, &order.ProcessedAt); err != nil {
			slog.Error("Failed to scan order", "error", err)
			continue
		}
		orders = append(orders, order)
	}

	if len(orders) == 0 {
		fmt.Println("│     │ No orders processed today            │        │                  │                             │")
	} else {
		for _, order := range orders {
			processedTime := order.ProcessedAt.In(jakartaLoc)
			fmt.Printf("│ %3d │ %-36s │ %6d │ %-16s │ %-27s │\n",
				order.ID,
				truncateString(order.OrderID, 36),
				order.Amount,
				truncateString(order.DestinationPhone, 16),
				processedTime.Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println("└─────┴──────────────────────────────────────┴────────┴──────────────────┴─────────────────────────────┘")
	fmt.Printf("Total orders: %d\n", len(orders))
}

func printInternalSettlement() {
	checkDatabaseTimezone()
	today, startOfDay, endOfDay := getTodayDateRange()
	jakartaLoc := getJakartaLocation()

	slog.Info("Printing internal settlement", "date", today, "timezone", jakartaLoc.String(), "local_time", time.Now().Format("2006-01-02 15:04:05 -0700"))
	slog.Info("Date range for query", "start", startOfDay.Format("2006-01-02 15:04:05 -0700"), "end", endOfDay.Format("2006-01-02 15:04:05 -0700"))

	query := `SELECT id, amount, admin_fee, type, operator, destination_phone, total, status, created_at
			  FROM internal_orders
			  WHERE created_at >= ? AND created_at < ?
			  ORDER BY created_at ASC`

	rows, err := database.DB.Query(query, startOfDay, endOfDay)
	if err != nil {
		slog.Error("Failed to query internal settlement", "error", err)
		return
	}
	defer rows.Close()

	fmt.Printf("Internal Settlement for %s (%s):\n", today, jakartaLoc.String())
	fmt.Println("┌─────┬────────┬──────────┬──────────┬──────────────────┬─────────────────────────────┐")
	fmt.Println("│ ID  │ Amount │ Admin Fee│ Operator │ Destination      │ Created At                  │")
	fmt.Println("├─────┼────────┼──────────┼──────────┼──────────────────┼─────────────────────────────┤")

	var orders []models.Order
	for rows.Next() {
		var order models.Order
		if err := rows.Scan(&order.ID, &order.Amount, &order.AdminFee, &order.Type, &order.Operator, &order.DestinationPhone, &order.Total, &order.Status, &order.CreatedAt); err != nil {
			slog.Error("Failed to scan internal order", "error", err)
			continue
		}
		orders = append(orders, order)
	}

	if len(orders) == 0 {
		fmt.Println("│ No internal orders created today                                                                    │")
	} else {
		for _, order := range orders {
			createdTime := order.CreatedAt.In(jakartaLoc)
			fmt.Printf("│ %-36s │ %6d │ %8d │ %-8s │ %-16s │ %-27s │\n",
				truncateString(order.ID, 36),
				order.Amount,
				order.AdminFee,
				truncateString(order.Operator, 8),
				truncateString(order.DestinationPhone, 16),
				createdTime.Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println("└──────────────────────────────────────┴────────┴──────────┴──────────┴──────────────────┴─────────────────────────────┘")
	fmt.Printf("Total internal orders: %d\n", len(orders))
}

func printFullSettlement() {
	checkDatabaseTimezone()
	today, startOfDay, endOfDay := getTodayDateRange()
	jakartaLoc := getJakartaLocation()

	slog.Info("Printing comprehensive settlement", "date", today, "timezone", jakartaLoc.String(), "local_time", time.Now().Format("2006-01-02 15:04:05 -0700"))
	slog.Info("Date range for query", "start", startOfDay.Format("2006-01-02 15:04:05 -0700"), "end", endOfDay.Format("2006-01-02 15:04:05 -0700"))

	queryExt := `SELECT id, order_id, amount, destination_phone, processed_at
				  FROM ext_orders
				  WHERE processed_at >= ? AND processed_at < ?
				  ORDER BY processed_at ASC`

	queryInt := `SELECT id, amount, admin_fee, type, operator, destination_phone, total, status, created_at
				  FROM internal_orders
				  WHERE created_at >= ? AND created_at < ?
				  ORDER BY created_at ASC`

	rowsExt, err := database.DB.Query(queryExt, startOfDay, endOfDay)
	if err != nil {
		slog.Error("Failed to query external settlement", "error", err)
		return
	}
	defer rowsExt.Close()

	rowsInt, err := database.DB.Query(queryInt, startOfDay, endOfDay)
	if err != nil {
		slog.Error("Failed to query internal settlement", "error", err)
		return
	}
	defer rowsInt.Close()

	fmt.Printf("Comprehensive Settlement for %s (%s):\n", today, jakartaLoc.String())

	fmt.Println("EXTERNAL ORDERS (From Vendor Report):")
	fmt.Println("┌─────┬──────────────────────────────────────┬────────┬──────────────────┬─────────────────────────────┐")
	fmt.Println("│ ID  │ Order ID                             │ Amount │ Destination      │ Processed At                │")
	fmt.Println("├─────┼──────────────────────────────────────┼────────┼──────────────────┼─────────────────────────────┤")

	var extOrders []models.ExtOrder
	for rowsExt.Next() {
		var order models.ExtOrder
		if err := rowsExt.Scan(&order.ID, &order.OrderID, &order.Amount, &order.DestinationPhone, &order.ProcessedAt); err != nil {
			slog.Error("Failed to scan external order", "error", err)
			continue
		}
		extOrders = append(extOrders, order)
	}

	if len(extOrders) == 0 {
		fmt.Println("│     │ No external orders processed today   │        │                  │                             │")
	} else {
		for _, order := range extOrders {
			processedTime := order.ProcessedAt.In(jakartaLoc)
			fmt.Printf("│ %3d │ %-36s │ %6d │ %-16s │ %-27s │\n",
				order.ID,
				truncateString(order.OrderID, 36),
				order.Amount,
				truncateString(order.DestinationPhone, 16),
				processedTime.Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println("└─────┴──────────────────────────────────────┴────────┴──────────────────┴─────────────────────────────┘")
	fmt.Printf("Total external orders: %d\n\n", len(extOrders))

	fmt.Println("INTERNAL ORDERS (Your Company):")
	fmt.Println("┌──────────────────────────────────────┬────────┬──────────┬──────────┬──────────────────┬─────────────────────────────┐")
	fmt.Println("│ Order ID                             │ Amount │ Admin Fee│ Operator │ Destination      │ Created At                  │")
	fmt.Println("├──────────────────────────────────────┼────────┼──────────┼──────────┼──────────────────┼─────────────────────────────┤")

	var intOrders []models.Order
	for rowsInt.Next() {
		var order models.Order
		if err := rowsInt.Scan(&order.ID, &order.Amount, &order.AdminFee, &order.Type, &order.Operator, &order.DestinationPhone, &order.Total, &order.Status, &order.CreatedAt); err != nil {
			slog.Error("Failed to scan internal order", "error", err)
			continue
		}
		intOrders = append(intOrders, order)
	}

	if len(intOrders) == 0 {
		fmt.Println("│ No internal orders created today                                                                    │")
	} else {
		for _, order := range intOrders {
			createdTime := order.CreatedAt.In(jakartaLoc)
			fmt.Printf("│ %-36s │ %6d │ %8d │ %-8s │ %-16s │ %-27s │\n",
				truncateString(order.ID, 36),
				order.Amount,
				order.AdminFee,
				truncateString(order.Operator, 8),
				truncateString(order.DestinationPhone, 16),
				createdTime.Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println("└──────────────────────────────────────┴────────┴──────────┴──────────┴──────────────────┴─────────────────────────────┘")
	fmt.Printf("Total internal orders: %d\n\n", len(intOrders))

	fmt.Printf("SUMMARY: External: %d, Internal: %d\n", len(extOrders), len(intOrders))
}

func printAttempt() {
	checkDatabaseTimezone()
	today, startOfDay, endOfDay := getTodayDateRange()
	jakartaLoc := getJakartaLocation()

	slog.Info("Printing fulfillment audit", "date", today, "timezone", jakartaLoc.String())

	query := `SELECT id, order_id, attempt_number, payload, attempted_at
			  FROM fulfillment_attempts
			  WHERE attempted_at >= ? AND attempted_at < ?
			  ORDER BY attempted_at DESC`

	rows, err := database.DB.Query(query, startOfDay, endOfDay)
	if err != nil {
		slog.Error("Failed to query fulfillment attempts", "error", err)
		return
	}
	defer rows.Close()

	fmt.Printf("Fulfillment Audit for %s (%s):\n", today, jakartaLoc.String())
	fmt.Println("┌─────┬──────────────────────────────────────┬──────────────┬──────────────────────────────────────┬─────────────────────────────┐")
	fmt.Println("│ ID  │ Order ID                             │ Attempt #    │ Payload                              │ Attempted At                │")
	fmt.Println("├─────┼──────────────────────────────────────┼──────────────┼──────────────────────────────────────┼─────────────────────────────┤")

	var attempts []models.FulfillmentAttempt
	for rows.Next() {
		var attempt models.FulfillmentAttempt
		if err := rows.Scan(&attempt.ID, &attempt.OrderID, &attempt.AttemptNumber, &attempt.Payload, &attempt.AttemptedAt); err != nil {
			slog.Error("Failed to scan fulfillment attempt", "error", err)
			continue
		}
		attempts = append(attempts, attempt)
	}

	if len(attempts) == 0 {
		fmt.Println("│     │ No fulfillment attempts today        │              │                                      │                             │")
	} else {
		for _, attempt := range attempts {
			attemptedTime := attempt.AttemptedAt.In(jakartaLoc)
			fmt.Printf("│ %3d │ %-36s │ %12d │ %-36s │ %-27s │\n",
				attempt.ID,
				truncateString(attempt.OrderID, 36),
				attempt.AttemptNumber,
				truncateString(attempt.Payload, 36),
				attemptedTime.Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println("└─────┴──────────────────────────────────────┴──────────────┴──────────────────────────────────────┴─────────────────────────────┘")
	fmt.Printf("Total fulfillment attempts: %d\n", len(attempts))
}

func getJakartaLocation() *time.Location {
	jakartaLoc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		jakartaLoc = time.FixedZone("Asia/Jakarta", 7*60*60)
	}
	return jakartaLoc
}

func getTodayDateRange() (string, time.Time, time.Time) {
	jakartaLoc := getJakartaLocation()
	now := time.Now().In(jakartaLoc)
	today := now.Format("2006-01-02")

	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, jakartaLoc)
	endOfDay := startOfDay.Add(24 * time.Hour)

	return today, startOfDay, endOfDay
}

func checkDatabaseTimezone() {
	var timezone string
	err := database.DB.QueryRow("SELECT @@time_zone").Scan(&timezone)
	if err != nil {
		slog.Error("Failed to get database timezone", "error", err)
		return
	}

	var currentTime time.Time
	err = database.DB.QueryRow("SELECT NOW()").Scan(&currentTime)
	if err != nil {
		slog.Error("Failed to get database current time", "error", err)
		return
	}

	slog.Info("Database timezone info", "timezone", timezone, "current_time", currentTime.Format("2006-01-02 15:04:05 -0700"))
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
