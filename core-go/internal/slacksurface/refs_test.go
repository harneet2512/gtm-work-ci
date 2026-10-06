package slacksurface

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// HAR-136: exactly-once channel messages. The doubles here are a Slack with a visible channel, message metadata,
// history and delete (slackSim), and core's create-only message refs (memRefs), each with fault injection at the
// points where a process can die. A "restart" is a new Publisher and Relay over the same refs and the same Slack:
// nothing the old process kept in memory survives.

type simMessage struct {
	channel, ts string
	msg         Message
	posted      time.Time
}

// slackSim is a Slack channel: every post is visible until deleted, and history finds messages by metadata.
type slackSim struct {
	mu       sync.Mutex
	msgs     []simMessage
	next     int
	postErr  map[string]error // by kind: fails the next post of that kind once
	updates  map[string]int   // ts -> chat.update calls
	updErr   error
	deletes  []string
	finds    int
	postedAt []string // kinds in post order
}

func newSlackSim() *slackSim {
	return &slackSim{postErr: map[string]error{}, updates: map[string]int{}}
}

func (s *slackSim) PostMessage(_ context.Context, channel string, m Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := ""
	if m.Meta != nil {
		kind = m.Meta.Kind
	}
	if err := s.postErr[kind]; err != nil {
		delete(s.postErr, kind)
		return "", err
	}
	s.next++
	ts := fmt.Sprintf("1700000000.%06d", s.next)
	s.msgs = append(s.msgs, simMessage{channel: channel, ts: ts, msg: m, posted: time.Now()})
	s.postedAt = append(s.postedAt, kind)
	return ts, nil
}

func (s *slackSim) UpdateMessage(_ context.Context, channel, ts string, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updErr != nil {
		return s.updErr
	}
	for i := range s.msgs {
		if s.msgs[i].channel == channel && s.msgs[i].ts == ts {
			s.msgs[i].msg = m
			s.updates[ts]++
			return nil
		}
	}
	return errors.New("message_not_found")
}

func (s *slackSim) FindMessage(_ context.Context, channel string, since time.Time, meta MessageMeta) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finds++
	for _, m := range s.msgs { // oldest first: the first post is the one to adopt
		if m.channel == channel && !m.posted.Before(since) && m.msg.Meta != nil && *m.msg.Meta == meta {
			return m.ts, true, nil
		}
	}
	return "", false, nil
}

func (s *slackSim) DeleteMessage(_ context.Context, channel, ts string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.msgs[:0]
	for _, m := range s.msgs {
		if m.channel != channel || m.ts != ts {
			kept = append(kept, m)
		}
	}
	s.msgs = kept
	s.deletes = append(s.deletes, ts)
	return nil
}

func (s *slackSim) OpenView(context.Context, string, slack.ModalViewRequest) (string, error) {
	return "V1", nil
}
func (s *slackSim) UpdateView(context.Context, string, slack.ModalViewRequest) error { return nil }
func (s *slackSim) PostEphemeral(context.Context, string, string, string) error      { return nil }

// visible counts the messages of one kind (or all with "") that a person in the channel can see.
func (s *slackSim) visible(kind string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.msgs {
		if kind == "" || (m.msg.Meta != nil && m.msg.Meta.Kind == kind) {
			n++
		}
	}
	return n
}

func (s *slackSim) order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.postedAt...)
}

// memRefs is core's surface_messages with the contract's rules and a failure injected at each write.
type memRefs struct {
	mu        sync.Mutex
	rows      map[string]RefRecord
	reserveAt time.Time
	recordErr error
	errKind   string // when set, recordErr applies to this kind only
	reserveEr error
	afterPost func(subject, kind string) // runs inside RecordRefTS before it writes: another process got there first
	reserves  int
}

func newMemRefs() *memRefs { return &memRefs{rows: map[string]RefRecord{}} }

func key(subject, kind string) string { return subject + "/" + kind }

func (m *memRefs) GetRef(_ context.Context, subject, kind string) (RefRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[key(subject, kind)]
	if !ok {
		return RefRecord{}, ErrNotFound
	}
	return r, nil
}

