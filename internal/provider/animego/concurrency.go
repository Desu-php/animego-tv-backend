package animego

// WithRequestConcurrency bounds actual upstream requests, including detached
// cache loads. Excess requests fail immediately rather than waiting in a queue.
func WithRequestConcurrency(limit int) ClientOption {
	return func(c *Client) { c.requestConcurrency = limit }
}
