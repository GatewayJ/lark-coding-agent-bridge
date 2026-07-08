package bridge

import "context"

type ProcessPoolStatus struct {
	Active  int `json:"active"`
	Waiting int `json:"waiting"`
	Cap     int `json:"cap"`
}

type Status struct {
	AgentName    string            `json:"agentName"`
	ActiveScopes []string          `json:"activeScopes"`
	Pool         ProcessPoolStatus `json:"pool"`
	Bridge       BridgeStatus      `json:"bridge,omitempty"`
	Runtime      *RuntimeStatus    `json:"runtime,omitempty"`
	Lark         LarkStatus        `json:"lark,omitempty"`
}

func (c *Client) Status() Status {
	if c == nil {
		return Status{}
	}
	pool := c.executor.PoolSnapshot()
	return Status{
		AgentName:    c.agent.DisplayName(),
		ActiveScopes: c.executor.ActiveScopes(),
		Pool: ProcessPoolStatus{
			Active:  pool.Active,
			Waiting: pool.Waiting,
			Cap:     pool.Cap,
		},
	}
}

func (c *Client) StopScope(ctx context.Context, scopeID string) bool {
	if c == nil || scopeID == "" {
		return false
	}
	return c.executor.Interrupt(ctx, scopeID)
}

func (c *Client) StopAll(ctx context.Context) error {
	if c == nil {
		return ErrNilClient
	}
	return c.executor.StopAll(ctx)
}

func (c *Client) WaitForAll(ctx context.Context) error {
	if c == nil {
		return ErrNilClient
	}
	return c.executor.WaitForAll(ctx)
}
