package demorun

import "context"

// Call is a generic request to core's HTTP API (the same authentication and error envelope as the typed methods): the
// one-time record run drives the human steps (choose, edit, send, verdict) through it, exactly the endpoints the Slack
// bot and the web call. out may be nil.
func (c CoreClient) Call(ctx context.Context, method, path string, body, out any) (int, error) {
	return c.do(ctx, method, path, body, out)
}
