package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/vterm"
)

// RunRequest is what an agent asks run_start for.
type RunRequest struct {
	// Folder is the project, or empty for the folder the server runs in.
	Folder, Device, Profile string
	// Yes allows the project's own command from .lazymobile.json, which the answer shows first.
	Yes   bool
	Flags []string
}

// PreparedRun is the project's tool and the running device it goes on.
type PreparedRun struct {
	Dir    string
	Argv   []string
	Device device.Device
	Title  string
}

const (
	maxRuns          = 3
	runCols, runRows = 140, 50
	defaultWait      = 10 * time.Second
	maxWait          = 2 * time.Minute
	settleAfter      = 3 * time.Second
	reloadSettles    = 1500 * time.Millisecond
	defaultTail      = 40
	maxTail          = 300
)

// runSession is a project's tool that an agent started. It runs on a terminal of its own, so its
// keys work, and it stops with the server.
type runSession struct {
	id      string
	device  device.Device
	command string
	started time.Time
	term    *vterm.Session
}

// runStore is the tools an agent has started.
type runStore struct {
	mu   sync.Mutex
	next int
	byID map[string]*runSession
}

func (r *runStore) add(sess *runSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	active := 0
	for _, existing := range r.byID {
		select {
		case <-existing.term.Done():
		default:
			active++
		}
	}
	if active >= maxRuns {
		return apperr.New(apperr.InvalidState, "%d runs are already going", active).WithHint("Stop one with run_stop first.")
	}
	if r.byID == nil {
		r.byID = map[string]*runSession{}
	}
	r.next++
	sess.id = fmt.Sprintf("run-%d", r.next)
	r.byID[sess.id] = sess
	return nil
}

func (r *runStore) get(id string) (*runSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess := r.byID[id]
	if sess == nil {
		return nil, apperr.New(apperr.NotFound, "No run is called %q", id).WithHint("run_start gives the id.")
	}
	return sess, nil
}

// stopAll ends every run that still goes, as the server ends.
func (r *runStore) stopAll() {
	r.mu.Lock()
	sessions := make([]*runSession, 0, len(r.byID))
	for _, sess := range r.byID {
		sessions = append(sessions, sess)
	}
	r.mu.Unlock()
	for _, sess := range sessions {
		sess.term.Stop()
	}
	for _, sess := range sessions {
		sess.term.Wait()
	}
}

// runView is what the run tools answer.
type runView struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Device   string `json:"device"`
	Command  string `json:"command"`
	Seconds  int    `json:"seconds"`
	Output   string `json:"output"`
}

func (sess *runSession) view(lines int) runView {
	v := runView{ID: sess.id, State: "running", Device: sess.device.Name, Command: sess.command,
		Seconds: int(time.Since(sess.started).Seconds()), Output: strings.Join(sess.term.Tail(lines), "\n")}
	select {
	case <-sess.term.Done():
		v.State = "exited"
		code := 0
		var exit *exec.ExitError
		if err := sess.term.Err(); errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			code = -1
		}
		v.ExitCode = &code
	default:
	}
	return v
}

// settle waits until the program has ended, or has been quiet for a moment after saying something,
// or limit has passed. A build can be quiet for long, so the agent reads again with run_status.
func (sess *runSession) settle(ctx context.Context, limit, quietFor time.Duration) {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	quiet := time.NewTimer(quietFor)
	defer quiet.Stop()
	spoke := len(sess.term.Tail(1)) > 0
	for {
		select {
		case <-sess.term.Done():
			return
		case <-deadline.C:
			return
		case <-ctx.Done():
			return
		case <-sess.term.Changed():
			spoke = true
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(quietFor)
		case <-quiet.C:
			if spoke {
				return
			}
			quiet.Reset(quietFor)
		}
	}
}

