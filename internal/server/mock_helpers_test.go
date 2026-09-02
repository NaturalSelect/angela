package server

import (
	"context"
	"errors"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/session"
	"go.uber.org/mock/gomock"
)

// coordinatorState is the mutable state a MockCoordinator built by
// newCoordinator reads and writes, mirroring the old hand-written
// stubCoordinator.
type coordinatorState struct {
	busy      map[string]bool
	branch    map[string]bool
	abandoned []string
}

// newCoordinator wires a MockCoordinator so IsSessionBusy, IsSessionBranch
// and AbandonBranch read and record against the returned state, and every
// other method returns a zero value, mirroring the old hand-written
// stubCoordinator.
func newCoordinator(t *testing.T) (*MockCoordinator, *coordinatorState) {
	t.Helper()
	st := &coordinatorState{busy: map[string]bool{}, branch: map[string]bool{}}
	m := NewMockCoordinator(gomock.NewController(t))
	m.EXPECT().IsSessionBusy(gomock.Any()).DoAndReturn(func(id string) bool { return st.busy[id] }).AnyTimes()
	m.EXPECT().IsSessionBranch(gomock.Any()).DoAndReturn(func(id string) bool { return st.branch[id] }).AnyTimes()
	m.EXPECT().AbandonBranch(gomock.Any()).DoAndReturn(func(id string) bool {
		st.abandoned = append(st.abandoned, id)
		return true
	}).AnyTimes()
	m.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	m.EXPECT().RunAccepted(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	m.EXPECT().BeginAccepted(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().Cancel(gomock.Any()).AnyTimes()
	m.EXPECT().CancelAll().AnyTimes()
	m.EXPECT().IsBusy().Return(false).AnyTimes()
	m.EXPECT().QueuedPrompts(gomock.Any()).Return(0).AnyTimes()
	m.EXPECT().QueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().ClearQueue(gomock.Any()).AnyTimes()
	m.EXPECT().Summarize(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().DefaultModel().Return(agent.Model{}).AnyTimes()
	m.EXPECT().EditActiveAgent(gomock.Any(), gomock.Any(), gomock.Any()).Return(config.ActiveAgent{}, nil).AnyTimes()
	m.EXPECT().ActiveAgent(gomock.Any(), gomock.Any()).Return(config.ActiveAgent{}, agent.Model{}, nil).AnyTimes()
	m.EXPECT().UpdateModels(gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().GenerateTitle(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.EXPECT().GenerateAgent(gomock.Any(), gomock.Any()).Return(config.Agent{}, "", nil).AnyTimes()
	m.EXPECT().SwitchAgent(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().SwitchVariant(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return m, st
}

// newSessions returns a MockSessionService whose List reports all, and
// whose Get looks all up by ID, mirroring the old hand-written
// stubSessions. Every other method is left unregistered: the old
// stubSessions embedded a nil session.Service for them, which would
// itself panic on any call, so nothing in the handlers under test
// reaches them.
func newSessions(t *testing.T, all ...session.Session) *MockSessionService {
	t.Helper()
	m := NewMockSessionService(gomock.NewController(t))
	m.EXPECT().List(gomock.Any()).Return(all, nil).AnyTimes()
	m.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id string) (session.Session, error) {
		for _, sess := range all {
			if sess.ID == id {
				return sess, nil
			}
		}
		return session.Session{}, errors.New("not found")
	}).AnyTimes()
	return m
}
