package vmoracle

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// OracleClient is a serialized client for the persistent jtron-oracle NDJSON process.
// Requests are serialized because java-tron's VMConfig is process-global and the oracle
// deliberately runs one deterministic execution at a time.
type OracleClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	scan   *bufio.Scanner
	mu     sync.Mutex
	close  sync.Once
	closed chan struct{}
}

// NewOracleClient starts command and connects its stdin/stdout to a persistent client.
// stderr is intentionally inherited so Gradle/JVM diagnostics never corrupt NDJSON stdout.
func NewOracleClient(command string, args ...string) (*OracleClient, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("oracle command is empty")
	}
	cmd := exec.Command(command, args...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("oracle stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("oracle stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start oracle: %w", err)
	}
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &OracleClient{cmd: cmd, stdin: stdin, scan: scan, closed: make(chan struct{})}, nil
}

// NewOracleClientFromEnv starts JTRON_ORACLE_COMMAND, or the repository launcher when the
// environment variable is absent. The default is useful for local development and is not used
// by ordinary unit tests.
func NewOracleClientFromEnv() (*OracleClient, error) {
	command := os.Getenv("JTRON_ORACLE_COMMAND")
	if command == "" {
		command = defaultOracleCommand()
	}
	return NewOracleClient(command)
}

func defaultOracleCommand() string {
	if _, err := os.Stat("tools/jtron-oracle/run.sh"); err == nil {
		return "tools/jtron-oracle/run.sh"
	}
	workingDir, err := os.Getwd()
	if err == nil {
		for directory := workingDir; directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
			candidate := filepath.Join(directory, "tools/jtron-oracle/run.sh")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return "tools/jtron-oracle/run.sh"
}

// Ping confirms the process is alive and reports an error response from the oracle as a Go error.
func (c *OracleClient) Ping() (map[string]string, error) {
	var response map[string]string
	if err := c.roundTrip(Request{Method: "ping"}, &response); err != nil {
		return nil, err
	}
	return response, nil
}

// Execute sends one fully specified world and returns java-tron's normalized execution.
func (c *OracleClient) Execute(world World, tx Tx) (Execution, error) {
	var response Execution
	if err := c.roundTrip(Request{World: &world, Tx: &tx}, &response); err != nil {
		return Execution{}, err
	}
	return response, nil
}

// Close shuts down the child process and releases its pipes. It is safe to call more than once.
func (c *OracleClient) Close() error {
	var err error
	c.close.Do(func() {
		close(c.closed)
		if closeErr := c.stdin.Close(); closeErr != nil {
			err = closeErr
		}
		if waitErr := c.cmd.Wait(); err == nil {
			err = waitErr
		}
	})
	return err
}

func (c *OracleClient) roundTrip(request Request, response any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return errors.New("oracle client is closed")
	default:
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal oracle request: %w", err)
	}
	if _, err := fmt.Fprintf(c.stdin, "%s\n", payload); err != nil {
		return fmt.Errorf("write oracle request: %w", err)
	}
	if !c.scan.Scan() {
		if err := c.scan.Err(); err != nil {
			return fmt.Errorf("read oracle response: %w", err)
		}
		return errors.New("oracle exited before response")
	}
	line := c.scan.Bytes()
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &failure); err != nil {
		return fmt.Errorf("decode oracle response: %w", err)
	}
	if failure.Error != "" {
		return errors.New(failure.Error)
	}
	if err := json.Unmarshal(line, response); err != nil {
		return fmt.Errorf("decode oracle result: %w", err)
	}
	return nil
}
