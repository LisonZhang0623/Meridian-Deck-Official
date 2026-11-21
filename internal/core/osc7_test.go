package core

import (
	"golang.org/x/crypto/ssh"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the actual shell hook, including byte-wise escaping of a UTF-8 path.
func TestOSC7ShellHook(t *testing.T) {
	for _, name := range []string{"bash", "zsh"} {
		t.Run(name, func(t *testing.T) {
			shell, err := exec.LookPath(name)
			if runtime.GOOS == "windows" && name == "bash" {
				if git, err := exec.LookPath("git"); err == nil {
					candidate := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe")
					if _, err := os.Stat(candidate); err == nil {
						shell = candidate
					}
				}
			}
			if shell == "" {
				t.Skipf("%s unavailable: %v", name, err)
			}
			args := []string{"--noprofile", "--norc", "-i", "-c"}
			before := "PROMPT_COMMAND='echo existing'\n"
			verify := `[[ ${PROMPT_COMMAND[*]} == *"echo existing"* ]] && [[ ${PROMPT_COMMAND[*]} == *__meridian_report_directory* ]] || exit 41`
			if name == "zsh" {
				args = []string{"-f", "-i", "-c"}
				before = "precmd_functions=(existing_hook)\n"
				verify = `[[ ${precmd_functions[*]} = 'existing_hook __meridian_report_directory' ]] || exit 42`
			}
			script := before + osc7Hook + "\n" + osc7Hook + "\n" + verify + "\nPWD='/lab/实验 #%'\n__meridian_report_directory"
			command := exec.Command(shell, append(args, script)...)
			command.Env = append(os.Environ(), "ZDOTDIR="+t.TempDir(), "TERM=xterm-256color")
			output, err := command.Output()
			if err != nil {
				t.Fatalf("hook failed: %v %s", err, output)
			}
			if string(output) != "\x1b]7;file:///lab/%E5%AE%9E%E9%AA%8C%20%23%25\x1b\\" {
				t.Fatalf("unexpected OSC 7 output %q", output)
			}
		})
	}
}

func TestOSC7Loopback(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			fixture := newSSHToolsFixture(t, func(f *sshToolsFixture, _ *ssh.ServerConfig) { f.loginShell = shell })
			manager, _, _ := sshTestConnect(t, fixture)
			files := NewFiles(manager)
			client, err := manager.SFTP("first")
			if err != nil {
				t.Fatal(err)
			}
			const original = "# existing prompt\nexport DEMO=1"
			target := "/.bashrc"
			if shell == "zsh" {
				target = "/custom/.zshrc"
				if err = client.Mkdir("/custom"); err != nil {
					t.Fatal(err)
				}
			}
			if shell == "bash" {
				w, err := client.Create(target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = w.Write([]byte(original)); err != nil {
					t.Fatal(err)
				}
				if err = w.Close(); err != nil {
					t.Fatal(err)
				}
			}
			check, err := files.OSC7("first", "")
			if err != nil || check.Configured || check.Shell != shell {
				t.Fatalf("probe: %+v %v", check, err)
			}
			if shell == "fish" {
				if check.Path != "" {
					t.Fatal(check)
				}
				if _, err = files.OSC7("first", target); err == nil {
					t.Fatal("unexpected write support")
				}
				return
			}
			if check.Path != target {
				t.Fatalf("wrong target: %q", check.Path)
			}
			if shell == "zsh" {
				if _, err = client.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("probe created a config: %v", err)
				}
			}
			if _, err = files.OSC7("first", "/different"); err == nil {
				t.Fatal("accepted wrong path")
			}
			for range 2 {
				installed, err := files.OSC7("first", target)
				if err != nil || !installed.Configured {
					t.Fatalf("configure: %+v %v", installed, err)
				}
			}
			check, err = files.OSC7("first", "")
			if err != nil || !check.Configured {
				t.Fatalf("recheck: %+v %v", check, err)
			}
			doc, err := files.Preview("first", target)
			if err != nil || strings.Count(doc.Content, "# >>> Meridian Deck OSC 7 >>>") != 1 {
				t.Fatalf("duplicate block or read error: %v", err)
			}
			if shell == "bash" && !strings.HasPrefix(doc.Content, original+"\n") {
				t.Fatal("existing shell config was modified")
			}
			tampered := strings.Replace(doc.Content, "printf -v byte", "# custom edit", 1)
			if _, err = files.Save("first", target, tampered, doc.Version); err != nil {
				t.Fatal(err)
			}
			if _, err = files.OSC7("first", target); err == nil {
				t.Fatal("overwrote a modified block")
			}
		})
	}
}