type runStartArgs struct {
	Folder      string   `json:"folder,omitempty" jsonschema:"the project's folder; the folder the server runs in when empty"`
	Device      string   `json:"device,omitempty" jsonschema:"an id, a name, or a unique part of a name; the only running simulator or emulator when empty"`
	Profile     string   `json:"profile,omitempty" jsonschema:"which way to run when the project has several, such as \"run dev\""`
	Flags       []string `json:"flags,omitempty" jsonschema:"flags for the project's own tool, such as --flavor dev"`
	Yes         bool     `json:"yes,omitempty" jsonschema:"allow the command from .lazymobile.json: the first answer shows it, and asks for this"`
	WaitSeconds int      `json:"wait_seconds,omitempty" jsonschema:"how long to wait for output before answering, up to 120; 10 when empty"`
}

func (s *Server) runStart(ctx context.Context, _ *mcp.CallToolRequest, in runStartArgs) (*mcp.CallToolResult, any, error) {
	if s.PrepareRun == nil {
		return nil, nil, apperr.New(apperr.Unsupported, "Running a project is not available in this server")
	}
	prepared, err := s.PrepareRun(ctx, RunRequest{Folder: in.Folder, Device: in.Device, Profile: in.Profile, Yes: in.Yes, Flags: in.Flags})
	if err != nil {
		return nil, nil, err
	}
	term, err := vterm.Start(prepared.Dir, prepared.Argv, os.Environ(), runCols, runRows)
	if err != nil {
		e := apperr.New(apperr.CommandFailed, "%s could not start: %v", prepared.Argv[0], err)
		e.Argv = prepared.Argv
		return nil, nil, e
	}
	sess := &runSession{device: prepared.Device, command: strings.Join(prepared.Argv, " "), started: time.Now(), term: term}
	if err := s.runs.add(sess); err != nil {
		term.Stop()
		term.Wait()
		return nil, nil, err
	}
	wait := defaultWait
	if in.WaitSeconds > 0 {
		wait = min(time.Duration(in.WaitSeconds)*time.Second, maxWait)
	}
	sess.settle(ctx, wait, settleAfter)
	return jsonText(sess.view(defaultTail))
}

type runStatusArgs struct {
	ID    string `json:"id" jsonschema:"the id run_start gave"`
	Lines int    `json:"lines,omitempty" jsonschema:"how many of the last lines of output, up to 300; 40 when empty"`
}

func (s *Server) runStatus(_ context.Context, _ *mcp.CallToolRequest, in runStatusArgs) (*mcp.CallToolResult, any, error) {
	sess, err := s.runs.get(in.ID)
	if err != nil {
		return nil, nil, err
	}
	lines := defaultTail
	if in.Lines > 0 {
		lines = min(in.Lines, maxTail)
	}
	return jsonText(sess.view(lines))
}

type runReloadArgs struct {
	ID      string `json:"id" jsonschema:"the id run_start gave"`
	Restart bool   `json:"restart,omitempty" jsonschema:"hot restart (R) instead of hot reload (r); Flutter has both"`
}

func (s *Server) runReload(ctx context.Context, _ *mcp.CallToolRequest, in runReloadArgs) (*mcp.CallToolResult, any, error) {
	sess, err := s.runs.get(in.ID)
	if err != nil {
		return nil, nil, err
	}
	select {
	case <-sess.term.Done():
		return nil, nil, apperr.New(apperr.InvalidState, "%s has ended", in.ID).WithHint("Start it again with run_start.")
	default:
	}
	key := "r"
	if in.Restart {
		key = "R"
	}
	sess.term.SendRaw([]byte(key))
	sess.settle(ctx, 20*time.Second, reloadSettles)
	return jsonText(sess.view(15))
}

type runStopArgs struct {
	ID string `json:"id" jsonschema:"the id run_start gave"`
}

func (s *Server) runStop(ctx context.Context, _ *mcp.CallToolRequest, in runStopArgs) (*mcp.CallToolResult, any, error) {
	sess, err := s.runs.get(in.ID)
	if err != nil {
		return nil, nil, err
	}
	sess.term.Stop()
	select {
	case <-sess.term.Done():
	case <-time.After(15 * time.Second):
	case <-ctx.Done():
	}
	return jsonText(sess.view(15))
}
