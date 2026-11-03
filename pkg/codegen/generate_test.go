package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ryuux05/godex/pkg/core/decoder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const transferABI = `[{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256"}]}]`

func TestGenerationIsDeterministicAndAcceptsArtifacts(t *testing.T) {
	cfg := Config{Package: "bindings", Type: "Token"}
	first, err := Generate([]byte(transferABI), cfg)
	require.NoError(t, err)
	artifact := []byte(`{"abi":` + transferABI + `,"bytecode":"0x00"}`)
	second, err := Generate(artifact, cfg)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	_, err = parser.ParseFile(token.NewFileSet(), "generated.go", first, parser.AllErrors)
	require.NoError(t, err)

	var events decoder.ABI
	require.NoError(t, json.Unmarshal([]byte(transferABI), &events))
	events = append(events, decoder.ABIItem{Type: "event", Name: "Approval"})
	abi, err := json.Marshal(events)
	require.NoError(t, err)
	first, err = Generate(abi, cfg)
	require.NoError(t, err)
	events[0], events[1] = events[1], events[0]
	abi, err = json.Marshal(events)
	require.NoError(t, err)
	second, err = Generate(abi, cfg)
	require.NoError(t, err)
	assert.Equal(t, first, second, "ABI event order must not change generated source")
}

func TestGenerationRejectsInvalidOrUnsupportedBindings(t *testing.T) {
	for _, tc := range []struct{ name, abi string }{
		{"malformed", `[`}, {"empty", `[]`}, {"null", `null`}, {"artifact missing ABI", `{}`},
		{"functions only", `[{"type":"function","name":"balanceOf"}]`},
		{"anonymous", `[{"type":"event","name":"Hidden","anonymous":true}]`},
		{"arrays", `[{"type":"event","name":"Batch","inputs":[{"name":"values","type":"uint256[]"}]}]`},
		{"tuples", `[{"type":"event","name":"Batch","inputs":[{"name":"value","type":"tuple"}]}]`},
		{"unsupported bytes", `[{"type":"event","name":"Small","inputs":[{"name":"value","type":"bytes4"}]}]`},
		{"invalid integer", `[{"type":"event","name":"Small","inputs":[{"name":"value","type":"uint7"}]}]`},
		{"noncanonical integer", `[{"type":"event","name":"Small","inputs":[{"name":"value","type":"uint008"}]}]`},
		{"duplicate fields", `[{"type":"event","name":"Pair","inputs":[{"name":"value","type":"bool"},{"name":"value","type":"bool"}]}]`},
		{"Go field collision", `[{"type":"event","name":"Pair","inputs":[{"name":"token_id","type":"uint256"},{"name":"tokenId","type":"uint256"}]}]`},
		{"Go event collision", `[{"type":"event","name":"transfer"},{"type":"event","name":"Transfer"}]`},
		{"reserved name", `[{"type":"event","name":"Handlers"}]`},
		{"empty event name", `[{"type":"event","inputs":[]}]`},
		{"overloads", `[{"type":"event","name":"Changed","inputs":[{"name":"value","type":"uint8"}]},{"type":"event","name":"Changed","inputs":[{"name":"value","type":"uint256"}]}]`},
		{"too many topics", `[{"type":"event","name":"Topics","inputs":[{"name":"a","type":"bool","indexed":true},{"name":"b","type":"bool","indexed":true},{"name":"c","type":"bool","indexed":true},{"name":"d","type":"bool","indexed":true}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := Generate([]byte(tc.abi), Config{Package: "bindings", Type: "Token"})
			assert.Error(t, err)
			assert.Nil(t, source)
		})
	}
	for _, cfg := range []Config{{Type: "Token"}, {Package: "for", Type: "Token"}, {Package: "_", Type: "Token"}, {Package: "bindings"}, {Package: "bindings", Type: "lower"}, {Package: "bindings", Type: "Invalid-Name"}} {
		_, err := Generate([]byte(transferABI), cfg)
		assert.Error(t, err)
	}
}

func TestEventSelectionAndUnnamedArguments(t *testing.T) {
	abi := []byte(`[{"type":"event","name":"Changed","inputs":[{"name":"","type":"uint"}]},{"type":"event","name":"Changed","inputs":[{"name":"value","type":"address"}]},{"type":"event","name":"Unsupported","inputs":[{"name":"values","type":"tuple[]"}]}]`)
	source, err := Generate(abi, Config{Package: "bindings", Type: "Token", Events: []string{"Changed(uint256)"}})
	require.NoError(t, err)
	assert.Contains(t, string(source), "Arg0 *big.Int")
	assert.NotContains(t, string(source), "TokenUnsupported")
	for _, selectors := range [][]string{{"Changed"}, {"missing"}, {"Changed(uint256)", "Changed(address)"}, {"Changed(uint256)", "Changed(uint256)"}} {
		_, err := Generate(abi, Config{Package: "bindings", Type: "Token", Events: selectors})
		assert.Error(t, err)
	}
}

func TestGeneratedBindingsCompileAndRun(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	dir := t.TempDir()
	goMod := "module generatedbindings\n\ngo 1.24.0\n\nrequire github.com/ryuux05/godex v0.0.0\nreplace github.com/ryuux05/godex => " + strconv.Quote(filepath.ToSlash(root)) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644))
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.sum"), sums, 0644))
	var abi decoder.ABI
	require.NoError(t, json.Unmarshal([]byte(transferABI), &abi))
	all := decoder.ABIItem{Type: "event", Name: "All"}
	for _, field := range []struct{ name, typ string }{
		{"u8", "uint8"}, {"u16", "uint16"}, {"u32", "uint32"}, {"u64", "uint64"}, {"u24", "uint24"}, {"wide", "uint256"},
		{"i8", "int8"}, {"i16", "int16"}, {"i32", "int32"}, {"i64", "int64"}, {"i24", "int24"}, {"signed", "int256"},
		{"flag", "bool"}, {"account", "address"}, {"text", "string"}, {"blob", "bytes"}, {"hash", "bytes32"},
		{"indexedText", "string"}, {"indexedBlob", "bytes"}, {"", "bool"},
	} {
		all.Inputs = append(all.Inputs, decoder.ABIInput{Name: field.name, Type: field.typ, Indexed: field.name == "indexedText" || field.name == "indexedBlob"})
	}
	abi = append(abi, all, decoder.ABIItem{Type: "event", Name: "Ping"})
	data, err := json.Marshal(abi)
	require.NoError(t, err)
	source, err := Generate(data, Config{Package: "bindings", Type: "Token"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "token_gen.go"), source, 0644))
	fixture, err := os.ReadFile("testdata/bindings_test.go.txt")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bindings_test.go"), fixture, 0644))
	paths, err := filepath.Glob(filepath.Join(root, "examples", "*", "*abi.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for i, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		source, err := Generate(data, Config{Package: "bindings", Type: "Repo" + exportedName(filepath.Base(filepath.Dir(path)))})
		require.NoError(t, err, path)
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("repo%d_gen.go", i)), source, 0644))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOPROXY=off", "GOWORK=off")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "generated bindings must compile and execute in a consuming module:\n%s", output)
}

func TestCommittedPaymentBindingsAreCurrent(t *testing.T) {
	data, err := os.ReadFile("../../examples/payment-reconciler/erc20_abi.json")
	require.NoError(t, err)
	expected, err := Generate(data, Config{Package: "bindings", Type: "ERC20"})
	require.NoError(t, err)
	actual, err := os.ReadFile("../../examples/payment-reconciler/bindings/erc20_gen.go")
	require.NoError(t, err)
	assert.Equal(t, string(expected), string(actual), "run go generate ./examples/payment-reconciler after changing its ABI or the generator")
}
