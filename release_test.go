package oapifly

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The next version follows the merged commit's conventional-commit type (#21): feat is a minor
// bump, a breaking change a minor bump while on 0.x and a major one from 1.0, anything else a
// patch.
func TestNextVersion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	script, err := filepath.Abs(filepath.Join("scripts", "next-version.sh"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		tag, subject, body, want string
	}{
		{"", "feat: the first one", "", "v0.1.0"},
		{"v0.21.2", "fix: a nullable enum lists null", "", "v0.21.3"},
		{"v0.21.2", "docs: say so", "", "v0.21.3"},
		{"v0.21.2", "feat: register operations in code", "", "v0.22.0"},
		{"v0.21.2", "feat(registry): a scoped feature", "", "v0.22.0"},
		{"v0.21.2", "feat!: write OpenAPI 3.1", "", "v0.22.0"},
		{"v0.21.2", "fix: closed schemas", "BREAKING CHANGE: structs are closed", "v0.22.0"},
		{"v1.4.2", "fix!: a breaking fix", "", "v2.0.0"},
		{"v1.4.2", "feat: a feature", "", "v1.5.0"},
		{"v0.9.0", "featuring: not a feat", "", "v0.9.1"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		git := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		git("init", "-q")
		git("commit", "-q", "--allow-empty", "-m", "chore: start")
		if c.tag != "" {
			git("tag", "-a", c.tag, "-m", c.tag)
			git("tag", "-a", "v0.0.1", "-m", "an older tag")
		}
		message := c.subject
		if c.body != "" {
			message += "\n\n" + c.body
		}
		git("commit", "-q", "--allow-empty", "-m", message)

		cmd := exec.Command("bash", script)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%q: %v\n%s", c.subject, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != c.want {
			t.Errorf("after %s, %q is %s, want %s", c.tag, c.subject, got, c.want)
		}
	}
}
