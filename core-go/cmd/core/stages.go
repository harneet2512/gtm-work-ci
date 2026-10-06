package main

import (
	"database/sql"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
)

// newStageRecorder builds the recorder of the live pipeline progress (HAR-145): Play, the orchestrator and the
// Slack message refs each write the stages they execute through one. It is stateless, so each wiring site gets
// its own.
func newStageRecorder(db *sql.DB) (*stageevents.Recorder, error) {
	rec, err := stageevents.NewRecorder(db, nil)
	if err != nil {
		return nil, fmt.Errorf("core: stage recorder: %w", err)
	}
	return rec, nil
}
