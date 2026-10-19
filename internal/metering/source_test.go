package metering

import (
	"context"
	"testing"
)

func TestAgentLoopContext(t *testing.T) {
	if got := AgentLoopFromContext(WithAgentLoop(context.Background(), "advanced")); got != "advanced" {
		t.Fatalf("got %q", got)
	}
	if got := AgentLoopFromContext(WithAgentLoop(context.Background(), "")); got != "" {
		t.Fatalf("empty loop should leave the context alone, got %q", got)
	}
	//nolint:staticcheck // a nil context is exactly what this guards against
	if got := AgentLoopFromContext(nil); got != "" {
		t.Fatalf("nil context: %q", got)
	}
	//nolint:staticcheck
	if AgentLoopFromContext(WithAgentLoop(nil, "advanced")) != "advanced" {
		t.Fatal("WithAgentLoop should accept a nil parent")
	}
}
