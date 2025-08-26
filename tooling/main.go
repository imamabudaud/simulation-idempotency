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
		fmt.Println("  external-settlement        - Print today's external settlement")
		fmt.Println("  internal-settlement        - Print today's internal settlement")
		fmt.Println("  attempt                    - Print fulfillment attempts audit log")
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
	case "external-settlement":
		printExternalSettlement()
	case "internal-settlement":
		printInternalSettlement()
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

func printExternalSettlement() {
	checkDatabaseTimezone()
	today, startOfDay, endOfDay := getTodayDateRange()
	jakartaLoc := getJakartaLocation()

	slog.Info("Printing settlement", "date", today, "timezone", jakartaLoc.String(), "local_time", time.Now().Format("2006-01-02 15:04:05 -0700"))
	slog.Info("Date range for query", "start", startOfDay.Format("2006-01-02 15:04:05 -0700"), "end", endOfDay.Format("2006-01-02 15:04:05 -0700"))

	query := `SELECT id, order_id, amount, destination_phone, status, processed_at
			  FROM ext_orders
			  WHERE processed_at >= ? AND processed_at < ?
			  ORDER BY processed_at ASC`

	rows, err := database.DB.Query(query, startOfDay, endOfDay)
	if err != nil {
		slog.Error("Failed to query settlement", "error", err)
		return
	}
	defer rows.Close()

	table := NewTable("Settlement for " + today + " (" + jakartaLoc.String() + ")")
	table.AddColumn("ID", 3, "left", nil)
	table.AddColumn("Order ID", 38, "left", nil)
	table.AddColumn("Amount", 8, "right", nil)
	table.AddColumn("Status", 9, "left", nil)
	table.AddColumn("Destination", 15, "left", nil)
	table.AddColumn("Processed At", 21, "left", nil)

	table.PrintHeader()

	totalSettledAmount := 0
	totalFailedAmount := 0
	var orders []models.ExtOrder
	for rows.Next() {
		var order models.ExtOrder
		if err := rows.Scan(&order.ID, &order.OrderID, &order.Amount, &order.DestinationPhone, &order.Status, &order.ProcessedAt); err != nil {
			slog.Error("Failed to scan order", "error", err)
			continue
		}
		if order.Status == "success" {
			totalSettledAmount += order.Amount
		} else {
			totalFailedAmount += order.Amount
		}
		orders = append(orders, order)
	}

	if len(orders) == 0 {
		table.PrintEmptyRow("No orders processed today")
	} else {
		for _, order := range orders {
			processedTime := order.ProcessedAt.In(jakartaLoc)
			table.PrintRow([]interface{}{
				order.ID,
				truncateString(order.OrderID, 36),
				order.Amount,
				order.Status,
				truncateString(order.DestinationPhone, 16),
				processedTime.Format("2006-01-02 15:04:05"),
			})
		}
	}

	table.PrintFooter()
	fmt.Printf("Total orders: %d\n", len(orders))
	fmt.Println("Total settled amount: ", totalSettledAmount)
	fmt.Println("Total failed amount: ", totalFailedAmount)
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

	table := NewTable("Internal Settlement for " + today + " (" + jakartaLoc.String() + ")")
	table.AddColumn("Order ID", 38, "left", nil)
	table.AddColumn("Amount", 7, "right", nil)
	table.AddColumn("Fee", 4, "right", nil)
	table.AddColumn("Status", 12, "left", nil)
	table.AddColumn("Operator", 10, "left", nil)
	table.AddColumn("Destination", 15, "left", nil)
	table.AddColumn("Created At", 21, "left", nil)

	table.PrintHeader()

	var orders []models.Order
	totalFulfilledAmount := 0
	for rows.Next() {
		var order models.Order
		if err := rows.Scan(&order.ID, &order.Amount, &order.AdminFee, &order.Type, &order.Operator, &order.DestinationPhone, &order.Total, &order.Status, &order.CreatedAt); err != nil {
			slog.Error("Failed to scan internal order", "error", err)
			continue
		}
		if order.Status == "fulfilment" {
			totalFulfilledAmount += order.Total
		}
		orders = append(orders, order)
	}

	if len(orders) == 0 {
		table.PrintEmptyRow("No internal orders created today")
	} else {
		for _, order := range orders {
			createdTime := order.CreatedAt.In(jakartaLoc)
			table.PrintRow([]interface{}{
				truncateString(order.ID, 36),
				order.Amount,
				order.AdminFee,
				order.Status,
				truncateString(order.Operator, 8),
				truncateString(order.DestinationPhone, 16),
				createdTime.Format("2006-01-02 15:04:05"),
			})
		}
	}

	table.PrintFooter()
	fmt.Printf("Total internal orders: %d\n", len(orders))
	fmt.Println("Total fulfilled amount: ", totalFulfilledAmount)
}

