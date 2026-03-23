package service

import "context"

// AgentRunner is the function signature for the main agent logic.
// It receives a context that is cancelled when the service is stopping.
type AgentRunner func(ctx context.Context) error
