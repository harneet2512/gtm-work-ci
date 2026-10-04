package slacksurface

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Core API paths of the message refs (contracts/openapi/core.yaml). The surface segment is always RefSurface.
const (
	pathRef            = "/surface-messages/%s/" + RefSurface + "/%s"             // contract (GET)
	pathRefReservation = "/surface-messages/%s/" + RefSurface + "/%s/reservation" // contract (POST)
	pathRefTS          = "/surface-messages/%s/" + RefSurface + "/%s/ts"          // contract (PUT)
)

// CodeTSConflict is the 409 of a second, different ts for one message ref.
const CodeTSConflict = "ts_conflict"

// refJSON is contracts/schemas/surface_message.v1.json.
type refJSON struct {
	SubjectID  string    `json:"subject_id"`
	Kind       string    `json:"kind"`
	Channel    string    `json:"channel"`
	TS         *string   `json:"ts"`
	ReservedAt time.Time `json:"reserved_at"`
}

func (r refJSON) record() RefRecord {
	rec := RefRecord{SubjectID: r.SubjectID, Kind: r.Kind, Channel: r.Channel, ReservedAt: r.ReservedAt}
	if r.TS != nil {
		rec.TS = *r.TS
	}
	return rec
}

// GetRef implements RefStore: ErrNotFound when nothing was reserved.
func (c *CoreHTTP) GetRef(ctx context.Context, subjectID, kind string) (RefRecord, error) {
	var out refJSON
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf(pathRef, subjectID, kind), nil, &out); err != nil {
		return RefRecord{}, err
	}
	return out.record(), nil
}

// ReserveRef implements RefStore.
func (c *CoreHTTP) ReserveRef(ctx context.Context, subjectID, kind, channel string) (RefRecord, bool, error) {
	var out struct {
		Created bool    `json:"created"`
		Message refJSON `json:"message"`
	}
	body := map[string]string{"channel": channel}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf(pathRefReservation, subjectID, kind), body, &out); err != nil {
		return RefRecord{}, false, err
	}
	return out.Message.record(), out.Created, nil
}

// RecordRefTS implements RefStore: a different ts than the recorded one is a *RefTSConflictError.
func (c *CoreHTTP) RecordRefTS(ctx context.Context, subjectID, kind, ts string) (RefRecord, error) {
	var out refJSON
	err := c.do(ctx, http.MethodPut, fmt.Sprintf(pathRefTS, subjectID, kind), map[string]string{"ts": ts}, &out)
	var conflict *ConflictError
	if errors.As(err, &conflict) && conflict.Code == CodeTSConflict {
		existing, gerr := c.GetRef(ctx, subjectID, kind)
		if gerr != nil {
			return RefRecord{}, gerr
		}
		return RefRecord{}, &RefTSConflictError{Existing: existing}
	}
	if err != nil {
		return RefRecord{}, err
	}
	return out.record(), nil
}

var _ RefStore = (*CoreHTTP)(nil)
