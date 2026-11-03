package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryuux05/godex/pkg/codegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testABI = `[{"type":"event","name":"Transfer","inputs":[{"name":"value","type":"uint256"}]}]`

func TestGenerateCommandDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, output, pkg, prefix string
		flags                     []string
	}{
		{"defaults", "generated.go", "generated", "Token", nil},
		{"package override", "generated.go", "token", "Token", []string{"-package", "token"}},
		{"output override", "generated/events.go", "generated", "Token", []string{"-out", "generated/events.go"}},
		{"type override", "generated.go", "generated", "ERC20", []string{"-type", "ERC20"}},
		{"all overrides", "generated/events.go", "token", "ERC20", []string{"-package", "token", "-out", "generated/events.go", "-type", "ERC20"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.WriteFile("token.json", []byte(testABI), 0644))
			args := append([]string{"generate", "-abi", "token.json"}, tc.flags...)
			var stdout, stderr bytes.Buffer
			require.NoError(t, run(args, &stdout, &stderr))
			source, err := os.ReadFile(tc.output)
			require.NoError(t, err)
			file, err := parser.ParseFile(token.NewFileSet(), tc.output, source, parser.AllErrors)
			require.NoError(t, err)
			assert.Equal(t, tc.pkg, file.Name.Name)
			assert.Contains(t, string(source), "type "+tc.prefix+"Transfer struct")
			assert.Empty(t, stdout.String())
		})
	}
}

func TestGenerateCommandInfersTypeFromFilename(t *testing.T) {
	for _, tc := range []struct{ filename, prefix string }{
		{"token.json", "Token"},
		{"ERC20.json", "ERC20"},
		{"erc20_abi.json", "Erc20"},
		{"contracts/ERC20_ABI.json", "ERC20"},
		{"my-token.abi.json", "MyToken"},
		{"my_token.json", "MyToken"},
		{"abi.json", "Abi"},
		{"123-token.json", "Contract123Token"},
		{"日本語.json", "Contract日本語"},
		{"---.json", "Contract"},
		{"Token", "Token"},
	} {
		t.Run(tc.filename, func(t *testing.T) {
			dir := t.TempDir()
			abiPath := filepath.Join(dir, tc.filename)
			require.NoError(t, os.MkdirAll(filepath.Dir(abiPath), 0755))
			require.NoError(t, os.WriteFile(abiPath, []byte(testABI), 0644))
			var stdout, stderr bytes.Buffer
			require.NoError(t, run([]string{"generate", "-abi", abiPath, "-out", "-"}, &stdout, &stderr))
			file, err := parser.ParseFile(token.NewFileSet(), "generated.go", stdout.Bytes(), parser.AllErrors)
			require.NoError(t, err)
			assert.Equal(t, "generated", file.Name.Name)
			assert.Contains(t, stdout.String(), "type "+tc.prefix+"Transfer struct")
			assert.Contains(t, stdout.String(), "type "+tc.prefix+"Handlers struct")
		})
	}
}

func TestGenerateCommandWritesAndRegenerates(t *testing.T) {
	dir := t.TempDir()
	abi := filepath.Join(dir, "token.json")
	require.NoError(t, os.WriteFile(abi, []byte(testABI), 0644))
	out := filepath.Join(dir, "bindings", "token_gen.go")
	args := []string{"generate", "-abi", abi, "-package", "bindings", "-type", "Token", "-out", out, "-event", "Transfer"}
	var stdout, stderr bytes.Buffer
	require.NoError(t, run(args, &stdout, &stderr))
	first, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(first, []byte(codegen.Header)))
	assert.Contains(t, string(first), "type TokenTransfer struct")
	assert.Empty(t, stdout.String())
	require.NoError(t, run(args, &stdout, &stderr))
	second, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, first, second)

	stdout.Reset()
	args[8] = "-"
	require.NoError(t, run(args, &stdout, &stderr))
	assert.Equal(t, first, stdout.Bytes())
}

func TestGenerateCommandPreservesExistingFilesOnFailure(t *testing.T) {
	dir := t.TempDir()
	abi, out := filepath.Join(dir, "abi.json"), filepath.Join(dir, "generated.go")
	require.NoError(t, os.WriteFile(abi, []byte(testABI), 0644))
	args := []string{"generate", "-abi", abi, "-package", "bindings", "-type", "Token", "-out", out}
	var stdout, stderr bytes.Buffer
	require.NoError(t, run(args, &stdout, &stderr))
	original, err := os.ReadFile(out)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(abi, []byte(`[{"type":"event","name":"Unsupported","inputs":[{"name":"items","type":"uint256[]"}]}]`), 0644))
	assert.Error(t, run(args, &stdout, &stderr))
	after, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, original, after)
	require.NoError(t, os.WriteFile(abi, []byte(testABI), 0644))
	require.NoError(t, os.WriteFile(out, []byte("package bindings\n// handwritten\n"), 0644))
	assert.ErrorContains(t, run(args, &stdout, &stderr), "refusing to overwrite")
	after, err = os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "package bindings\n// handwritten\n", string(after))
	link := filepath.Join(dir, "link.go")
	require.NoError(t, os.Symlink(out, link))
	assert.Error(t, writeGenerated(link, original))
	assert.Error(t, writeGenerated(filepath.Join(dir, "output.txt"), original))
}

func TestCommandHelpAndInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"generate", "--help"}} {
		var stdout, stderr bytes.Buffer
		require.NoError(t, run(args, &stdout, &stderr))
		assert.NotEmpty(t, stdout.String()+stderr.String())
	}
	for _, args := range [][]string{{"unknown"}, {"generate"}, {"generate", "-unknown"}, {"generate", "extra"}, {"generate", "-abi", "missing", "-package", "bindings", "-type", "Token", "-out", "missing.go"}} {
		var stdout, stderr bytes.Buffer
		assert.Error(t, run(args, &stdout, &stderr))
	}
}
