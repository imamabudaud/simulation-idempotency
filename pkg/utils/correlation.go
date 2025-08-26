package utils

import (
	"crypto/rand"
	"math/big"
)

func GenerateCorrelationID() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 6)
	
	for i := range result {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			idx = big.NewInt(int64(i * 17 % len(charset)))
		}
		result[i] = charset[idx.Int64()]
	}
	
	return string(result)
}
