package knowledgestore

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

func TestDatabaseErrorsAreReturned(t *testing.T) {
	tx, err := env.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback() // every statement now fails with sql.ErrTxDone
	r := rules(t)
	ev := knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: uuid(41), At: t0}
	checks := map[string]error{}
	_, checks["insert"] = Insert(ctx, tx, neutral(uuid(40)), "x")
	_, checks["get"] = Get(ctx, tx, uuid(40))
	_, checks["list"] = List(ctx, tx, "", 0)
	_, checks["listApplicable"] = ListApplicable(ctx, tx, r)
	_, _, checks["record"] = RecordEvidence(ctx, tx, uuid(40), ev, r)
	_, _, checks["revalidate"] = Revalidate(ctx, tx, uuid(40), t0, r)
	for name, err := range checks {
		if !errors.Is(err, sql.ErrTxDone) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestInsertRefusesUnearnedStatusAndCounts(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		validated := t0
		cases := map[string]func(*knowledge.Knowledge){
			"status":   func(k *knowledge.Knowledge) { k.Status = knowledge.StatusConfirmed },
			"counts":   func(k *knowledge.Knowledge) { k.Counts.Decisions = 8 },
			"episodes": func(k *knowledge.Knowledge) { k.SupportingDecisionEpisodeIDs = []string{uuid(61)} },
			"counterexamples": func(k *knowledge.Knowledge) {
				k.Counterexamples = []knowledge.Counterexample{{DecisionEpisodeID: uuid(62), Note: "x"}}
			},
			"history":   func(k *knowledge.Knowledge) { k.StatusHistory = []knowledge.HistoryEntry{{ToStatus: "candidate"}} },
			"validated": func(k *knowledge.Knowledge) { k.LastValidatedAt = &validated },
		}
		for name, mutate := range cases {
			k := neutral(uuid(60))
			mutate(&k)
			if _, err := Insert(ctx, tx, k, "x"); !errors.Is(err, ErrUnearnedEvidence) {
				t.Fatalf("%s: err = %v", name, err)
			}
		}
	})
}

func TestCorruptColumnsAreDecodeErrors(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		id := insert(t, tx, neutral(uuid(50))).ID
		_, err := tx.Exec(`UPDATE knowledge SET counts = '{"decisions":"many","positive_reactions":0,
			"negative_reactions":0,"outcomes_advanced":0,"counterexamples":0}' WHERE id = $1`, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Get(ctx, tx, id); err == nil {
			t.Fatal("a corrupt counts column must be an error")
		}
		if _, _, err := RecordEvidence(ctx, tx, id, knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: uuid(51), At: t0}, rules(t)); err == nil {
			t.Fatal("recording on a corrupt row must fail")
		}
	})
}
