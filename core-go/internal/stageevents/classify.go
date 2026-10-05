package stageevents

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"os"
)

// kindError carries a failure kind that the error's own type cannot say (a worker answering 503 is a transport
// problem; an unusable model output is a contract problem).
type kindError struct {
	kind FailureKind
	err  error
}

func (e *kindError) Error() string { return e.err.Error() }
func (e *kindError) Unwrap() error { return e.err }

// MarkTransport tags err as a transport failure: the outcome is unknown and a retry may succeed.
func MarkTransport(err error) error { return mark(err, Transport) }

// MarkContract tags err as a contract failure: an input or output broke a contract.
func MarkContract(err error) error { return mark(err, Contract) }

func mark(err error, kind FailureKind) error {
	if err == nil {
		return nil
	}
	return &kindError{kind: kind, err: err}
}

// ClassifyError says why a stage did not complete. An explicit mark wins; otherwise timeouts, cancelled calls,
// network and connection errors are transport; everything else is internal. A transport error is never an eval
// verdict (see the package comment).
func ClassifyError(err error) FailureKind {
	var tagged *kindError
	var netErr net.Error
	switch {
	case err == nil:
		return Internal
	case errors.As(err, &tagged):
		return tagged.kind
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), errors.Is(err, os.ErrDeadlineExceeded):
		return Transport
	case errors.As(err, &netErr), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, driver.ErrBadConn):
		return Transport
	default:
		return Internal
	}
}
