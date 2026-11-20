package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandParameterSafety(t *testing.T) {
	for _, command := range []string{"echo '{{p}}'", "echo \"{{p}}\"", "echo x{{p}}", "echo {{p}};", `echo \{{p}}`, "echo # {{p}}", "echo $(whoami) {{p}}", "echo $((1+1)) {{p}}", "cat <<EOF {{p}}", "echo `whoami` {{p}}", "echo {{bad-name}}", "echo {{missing"} {
		if _, err := CommandParameters(command); err == nil {
			t.Errorf("accepted unsafe template %q", command)
		}
	}
	command := "tail -n {{lines}} -- {{path}}"
	output, err := RenderCommand(command, map[string]string{"lines": "10", "path": "a'; touch /tmp/x; '"})
	if err != nil {
		t.Fatal(err)
	}
	if output != "tail -n '10' -- 'a'\\''; touch /tmp/x; '\\'''" {
		t.Fatalf("unexpected rendering %s", output)
	}
	for _, params := range []map[string]string{{"lines": "10"}, {"lines": "10", "path": "ok", "extra": "bad"}, {"lines": "10", "path": "a\nb"}} {
		if _, err := RenderCommand(command, params); err == nil {
			t.Fatal("invalid params accepted")
		}
	}
	if _, err := RenderCommand("echo {{p}}", map[string]string{"p": strings.Repeat("a", 4097)}); err == nil {
		t.Fatal("oversized parameter accepted")
	}
}

func TestCommandStorePersistenceAndIsolation(t *testing.T) {
	dir := t.TempDir()
	s, err := NewCommandsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Save(CommandSnippet{Name: "日志", Command: "tail -n {{n}} -- {{path}}"})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewCommandsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := reopened.Render(c.ID, map[string]string{"n": "20", "path": "/tmp/a b"})
	if err != nil || output != "tail -n '20' -- '/tmp/a b'" {
		t.Fatal(output, err)
	}
	list := reopened.List()
	list[0].Command = "mutated"
	if reopened.List()[0].Command == "mutated" {
		t.Fatal("returned list aliases state")
	}
	if err = reopened.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if len(reopened.List()) != 0 {
		t.Fatal("delete not applied")
	}
	final, err := NewCommandsStore(dir)
	if err != nil || len(final.List()) != 0 {
		t.Fatal("delete not persisted", err)
	}
}

func TestCommandStoreCorruptionPreserved(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "commands.json")
	raw := []byte(`{"version":1,"commands":[{"id":"same","name":"a","command":"echo a"},{"id":"same","name":"b","command":"echo b"}]}`)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommandsStore(dir); err == nil {
		t.Fatal("duplicate id accepted")
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(raw, after) {
		t.Fatal("corrupt command file overwritten")
	}
}
