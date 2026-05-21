package main

import (
	"fmt"
	"os"
)

// Placeholder entry point for the webhook delivery worker.
//
// F2 will implement the Postgres outbox consumer here:
//   - poll webhook_delivery_outbox for pending events,
//   - deliver via HTTP with exponential backoff + HMAC signing,
//   - update delivery status, route permanent failures to a DLQ table,
//   - expose Prometheus metrics and OTEL spans.
func main() {
	fmt.Fprintln(os.Stderr, "zapfast worker: not implemented yet (F2)")
	os.Exit(0)
}
