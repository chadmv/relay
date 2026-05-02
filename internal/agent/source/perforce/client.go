package perforce

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Runner is the interface used to invoke p4 commands. Swappable in tests.
// cwd, when non-empty, sets the child process's working directory; pass ""
// for server-global operations that aren't tied to a specific workspace.
type Runner interface {
	Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error)
	Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error
}

// execRunner uses os/exec to invoke the p4 binary on PATH.
type execRunner struct{ binary string }

func newExecRunner() *execRunner { return &execRunner{binary: "p4"} }

func (e *execRunner) Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("p4 %s: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}

func (e *execRunner) Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, e.binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		onLine(sc.Text())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("p4 %s: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

// Client wraps p4 CLI invocations.
type Client struct {
	r Runner
}

// NewClient creates a Client that invokes the real p4 binary.
func NewClient() *Client { return &Client{r: newExecRunner()} }

// CreateStreamClient creates (or recreates) a stream-bound p4 client.
// If template is non-empty, uses -t <template> to inherit non-View fields.
func (c *Client) CreateStreamClient(ctx context.Context, name, root, stream, template string) error {
	args := []string{"client", "-o", "-S", stream}
	if template != "" {
		args = append(args, "-t", template)
	}
	args = append(args, name)
	spec, err := c.r.Run(ctx, "", args, nil)
	if err != nil {
		return err
	}

	// Override Root so the workspace points at our chosen on-disk dir.
	spec = setSpecField(spec, "Root", root)
	spec = setSpecField(spec, "Host", "")   // blank Host: portable across renames
	spec = setSpecField(spec, "Owner", "")  // let p4 default to the caller

	if _, err := c.r.Run(ctx, "", []string{"client", "-i"}, bytes.NewReader(spec)); err != nil {
		return err
	}
	return nil
}

// DeleteClient deletes the named p4 client.
func (c *Client) DeleteClient(ctx context.Context, name string) error {
	_, err := c.r.Run(ctx, "", []string{"client", "-d", name}, nil)
	return err
}

var changeFirstLine = regexp.MustCompile(`^Change (\d+) `)

// ResolveHead resolves a depot path to its head CL number via `p4 changes -m1`.
func (c *Client) ResolveHead(ctx context.Context, path string) (int64, error) {
	out, err := c.r.Run(ctx, "", []string{"changes", "-m1", path + "#head"}, nil)
	if err != nil {
		return 0, err
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	m := changeFirstLine.FindStringSubmatch(line)
	if m == nil {
		return 0, fmt.Errorf("could not parse %q", line)
	}
	return strconv.ParseInt(m[1], 10, 64)
}

// SyncStream runs `p4 -c <client> sync -q --parallel=4 <specs...>` from cwd,
// streaming lines to onLine.
func (c *Client) SyncStream(ctx context.Context, cwd, client string, specs []string, onLine func(string)) error {
	args := append([]string{"-c", client, "sync", "-q", "--parallel=4"}, specs...)
	return c.r.Stream(ctx, cwd, args, onLine)
}

// CreatePendingCL creates an empty pending changelist on the named client
// with the given description. Returns the new CL number.
func (c *Client) CreatePendingCL(ctx context.Context, cwd, client, description string) (int64, error) {
	spec, err := c.r.Run(ctx, cwd, []string{"-c", client, "change", "-o"}, nil)
	if err != nil {
		return 0, err
	}
	spec = setSpecField(spec, "Description", description)
	spec = removeSpecBlock(spec, "Files")
	out, err := c.r.Run(ctx, cwd, []string{"-c", client, "change", "-i"}, bytes.NewReader(spec))
	if err != nil {
		return 0, err
	}
	re := regexp.MustCompile(`Change (\d+) created`)
	m := re.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("unexpected change -i output: %s", out)
	}
	return strconv.ParseInt(string(m[1]), 10, 64)
}

// Unshelve unshelves files from sourceCL into targetCL on the named client.
func (c *Client) Unshelve(ctx context.Context, cwd, client string, sourceCL, targetCL int64) error {
	_, err := c.r.Run(ctx, cwd, []string{
		"-c", client,
		"unshelve",
		"-s", strconv.FormatInt(sourceCL, 10),
		"-c", strconv.FormatInt(targetCL, 10),
	}, nil)
	return err
}

// RevertCL reverts all files in the given pending CL.
func (c *Client) RevertCL(ctx context.Context, cl int64) error {
	_, err := c.r.Run(ctx, "", []string{"revert", "-c", strconv.FormatInt(cl, 10), "//..."}, nil)
	return err
}

// DeleteCL deletes an empty pending CL.
func (c *Client) DeleteCL(ctx context.Context, cl int64) error {
	_, err := c.r.Run(ctx, "", []string{"change", "-d", strconv.FormatInt(cl, 10)}, nil)
	return err
}

// PendingChangesByDescPrefix returns relay-owned pending CLs on this client
// whose description starts with the given prefix.
func (c *Client) PendingChangesByDescPrefix(ctx context.Context, client, prefix string) ([]int64, error) {
	out, err := c.r.Run(ctx, "", []string{"changes", "-c", client, "-s", "pending", "-l"}, nil)
	if err != nil {
		return nil, err
	}
	var cls []int64
	var current int64
	var inDesc bool
	for _, line := range strings.Split(string(out), "\n") {
		if m := changeFirstLine.FindStringSubmatch(line); m != nil {
			current, _ = strconv.ParseInt(m[1], 10, 64)
			inDesc = true
			continue
		}
		if inDesc && strings.TrimSpace(line) != "" && current != 0 {
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				cls = append(cls, current)
			}
			inDesc = false
			current = 0
		}
	}
	return cls, nil
}

// setSpecField updates or inserts a "Field:\tvalue\n" line in a p4 spec form.
func setSpecField(spec []byte, field, value string) []byte {
	var out bytes.Buffer
	re := regexp.MustCompile(fmt.Sprintf(`(?m)^%s:.*$`, regexp.QuoteMeta(field)))
	if re.Match(spec) {
		return re.ReplaceAll(spec, []byte(fmt.Sprintf("%s:\t%s", field, value)))
	}
	fmt.Fprintf(&out, "%s:\t%s\n", field, value)
	out.Write(spec)
	return out.Bytes()
}

// removeSpecBlock removes a multi-line indented block starting with "Field:".
func removeSpecBlock(spec []byte, field string) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(spec))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	skip := false
	for sc.Scan() {
		line := sc.Text()
		if skip {
			if line == "" || (line[0] != '\t' && line[0] != ' ') {
				skip = false
			} else {
				continue
			}
		}
		if strings.HasPrefix(line, field+":") {
			skip = true
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}
