package workerclient

import (
	"context"
	"net/http"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ask"
)

// AskTimeout bounds POST /v1/ask: the worker's own deadline is 60 s, so it answers (or gives its plain "ran out of
// time" answer) before core gives up.
const AskTimeout = 70 * time.Second

// Ask calls POST /v1/ask (contracts/openapi/worker.yaml). The worker runs the tool loop; core serves its tools.
func (c *Client) Ask(ctx context.Context, req ask.WorkerRequest) (ask.WorkerAnswer, error) {
	var out ask.WorkerAnswer
	if err := c.call(ctx, "/v1/ask", AskTimeout, req, &out); err != nil {
		return ask.WorkerAnswer{}, err
	}
	if out.Model == "" || out.AnswerMarkdown == "" {
		return ask.WorkerAnswer{}, &Error{Status: http.StatusOK, Code: "bad_response", Message: "worker returned no answer or model"}
	}
	return out, nil
}

var _ ask.Worker = (*Client)(nil)
