package desktop

import "sshtool/internal/core"

type terminalFrame struct {
	session string
	done    chan struct{}
}

// Publish each frame in session order. The SSH reader waits for xterm to parse
// it outside the event gate, bounding the queue without blocking other sessions.
func (s *Service) sendFrame(event core.Event) <-chan struct{} {
	id, _ := event["sessionId"].(string)
	token := core.NewID()
	frame := terminalFrame{id, make(chan struct{})}
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return nil
	}
	s.frames[token] = frame
	s.mu.Unlock()
	event["sequence"] = token
	s.emit(event)
	return frame.done
}
func (s *Service) ackFrame(id, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if frame, ok := s.frames[token]; ok && frame.session == id {
		delete(s.frames, token)
		close(frame.done)
	}
}
func (s *Service) cancelFrames(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, frame := range s.frames {
		if id == "" || frame.session == id {
			delete(s.frames, token)
			close(frame.done)
		}
	}
}
