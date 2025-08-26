# Substack Idempotency Simulation

A Go-based simulation system for testing idempotency in order fulfillment workflows with clean, modular architecture.

## Architecture

- **Internal Order Service** (Port 8000): Creates orders and processes payment messages
- **Internal Payment Service** (Port 8001): Simulates payment processing with configurable timeouts
- **External Order Fulfillment Service** (Port 9000): Handles order fulfillment with idempotency
- **Tooling**: Database reset, simulation runner, and settlement printer


## Prerequisites

- Go 1.24+
- Docker and Docker Compose
- MySQL 8
- NATS

## Setup

1. Start infrastructure services:
```bash
docker-compose up -d
```

2. Install Go dependencies:
```bash
go mod tidy
```

3. Copy environment configuration:
```bash
cp env.example .env
```

4. Update `.env` file with your database credentials if needed.

## Environment Configuration

Copy `env.example` to `.env` and configure:

- **Database**: MySQL connection details
- **NATS**: Message broker configuration  
- **IDEMPOTENCY_CHECK**: Internal service idempotency (client-side)
- **EXTERNAL_IDEMPOTENCY_CHECK**: External service idempotency (server-side)
- **PAYMENT_TIMEOUT_MS**: Payment processing timeout

## Idempotency Features

The system implements **dual-layer idempotency** to simulate real-world scenarios:

### Client-Side Idempotency (`IDEMPOTENCY_CHECK`)
- **Internal Order Service**: Prevents duplicate fulfillment processing
- **Location**: Client company (your internal systems)
- **Purpose**: Prevent duplicate external API calls

### Server-Side Idempotency (`EXTERNAL_IDEMPOTENCY_CHECK`)  
- **External Fulfillment Service**: Prevents duplicate order processing
- **Location**: External company (vendor/supplier systems)
- **Purpose**: Prevent duplicate order creation

### Configuration Scenarios

| Internal | External | Behavior |
|----------|----------|----------|
| `true`   | `true`   | Full idempotency (production) |
| `true`   | `false`  | Client prevents duplicates, server processes all |
| `false`  | `true`   | Client allows duplicates, server prevents duplicates |
| `false`  | `false`  | No idempotency (testing/debugging) |


## Running Services

Set your `.env`, then run the services

### Internal Order Service
```bash
go run internal-order/main.go
```

### Internal Payment Service
```bash
go run internal-payment/main.go
```

### External Order Fulfillment Service
```bash
go run external-order-fulfilment/main.go
```

## Tooling

### Reset Database
```bash
go run tooling/main.go resetdb
```

This will recreate the database, hence destroying all data

### Run Simulation
```bash
go run tooling/main.go simulator 100
```

### Print Internal Settlement
```bash
go run tooling/main.go internal-settlement
```

### Print External Settlement
```bash
go run tooling/main.go external-settlement
```

### Print Full Settlement
```bash
go run tooling/main.go full-settlement
```


## Testing

Test payment trigger manually:
```bash
chmod +x test-payment.sh
./test-payment.sh
```

## Configuration

- `IDEMPOTENCY_CHECK`: Enable/disable idempotency checking (default: true) for our internal service pov
- `EXTERNAL_IDEMPOTENCY_CHECK`: Enable/disable idempotency checking (default: true) for external service pov
- `PAYMENT_TIMEOUT_MS`: Payment service timeout in milliseconds (default: 200)