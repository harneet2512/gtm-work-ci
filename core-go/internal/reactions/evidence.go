package reactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
)

// applyDecisionEvidence attaches the episode itself to the knowledge its decision used: the send is
// the 'decision_episode' evidence the lifecycle counts toward support (and the 'human_decision' link
// names the recorded approve/edit). Both are idempotent — a second scan skips what exists.
func (s *Service) applyDecisionEvidence(ctx context.Context, tx *sql.Tx, ep *episode, res *ScanResult) error {
	for _, kID := range ep.knowledgeIDs {
		k, err := knowledgestore.Get(ctx, tx, kID)
		if err != nil {
			return fmt.Errorf("reactions: load knowledge %s: %w", kID, err)
		}
		supported := false
		for _, id := range k.SupportingDecisionEpisodeIDs {
			if id == ep.ID {
				supported = true
				break
			}
		}
		if !supported {
			err := s.record(ctx, tx, kID, knowledge.Evidence{
				Kind: knowledge.EvidenceDecisionEpisode, RefID: ep.ID, At: ep.SendAt,
			}, res)
			if err != nil {
				return err
			}
		}
		err = s.record(ctx, tx, kID, knowledge.Evidence{
			Kind: knowledge.EvidenceHumanDecision, RefID: ep.DecisionID, At: ep.SendAt,
		}, res)
		if err != nil {
			return err
		}
	}
	return nil
}

// applyRowEvidence attaches one new reaction/outcome row to every knowledge the decision used.
// Positive reactions and advanced outcomes strengthen it, the rest narrow or dispute — the
// lifecycle rules in s.rules decide what that means.
func (s *Service) applyRowEvidence(ctx context.Context, tx *sql.Tx, ep *episode, res *ScanResult, ev knowledge.Evidence) error {
	for _, kID := range ep.knowledgeIDs {
		if err := s.record(ctx, tx, kID, ev, res); err != nil {
			return err
		}
	}
	return nil
}

// record is the one place knowledgestore.RecordEvidence is called from: a duplicate is a skip, not
// an error — the evidence kinds that can repeat across episodes are deduped by knowledge_evidence_once.
func (s *Service) record(ctx context.Context, tx *sql.Tx, kID string, ev knowledge.Evidence, res *ScanResult) error {
	_, _, err := knowledgestore.RecordEvidence(ctx, tx, kID, ev, s.rules)
	if errors.Is(err, knowledgestore.ErrDuplicateEvidence) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reactions: evidence %s %s on %s: %w", ev.Kind, ev.RefID, kID, err)
	}
	res.Evidence++
	return nil
}
