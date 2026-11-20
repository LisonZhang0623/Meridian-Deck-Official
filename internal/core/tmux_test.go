package core

import "testing"

func TestSSHTmuxTerminalReusesConnection(t *testing.T) {
	fixture := newSSHToolsFixture(t)
	manager, events, profile := sshTestConnect(t, fixture)
	for _, name := range []string{"", "-option", "session;other"} {
		if err := manager.NewTmuxTerminal("bad-tmux", "first", profile, name); err == nil {
			t.Fatalf("accepted invalid tmux name %q", name)
		}
	}

	const name = "project_01"
	const command = "exec tmux new-session -A -s 'project_01' -c '/'"
	for _, id := range []string{"tmux-first", "tmux-reattach"} {
		if err := manager.NewTmuxTerminal(id, "first", profile, name); err != nil {
			t.Fatal(err)
		}
		// The loopback fixture echoes exec requests and closes that PTY.
		sshTestEventually(t, func() bool { return events.output(id, command) })
		sshTestEventually(t, func() bool { return events.state(id, "disconnected") })
	}
	if fixture.connections.Load() != 1 || fixture.authentications.Load() != 1 || fixture.shells.Load() != 1 {
		t.Fatal("opening and reattaching tmux should use exec on the original SSH transport")
	}
	manager.Input("first", "original-terminal-live")
	sshTestEventually(t, func() bool { return events.output("first", "original-terminal-live") })
}
