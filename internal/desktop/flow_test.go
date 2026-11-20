package desktop

import (
	"path/filepath"
	"testing"
	"time"

	"sshtool/internal/core"
)

func flowService(t *testing.T) (*Service, Hooks, <-chan core.Event) {
	t.Helper()
	directory := t.TempDir()
	events := make(chan core.Event, 32)
	s, hooks, err := New(directory, "", filepath.Join(directory, "cache"), UI{}, func(event core.Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hooks.Close)
	return s, hooks, events
}

func startTerminalFrame(s *Service, session string) <-chan struct{} {
	return s.sendFrame(core.Event{"type": "terminal", "sessionId": session, "data": "5Lit5paH"})
}

func receiveFrame(t *testing.T, events <-chan core.Event) core.Event {
	t.Helper()
	select {
	case event := <-events:
		if event["type"] != "terminal" {
			t.Fatalf("expected terminal event, got %#v", event)
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("terminal frame did not reach renderer")
		return nil
	}
}

func assertFinished(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal output goroutine did not exit")
	}
}

func assertPending(t *testing.T, s *Service, event core.Event, done <-chan struct{}) {
	t.Helper()
	token, ok := event["sequence"].(string)
	if !ok {
		t.Fatalf("missing opaque frame sequence: %#v", event)
	}
	s.mu.Lock()
	_, exists := s.frames[token]
	s.mu.Unlock()
	if !exists {
		t.Fatal("frame was released before a matching acknowledgement")
	}
	select {
	case <-done:
		t.Fatal("output reader returned before acknowledgement")
	default:
	}
}

func ack(t *testing.T, s *Service, event core.Event) {
	t.Helper()
	result := s.Call("terminal:ack", callArguments(t, event["sessionId"], event["sequence"]))
	if !result.OK {
		t.Fatal(result.Error)
	}
}

func TestTerminalFlowMatchesSessionAndOpaqueSequence(t *testing.T) {
	s, _, events := flowService(t)
	done := startTerminalFrame(s, "session-a")
	frame := receiveFrame(t, events)
	token, ok := frame["sequence"].(string)
	if !ok || core.ValidateID(token) != nil {
		t.Fatalf("invalid frame sequence %#v", frame)
	}
	assertPending(t, s, frame, done)
	for _, args := range [][]any{{"session-b", token}, {"session-a", core.NewID()}} {
		result := s.Call("terminal:ack", callArguments(t, args...))
		if !result.OK {
			t.Fatal(result.Error)
		}
		assertPending(t, s, frame, done)
	}
	ack(t, s, frame)
	assertFinished(t, done)
	s.mu.Lock()
	remaining := len(s.frames)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("acknowledged frame retained")
	}
	// Repeated acknowledgements cannot close the same channel twice.
	ack(t, s, frame)
}

func TestTerminalFlowOldAcknowledgementCannotReleaseNewFrame(t *testing.T) {
	s, _, events := flowService(t)
	firstDone := startTerminalFrame(s, "session-a")
	first := receiveFrame(t, events)
	ack(t, s, first)
	assertFinished(t, firstDone)
	secondDone := startTerminalFrame(s, "session-a")
	second := receiveFrame(t, events)
	if first["sequence"] == second["sequence"] {
		t.Fatal("frame sequence reused")
	}
	ack(t, s, first)
	assertPending(t, s, second, secondDone)
	ack(t, s, second)
	assertFinished(t, secondDone)
}

func TestTerminalFlowDisconnectReleasesOnlyItsSession(t *testing.T) {
	s, _, events := flowService(t)
	aDone := startTerminalFrame(s, "session-a")
	a := receiveFrame(t, events)
	bDone := startTerminalFrame(s, "session-b")
	b := receiveFrame(t, events)
	s.emit(core.Event{"type": "state", "sessionId": "session-b", "phase": "connected", "message": "connected"})
	assertPending(t, s, b, bDone)
	s.emit(core.Event{"type": "state", "sessionId": "session-a", "phase": "disconnected", "message": "cancelled"})
	assertFinished(t, aDone)
	assertPending(t, s, b, bDone)
	// A late ACK for the disconnected session does not affect another reader.
	ack(t, s, a)
	assertPending(t, s, b, bDone)
	ack(t, s, b)
	assertFinished(t, bDone)
}

func TestTerminalFlowCloseReleasesAllReadersAndRejectsNewFrames(t *testing.T) {
	s, hooks, events := flowService(t)
	oneDone := startTerminalFrame(s, "session-a")
	one := receiveFrame(t, events)
	twoDone := startTerminalFrame(s, "session-b")
	two := receiveFrame(t, events)
	assertPending(t, s, one, oneDone)
	assertPending(t, s, two, twoDone)
	closed := make(chan struct{})
	go func() { hooks.Close(); close(closed) }()
	assertFinished(t, closed)
	assertFinished(t, oneDone)
	assertFinished(t, twoDone)
	s.mu.Lock()
	remaining := len(s.frames)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("shutdown retained pending frames")
	}
	if startTerminalFrame(s, "session-after-close") != nil {
		t.Fatal("shutdown accepted a new frame")
	}
	select {
	case event := <-events:
		t.Fatalf("event emitted after close: %#v", event)
	default:
	}
}

func TestDirtyStateCombinesUnsavedEditsAndTypedTransferEvents(t *testing.T) {
	s, hooks, _ := testService(t)
	if hooks.Dirty() {
		t.Fatal("new workspace is dirty")
	}
	setDirty := func(value bool) {
		t.Helper()
		result := s.Call("window:dirty", callArguments(t, value))
		if !result.OK {
			t.Fatal(result.Error)
		}
	}
	setDirty(true)
	if !hooks.Dirty() {
		t.Fatal("unsaved state not tracked")
	}
	setDirty(false)
	if hooks.Dirty() {
		t.Fatal("saved state not cleared")
	}
	transfer := func(id, status string) {
		s.emit(core.Event{"type": "transfer", "transfer": core.Transfer{ID: id, SessionID: "session-a", Status: status}})
	}
	transfer("one", "queued")
	if !hooks.Dirty() {
		t.Fatal("queued transfer not tracked")
	}
	transfer("one", "running")
	s.mu.Lock()
	busyCount := len(s.busy)
	s.mu.Unlock()
	if busyCount != 1 {
		t.Fatal("one transfer counted more than once")
	}
	transfer("two", "queued")
	transfer("one", "completed")
	if !hooks.Dirty() {
		t.Fatal("other queued transfer lost")
	}
	transfer("two", "failed")
	if hooks.Dirty() {
		t.Fatal("failed or completed transfer still marks workspace busy")
	}
	transfer("three", "running")
	setDirty(true)
	transfer("three", "cancelled")
	if !hooks.Dirty() {
		t.Fatal("transfer cancellation cleared unsaved edits")
	}
	setDirty(false)
	if hooks.Dirty() {
		t.Fatal("clean terminal state retained")
	}
}
