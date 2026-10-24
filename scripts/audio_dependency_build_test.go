package scripts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAudioBuildRunsNonExecutableModuleScripts(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("audio build integration test requires sh")
	}
	for _, tc := range []struct {
		entry string
		code  int
	}{
		{"bootstrap-static-audio-deps.sh", 0},
		{"build-ffmpeg-audio-static.sh", 0},
		{"bootstrap-static-audio-deps.sh", 7},
	} {
		t.Run(fmt.Sprintf("%s/exit%d", tc.entry, tc.code), func(t *testing.T) {
			root := t.TempDir()
			moduleDir := filepath.Join(root, "module cache")
			binDir := filepath.Join(root, "bin")
			buildDir := filepath.Join(root, "build temp")
			outputDir := filepath.Join(root, "audio output")
			for _, dir := range []string{filepath.Join(moduleDir, "scripts"), binDir, buildDir} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				tc.entry: `#!/bin/sh
set -eu
printf '%s\n' "$SRC_DIR" "$PREFIX"
exit "$AUDIO_TEST_EXIT_CODE"
`,
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(moduleDir, "scripts", name), []byte(content), 0444); err != nil {
					t.Fatal(err)
				}
			}
			goStub := `#!/bin/sh
set -eu
case "$1 $2" in
  'mod download') ;;
  'list -m') printf '%s\n' "$AUDIO_TEST_MODULE_DIR" ;;
  *) exit 99 ;;
esac
`
			if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(goStub), 0755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "run-audio-dependency-build.sh", tc.entry)
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"AUDIO_TEST_MODULE_DIR="+moduleDir,
				fmt.Sprintf("AUDIO_TEST_EXIT_CODE=%d", tc.code),
				"TMPDIR="+buildDir, "THIRD_PARTY_DIR="+outputDir,
				"SRC_DIR=", "PREFIX=", "MSYSTEM=",
			)
			output, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.code {
				t.Fatalf("unexpected exit: %v\n%s", err, output)
			}
			want := filepath.Join(outputDir, "src") + "\n" + filepath.Join(outputDir, "ffmpeg-audio") + "\n"
			if string(output) != want {
				t.Fatalf("build paths: got %q, want %q", output, want)
			}
			for name, content := range files {
				path := filepath.Join(moduleDir, "scripts", name)
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0444 {
					t.Fatalf("module permissions changed for %s: %v", name, err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != content {
					t.Fatalf("module contents changed for %s: %v", name, err)
				}
			}
			entries, err := os.ReadDir(buildDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unexpected temporary build files: %v %v", entries, err)
			}
		})
	}
}

func TestAudioBuildRejectsUnknownEntrypoint(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("audio build integration test requires sh")
	}
	cmd := exec.Command("sh", "run-audio-dependency-build.sh", "../../unknown.sh")
	output, err := cmd.CombinedOutput()
	if err == nil || cmd.ProcessState.ExitCode() != 2 || !strings.Contains(string(output), "usage:") {
		t.Fatalf("invalid entrypoint accepted: %v\n%s", err, output)
	}
}