func printAttempt() {
	checkDatabaseTimezone()
	today, startOfDay, endOfDay := getTodayDateRange()
	jakartaLoc := getJakartaLocation()

	slog.Info("Printing fulfillment audit", "date", today, "timezone", jakartaLoc.String())

	query := `SELECT 
				fa.id, 
				fa.order_id, 
				fa.payload, 
				fa.attempted_at,
				ROW_NUMBER() OVER (PARTITION BY fa.order_id ORDER BY fa.attempted_at ASC) as attempt_number
			  FROM fulfillment_attempts fa
			  WHERE fa.attempted_at >= ? AND fa.attempted_at < ?
			  ORDER BY fa.order_id, fa.attempted_at DESC`

	rows, err := database.DB.Query(query, startOfDay, endOfDay)
	if err != nil {
		slog.Error("Failed to query fulfillment attempts", "error", err)
		return
	}
	defer rows.Close()

	table := NewTable("Fulfillment Audit for " + today + " (" + jakartaLoc.String() + ")")
	table.AddColumn("ID", 3, "left", nil)
	table.AddColumn("Order ID", 38, "left", nil)
	table.AddColumn("Attempt", 9, "right", nil)
	table.AddColumn("Payload", 60, "left", nil)
	table.AddColumn("Attempted At", 21, "left", nil)

	table.PrintHeader()

	var attempts []struct {
		ID            int
		OrderID       string
		Payload       string
		AttemptedAt   time.Time
		AttemptNumber int
	}

	for rows.Next() {
		var attempt struct {
			ID            int
			OrderID       string
			Payload       string
			AttemptedAt   time.Time
			AttemptNumber int
		}
		if err := rows.Scan(&attempt.ID, &attempt.OrderID, &attempt.Payload, &attempt.AttemptedAt, &attempt.AttemptNumber); err != nil {
			slog.Error("Failed to scan fulfillment attempt", "error", err)
			continue
		}
		attempts = append(attempts, attempt)
	}

	if len(attempts) == 0 {
		table.PrintEmptyRow("No fulfillment attempts today")
	} else {
		for _, attempt := range attempts {
			attemptedTime := attempt.AttemptedAt.In(jakartaLoc)
			table.PrintRow([]interface{}{
				attempt.ID,
				truncateString(attempt.OrderID, 36),
				attempt.AttemptNumber,
				truncateString(attempt.Payload, 58),
				attemptedTime.Format("2006-01-02 15:04:05"),
			})
		}
	}

	table.PrintFooter()
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

type TableColumn struct {
	Header     string
	Width      int
	Alignment  string // "left", "right", "center"
	FormatFunc func(interface{}) string
}

type Table struct {
	columns []TableColumn
	title   string
}

func NewTable(title string) *Table {
	return &Table{
		title:   title,
		columns: make([]TableColumn, 0),
	}
}

func (t *Table) AddColumn(header string, width int, alignment string, formatFunc func(interface{}) string) {
	t.columns = append(t.columns, TableColumn{
		Header:     header,
		Width:      width,
		Alignment:  alignment,
		FormatFunc: formatFunc,
	})
}

func (t *Table) PrintHeader() {
	if t.title != "" {
		fmt.Printf("%s:\n", t.title)
	}

	// Print top border
	fmt.Print("┌")
	for i, col := range t.columns {
		if i > 0 {
			fmt.Print("┬")
		}
		fmt.Print(strings.Repeat("─", col.Width))
	}
	fmt.Println("┐")

	// Print header row
	fmt.Print("│")
	for i, col := range t.columns {
		if i > 0 {
			fmt.Print("│")
		}
		fmt.Printf(" %-*s", col.Width-1, col.Header)
	}
	fmt.Println("│")

	// Print separator
	fmt.Print("├")
	for i, col := range t.columns {
		if i > 0 {
			fmt.Print("┼")
		}
		fmt.Print(strings.Repeat("─", col.Width))
	}
	fmt.Println("┤")
}

func (t *Table) PrintRow(data []interface{}) {
	if len(data) != len(t.columns) {
		return
	}

	fmt.Print("│")
	for i, col := range t.columns {
		if i > 0 {
			fmt.Print("│")
		}

		var value string
		if col.FormatFunc != nil {
			value = col.FormatFunc(data[i])
		} else {
			value = fmt.Sprintf("%v", data[i])
		}

		switch col.Alignment {
		case "right":
			fmt.Printf(" %*s", col.Width-1, value)
		case "center":
			padding := col.Width - len(value)
			left := padding / 2
			right := padding - left
			fmt.Printf(" %*s%s%*s", left, "", value, right, "")
		default: // left
			fmt.Printf(" %-*s", col.Width-1, value)
		}
	}
	fmt.Println("│")
}

func (t *Table) PrintEmptyRow(message string) {
	// Calculate total width for the message
	totalWidth := 0
	for _, col := range t.columns {
		totalWidth += col.Width
	}
	totalWidth += len(t.columns) - 1 // Add separator characters

	// Center the message
	padding := totalWidth - len(message)
	left := padding / 2
	right := padding - left

	fmt.Print("│")
	for i, col := range t.columns {
		if i > 0 {
			fmt.Print("│")
		}
		if i == 0 {
			fmt.Printf(" %*s%s%*s", left, "", message, right, "")
		} else {
			fmt.Printf(" %*s", col.Width-1, "")
		}
	}
	fmt.Println("│")
}

func (t *Table) PrintFooter() {
	fmt.Print("└")
	for i, col := range t.columns {
		if i > 0 {
			fmt.Print("┴")
		}
		fmt.Print(strings.Repeat("─", col.Width))
	}
	fmt.Println("┘")
}
