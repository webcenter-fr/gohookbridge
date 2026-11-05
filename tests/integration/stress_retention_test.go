//go:build integration

package integration_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/webcenter-fr/gohookbridge/pkg/nats"
	"gotest.tools/v3/assert"
)

// F11: under a deterministic flood (well beyond the byte budget) the ring
// buffer never retains more than the configured number of payload bytes.
func TestRetainedBytesUnderBudget(t *testing.T) {
	const budget = 1 << 20 // 1 MiB
	const payloadSize = 4096
	const appends = 2000

	rb := nats.NewRingBuffer(4096, budget, time.Hour)
	payload := bytes.Repeat([]byte("x"), payloadSize)

	for i := 0; i < appends; i++ {
		rb.Append("flood", payload)
	}

	assert.Assert(t, rb.RetainedBytes() <= budget,
		"retained %d bytes exceeds the %d-byte budget", rb.RetainedBytes(), budget)
}