func (m *memRefs) ReserveRef(_ context.Context, subject, kind, channel string) (RefRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reserveEr != nil {
		return RefRecord{}, false, m.reserveEr
	}
	m.reserves++
	if r, ok := m.rows[key(subject, kind)]; ok {
		return r, false, nil
	}
	at := m.reserveAt
	if at.IsZero() {
		at = time.Now()
	}
	r := RefRecord{SubjectID: subject, Kind: kind, Channel: channel, ReservedAt: at}
	m.rows[key(subject, kind)] = r
	return r, true, nil
}

func (m *memRefs) RecordRefTS(_ context.Context, subject, kind, ts string) (RefRecord, error) {
	m.mu.Lock()
	hook := m.afterPost
	m.mu.Unlock()
	if hook != nil {
		hook(subject, kind)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recordErr != nil && (m.errKind == "" || m.errKind == kind) {
		err := m.recordErr
		m.recordErr = nil
		return RefRecord{}, err
	}
	r, ok := m.rows[key(subject, kind)]
	if !ok {
		return RefRecord{}, ErrNotFound
	}
	if r.TS != "" && r.TS != ts {
		return RefRecord{}, &RefTSConflictError{Existing: r}
	}
	r.TS = ts
	m.rows[key(subject, kind)] = r
	return r, nil
}

func (m *memRefs) set(subject, kind string, r RefRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[key(subject, kind)] = r
}

func (m *memRefs) row(subject, kind string) (RefRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[key(subject, kind)]
	return r, ok
}

// refCore is the in-memory core with message refs, so a Publisher over it is exactly-once.
type refCore struct {
	*memCore
	*memRefs
}

// process is one run of the slackbot: a fresh Publisher and Relay with no memory of an earlier run.
type process struct {
	relay *Relay
	pub   *Publisher
}

func startProcess(t *testing.T, core Core, poster Poster, src EventSource) process {
	t.Helper()
	pub := NewPublisher(core, poster, "C0TEST", "")
	r, err := NewRelay(src, pub, quietLog(), 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	r.now = func() time.Time { clock = clock.Add(time.Hour); return clock } // never inside a backoff: every drain tries every event
	return process{relay: r, pub: pub}
}

func newRefCore() (*refCore, *memCore) {
	mc := newMemCore()
	return &refCore{memCore: mc, memRefs: newMemRefs()}, mc
}

func episodeOf(c *memCore) string { return c.fx.Strategies.StrategySet.DecisionEpisodeID }
func biOf(c *memCore) string      { return c.fx.BI.ID }

func setEvent(id int64, c *memCore, biUpdate string) OutboxEvent {
	return OutboxEvent{ID: id, Topic: TopicStrategySetPublished, AgentRunID: "run-1", StrategySetID: c.fx.Strategies.StrategySet.ID,
		DecisionEpisodeID: episodeOf(c), AccountID: c.fx.BI.AccountID, BIUpdateID: biUpdate}
}

func biEvent(id int64, c *memCore) OutboxEvent {
	return OutboxEvent{ID: id, Topic: TopicBIUpdatePublished, AccountID: c.fx.BI.AccountID, BIUpdateID: biOf(c)}
}

func TestACrashBetweenThePostAndTheAckNeverMakesASecondVisibleMessage(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	src := &memEvents{pending: []OutboxEvent{setEvent(7, mc, "")}}

	src.ackErr = errors.New("process died before the acknowledgement")
	first := startProcess(t, core, sim, src)
	if n, _ := first.relay.Drain(context.Background()); n != 0 || sim.visible("chooser") != 1 {
		t.Fatalf("first run: acknowledged %d, visible %d", n, sim.visible("chooser"))
	}
	src.ackErr = nil

	restarted := startProcess(t, core, sim, src) // nothing in memory survives
	if n, _ := restarted.relay.Drain(context.Background()); n != 1 {
		t.Fatalf("restart: acknowledged %d, want the redelivered event acknowledged", n)
	}
	if got := sim.visible("chooser"); got != 1 {
		t.Fatalf("%d visible Message 2 after a crash between post and ack, want exactly 1", got)
	}
	ref, ok := core.row(episodeOf(mc), KindChooser)
	if !ok || ref.TS == "" || sim.updates[ref.TS] != 1 {
		t.Fatalf("ref %+v (found %v), updates %v: the repeated delivery must find the ts and chat.update, not post", ref, ok, sim.updates)
	}
	if got := src.ackedIDs(); len(got) != 1 || got[0] != 7 {
		t.Fatalf("acked = %v", got)
	}
}

func TestACrashBetweenThePostAndRecordingItsTsAdoptsTheMessageFromHistory(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	src := &memEvents{pending: []OutboxEvent{setEvent(8, mc, "")}}

	core.recordErr = errors.New("process died before the ts was stored")
	first := startProcess(t, core, sim, src)
	if n, _ := first.relay.Drain(context.Background()); n != 0 || sim.visible("chooser") != 1 {
		t.Fatalf("first run: acknowledged %d, visible %d", n, sim.visible("chooser"))
	}
	if ref, _ := core.row(episodeOf(mc), KindChooser); ref.TS != "" {
		t.Fatalf("the ts was stored despite the crash: %+v", ref)
	}

	restarted := startProcess(t, core, sim, src)
	if n, _ := restarted.relay.Drain(context.Background()); n != 1 {
		t.Fatalf("restart acknowledged %d", n)
	}
	posted := sim.msgs[0]
	if got := sim.visible("chooser"); got != 1 {
		t.Fatalf("%d visible Message 2 after a crash before the ts was stored, want 1", got)
	}
	if ref, _ := core.row(episodeOf(mc), KindChooser); ref.TS != posted.ts {
		t.Fatalf("the adopted ts = %q, want the posted message %q", ref.TS, posted.ts)
	}
	if sim.finds == 0 {
		t.Fatal("the reservation without a ts was not reconciled against the channel history")
	}
}

func TestACrashAfterTheReservationBeforeThePostPostsOnceOnTheNextTry(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	src := &memEvents{pending: []OutboxEvent{setEvent(9, mc, "")}}

	sim.postErr[KindChooser] = errors.New("slack unreachable")
	first := startProcess(t, core, sim, src)
	if n, _ := first.relay.Drain(context.Background()); n != 0 || sim.visible("") != 0 {
		t.Fatalf("first run: acknowledged %d, visible %d", n, sim.visible(""))
	}
	if ref, ok := core.row(episodeOf(mc), KindChooser); !ok || ref.TS != "" {
		t.Fatalf("expected a reservation without a ts, got %+v (found %v)", ref, ok)
	}

	restarted := startProcess(t, core, sim, src)
	if n, _ := restarted.relay.Drain(context.Background()); n != 1 || sim.visible("chooser") != 1 {
		t.Fatalf("restart: acknowledged %d, visible %d", n, sim.visible("chooser"))
	}
	if ref, _ := core.row(episodeOf(mc), KindChooser); ref.TS == "" {
		t.Fatal("the ts of the message posted after the reservation was not stored")
	}
}

func TestAMessageAnotherProcessPostedFirstIsKeptAndOurDuplicateIsRemoved(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	src := &memEvents{pending: []OutboxEvent{setEvent(10, mc, "")}}

	// Another process reserved, posted and stored its ts between our reservation check and our ts write.
	core.afterPost = func(subject, kind string) {
		core.afterPost = nil
		sim.mu.Lock()
		sim.next++
		ts := fmt.Sprintf("1700000000.%06d", 900+sim.next)
		sim.msgs = append(sim.msgs, simMessage{channel: "C0TEST", ts: ts, msg: Message{Meta: &MessageMeta{SubjectID: subject, Kind: kind}}, posted: time.Now()})
		sim.mu.Unlock()
		ref, _ := core.row(subject, kind)
		ref.TS = ts
		core.set(subject, kind, ref)
	}
	p := startProcess(t, core, sim, src)
	if n, _ := p.relay.Drain(context.Background()); n != 1 {
		t.Fatalf("acknowledged %d", n)
	}
	if got := sim.visible("chooser"); got != 1 {
		t.Fatalf("%d visible Message 2, want 1: the duplicate must be deleted", got)
	}
	if len(sim.deletes) != 1 {
		t.Fatalf("deletes = %v, want our own duplicate removed", sim.deletes)
	}
}

func TestNoMoreThanThreeMessagesAreEverVisibleForAnEpisodeWhereverTheProcessDies(t *testing.T) {
	// For each message (M1 update, M2 chooser, M3 judgment) and each way a process can die around its post (the
	// post fails, the ts is never stored, the acknowledgement is lost), the first run dies there and two clean
	// restarts follow. However it is interleaved, a person sees at most three messages at any moment and, at the
	// end, exactly one of each kind.
	kinds := []string{KindBI, KindChooser, KindJudgment}
	crashes := map[string]func(core *refCore, sim *slackSim, src *memEvents, kind string){
		"the post fails": func(_ *refCore, sim *slackSim, _ *memEvents, kind string) { sim.postErr[kind] = errors.New("died") },
		"the ts is never stored": func(core *refCore, _ *slackSim, _ *memEvents, kind string) {
			core.recordErr, core.errKind = errors.New("died"), kind
		},
		"the acknowledgement is lost": func(_ *refCore, _ *slackSim, src *memEvents, _ string) { src.ackErr = errors.New("died") },
	}
	for _, kind := range kinds {
		for name, crash := range crashes {
			t.Run(kind+" when "+name, func(t *testing.T) {
				core, mc := newRefCore()
				sim := newSlackSim()
				src := &memEvents{pending: []OutboxEvent{biEvent(1, mc), setEvent(2, mc, biOf(mc))}}
				ctx := context.Background()
				if _, err := mc.RecordStrategyDecision(ctx, "run-1", StrategyDecisionRequest{SelectedCandidateID: fixtureCandA, Surface: "slack", ActorLabel: "x"}); err != nil {
					t.Fatal(err)
				}
				if _, err := mc.SendRun(ctx, "run-1", SendRequest{Decision: SendSend, Surface: "slack", ActorLabel: "x"}); err != nil {
					t.Fatal(err) // the judgment (M3) exists only once the human has sent
				}
				for run := 0; run < 3; run++ {
					if run == 0 {
						crash(core, sim, src, kind)
					}
					p := startProcess(t, core, sim, src)
					_, _ = p.relay.Drain(ctx)
					_, _ = p.pub.PostJudgment(ctx, "run-1", episodeOf(mc))
					src.ackErr = nil
					if got := sim.visible(""); got > 3 {
						t.Fatalf("run %d: %d visible messages, want at most 3 (post order %v)", run, got, sim.order())
					}
				}
				for _, k := range kinds {
					if got := sim.visible(k); got != 1 {
						t.Errorf("%d visible %s messages, want exactly 1 (post order %v)", got, k, sim.order())
					}
				}
				if len(src.pending) != 0 {
					t.Errorf("events left unacknowledged: %+v", src.pending)
				}
				if len(sim.deletes) != 0 {
					t.Errorf("one process at a time needs no deletes, got %v", sim.deletes)
				}
			})
		}
	}
}

func TestMessage2IsNeverPostedBeforeItsEpisodesMessage1(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	// The set event is listed first and M1's own event is not even visible yet; M2 must still come second.
	src := &memEvents{pending: []OutboxEvent{setEvent(5, mc, biOf(mc))}}
	p := startProcess(t, core, sim, src)
	if n, _ := p.relay.Drain(context.Background()); n != 1 {
		t.Fatalf("acknowledged %d", n)
	}
	if got := sim.order(); len(got) != 2 || got[0] != KindBI || got[1] != KindChooser {
		t.Fatalf("post order = %v, want [bi chooser]", got)
	}
	// M1's own event arrives afterwards: its message exists, so it updates it and posts nothing
	src.pending = append(src.pending, biEvent(6, mc))
	if n, _ := p.relay.Drain(context.Background()); n != 1 || sim.visible(KindBI) != 1 {
		t.Fatalf("bi event: acknowledged %d, visible bi %d", n, sim.visible(KindBI))
	}
}

func TestMessage2WaitsWhileMessage1CannotBePosted(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	src := &memEvents{pending: []OutboxEvent{setEvent(5, mc, biOf(mc))}}
	sim.postErr[KindBI] = errors.New("slack unreachable")

	p := startProcess(t, core, sim, src)
	if n, _ := p.relay.Drain(context.Background()); n != 0 || sim.visible("") != 0 || len(src.ackedIDs()) != 0 {
		t.Fatalf("with M1 failing: acknowledged %d, visible %d: M2 must wait", n, sim.visible(""))
	}
	if _, ok := core.row(episodeOf(mc), KindChooser); ok {
		t.Fatal("M2's slot was reserved while M1 was missing")
	}
	if n, _ := p.relay.Drain(context.Background()); n != 1 {
		t.Fatalf("once M1 posts: acknowledged %d", n)
	}
	if got := sim.order(); len(got) != 2 || got[0] != KindBI || got[1] != KindChooser {
		t.Fatalf("post order = %v, want [bi chooser]", got)
	}
}

func TestAnEpisodeWithoutAnUpdateAndASupersededUpdateDoNotHoldMessage2Back(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	other := "ffffffff-0000-4000-8000-00000000ff01" // an older update: the account's latest is the fixture's
	src := &memEvents{pending: []OutboxEvent{setEvent(1, mc, other), biEvent(2, mc)}}
	src.pending[1].BIUpdateID = other // M1's event for the superseded update
	p := startProcess(t, core, sim, src)
	if n, _ := p.relay.Drain(context.Background()); n != 2 {
		t.Fatalf("acknowledged %d, want both", n)
	}
	if got := sim.order(); len(got) != 1 || got[0] != KindChooser {
		t.Fatalf("post order = %v: a superseded update has no message to wait for and none is posted for it", got)
	}
}

func TestARepeatedDeliveryNeverRevertsAChooserTheHumanAlreadyActedOn(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	src := &memEvents{pending: []OutboxEvent{setEvent(3, mc, "")}}
	src.ackErr = errors.New("died")
	p := startProcess(t, core, sim, src)
	_, _ = p.relay.Drain(context.Background())
	src.ackErr = nil

	// The human chose a candidate; the message now shows the selection and is the handler's to update.
	if _, err := mc.RecordStrategyDecision(context.Background(), "run-1", StrategyDecisionRequest{SelectedCandidateID: fixtureCandA, Surface: "slack", ActorLabel: "x"}); err != nil {
		t.Fatal(err)
	}
	restarted := startProcess(t, core, sim, src)
	if n, _ := restarted.relay.Drain(context.Background()); n != 1 {
		t.Fatalf("acknowledged %d", n)
	}
	ref, _ := core.row(episodeOf(mc), KindChooser)
	if sim.updates[ref.TS] != 0 {
		t.Fatalf("the redelivery re-rendered the chooser over the human's selection (%d updates)", sim.updates[ref.TS])
	}
}

func TestAFailedRefreshOfAnExistingMessageDoesNotBlockTheAcknowledgement(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	src := &memEvents{pending: []OutboxEvent{setEvent(4, mc, "")}}
	src.ackErr = errors.New("died")
	_, _ = startProcess(t, core, sim, src).relay.Drain(context.Background())
	src.ackErr = nil
	sim.updErr = errors.New("message_not_found")
	if n, _ := startProcess(t, core, sim, src).relay.Drain(context.Background()); n != 1 || sim.visible("chooser") != 1 {
		t.Fatalf("acknowledged %d, visible %d: the message exists, so a failed refresh must not repost or hold the event", n, sim.visible("chooser"))
	}
}

func TestACoreWithoutRefsKeepsTheInProcessGuard(t *testing.T) {
	// A Core that is not a RefStore (the fixture core of --dry-run) still posts once per process.
	mc := newMemCore()
	sim := newSlackSim()
	p := NewPublisher(mc, sim, "C0TEST", "")
	for i := 0; i < 3; i++ {
		if _, err := p.PostChooser(context.Background(), "run-1"); err != nil {
			t.Fatal(err)
		}
	}
	if sim.visible("chooser") != 1 {
		t.Fatalf("%d messages", sim.visible("chooser"))
	}
}

func TestReservingFailsClosedWhenCoreIsUnreachable(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	core.reserveEr = errors.New("core down")
	src := &memEvents{pending: []OutboxEvent{setEvent(2, mc, "")}}
	if n, _ := startProcess(t, core, sim, src).relay.Drain(context.Background()); n != 0 || sim.visible("") != 0 {
		t.Fatalf("with refs unreachable: acknowledged %d, visible %d: nothing may be posted without a reservation", n, sim.visible(""))
	}
}
