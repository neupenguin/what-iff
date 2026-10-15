package provider

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"
)

// samplingFieldAssign matches a provider request setting a sampling knob, as a struct field
// (`Temperature: ...`) or an assignment (`params.TopP = ...`).
var samplingFieldAssign = regexp.MustCompile(`(^|[\s.{,])(Temperature|TopP|TopK)\s*(:|=[^=])`)

// TestNoSamplingParamsInSource pins the rule in docs/ARCHITECTURE_SUMMARY.md: no request sets
// temperature, top_p or top_k. Models increasingly reject them (gpt-5.6-luna 400s on any
// temperature), so a value that works today breaks every call site on the next model upgrade.
func TestNoSamplingParamsInSource(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..") // internal/
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if samplingFieldAssign.MatchString(code) {
				offenders = append(offenders, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, offenders, "provider requests must not set sampling params; leave them at the provider default")
}

func TestBuildClaudeBetaMCPParams_DropsSamplingParams(t *testing.T) {
	t.Parallel()
	in := anthropic.MessageNewParams{
		Model:       "claude-sonnet-5",
		MaxTokens:   1024,
		Temperature: anthropic.Float(0.2),
		TopP:        anthropic.Float(0.9),
		TopK:        anthropic.Int(40),
	}
	out, err := buildClaudeBetaMCPParams(in, &ClaudeMCPConfig{})
	require.NoError(t, err)
	require.False(t, out.Temperature.Valid())
	require.False(t, out.TopP.Valid())
	require.False(t, out.TopK.Valid())
	require.Equal(t, in.Model, out.Model)
	require.Equal(t, in.MaxTokens, out.MaxTokens)
}
