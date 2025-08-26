package utils

import (
	"fmt"
	"math/rand"
	"time"

	"substack-idempotency/pkg/models"

	"github.com/google/uuid"
)

func GenerateRandomOrder() models.Order {
	operators := []string{"indosat", "xl", "three"}
	operator := operators[time.Now().UnixNano()%3]

	var adminFee int
	switch operator {
	case "indosat":
		adminFee = 100
	case "xl":
		adminFee = 200
	case "three":
		adminFee = 300
	}

	amounts := []int{1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000, 9000}
	amount := amounts[time.Now().UnixNano()%9]

	var phonePrefix string
	switch operator {
	case "indosat":
		phonePrefix = "0815-000-00"
	case "xl":
		phonePrefix = "0817-000-00"
	case "three":
		phonePrefix = "0896-000-00"
	}

	phoneSuffix := fmt.Sprintf("%02d", (time.Now().UnixNano()%99)+1)
	destinationPhone := phonePrefix + phoneSuffix

	return models.Order{
		ID:               GenerateUUID7(),
		Amount:           amount,
		AdminFee:         adminFee,
		Type:             "phone_credit",
		Operator:         operator,
		DestinationPhone: destinationPhone,
		Total:            amount + adminFee,
		Status:           "pending",
		CreatedAt:        time.Now(),
	}
}

func GenerateUUID7() string {
	u, err := uuid.NewV7()
	if err != nil {
		return ""
	}
	return u.String()
}

func DeterminePublishCount() int {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	chance := r.Intn(100)

	if chance < 70 {
		return 1
	} else if chance < 90 {
		return 2
	} else {
		return 3
	}
}
