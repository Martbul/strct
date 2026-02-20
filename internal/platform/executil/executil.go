// Each consuming package defines its own narrow interface. tunnel defines processRunner with only Run. wifi defines commander with Run, Output, CombinedOutput. These are unexported — they're an implementation detail. Both are satisfied by executil.Real{} and *executil.Mock without either package knowing about each other.
// Constructors get two versions:
// New(cfg Config, runner processRunner) *Service     ← testable, takes interface
// NewFromConfig(cfg *config.Config) *Service         ← for main.go, injects executil.Real{}
// Package executil provides an abstraction over os/exec so that packages
// that shell out to system tools (nmcli, iptables, frpc, etc.) can be
// tested without root access or real hardware.
//
// Usage pattern:
//
//  1. Each consuming package defines its own narrow interface (Go idiom).
//  2. That interface is satisfied by executil.Real in production
//     and executil.Mock (or a hand-rolled spy) in tests.
//  3. The consuming struct accepts the interface via its constructor.
//
// See tunnel.Service and wifi.RealWiFi for concrete examples.
package executil

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Runner is the shared interface. Consuming packages copy the subset of
// methods they actually need into their own local interface — this keeps
// dependencies minimal and mocks small.
type Runner interface {
	// Run executes a command and returns an error if it exits non-zero.
	Run(name string, args ...string) error

	// Output executes a command and returns its combined stdout output.
	// Returns an error if the command exits non-zero.
	Output(name string, args ...string) ([]byte, error)

	// CombinedOutput executes a command and returns stdout + stderr merged.
	CombinedOutput(name string, args ...string) ([]byte, error)
}

// Real executes commands via os/exec. This is the implementation injected
// in all non-test code.
type Real struct{}

func (Real) Run(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func (Real) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

func (Real) CombinedOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// Call records a single command invocation for assertion in tests.
type Call struct {
	Name string
	Args []string
}

// String returns a human-readable representation for test failure messages.
func (c Call) String() string {
	return c.Name + " " + strings.Join(c.Args, " ")
}

// MockResult lets you pre-program what a specific command should return.
type MockResult struct {
	Output []byte
	Err    error
}

// Mock records all commands that were run and lets you pre-program responses.
// It is safe to use from a single goroutine (tests are sequential).
//
// Example:
//
//	m := &executil.Mock{}
//	m.Expect("nmcli", executil.MockResult{Err: nil})
//	wifi := wifi.NewRealWiFi("wlan0", m)
//	// ... exercise code ...
//	m.AssertCalled(t, "nmcli con up Hotspot")
type Mock struct {
	// Calls records every command Run/Output/CombinedOutput was called with,
	// in order. Inspect this in your tests.
	Calls []Call

	// responses maps "name arg1 arg2..." → MockResult.
	// If no match is found, Run returns nil and Output returns ("", nil).
	responses map[string]MockResult
}

// Expect pre-programs a response for a specific command signature.
// The key is "name arg1 arg2 ..." — exact match on the full command string.
//
//	m.Expect("iptables -t nat -A PREROUTING ...", executil.MockResult{Err: errors.New("permission denied")})
func (m *Mock) Expect(command string, result MockResult) {
	if m.responses == nil {
		m.responses = make(map[string]MockResult)
	}
	m.responses[command] = result
}

func (m *Mock) record(name string, args []string) MockResult {
	m.Calls = append(m.Calls, Call{Name: name, Args: args})
	key := name
	if len(args) > 0 {
		key += " " + strings.Join(args, " ")
	}
	if r, ok := m.responses[key]; ok {
		return r
	}
	return MockResult{} 
}

func (m *Mock) Run(name string, args ...string) error {
	return m.record(name, args).Err
}

func (m *Mock) Output(name string, args ...string) ([]byte, error) {
	r := m.record(name, args)
	return r.Output, r.Err
}

func (m *Mock) CombinedOutput(name string, args ...string) ([]byte, error) {
	r := m.record(name, args)
	return r.Output, r.Err
}


// WasCalled reports whether the given command string was ever called.
// The command string is "name arg1 arg2 ..." — same format as Expect.
func (m *Mock) WasCalled(command string) bool {
	for _, c := range m.Calls {
		if c.String() == command {
			return true
		}
	}
	return false
}

func (m *Mock) AssertCalled(t interface {
	Helper()
	Errorf(string, ...any)
}, command string) {
	t.Helper()
	if !m.WasCalled(command) {
		var buf bytes.Buffer
		buf.WriteString(fmt.Sprintf("expected command %q to be called, but it was not.\n", command))
		buf.WriteString("calls made:\n")
		for _, c := range m.Calls {
			buf.WriteString("  " + c.String() + "\n")
		}
		t.Errorf(buf.String())
	}
}

func (m *Mock) AssertNotCalled(t interface {
	Helper()
	Errorf(string, ...any)
}, command string) {
	t.Helper()
	if m.WasCalled(command) {
		t.Errorf("expected command %q NOT to be called, but it was", command)
	}
}

func (m *Mock) CallCount(command string) int {
	count := 0
	for _, c := range m.Calls {
		if c.String() == command {
			count++
		}
	}
	return count
}