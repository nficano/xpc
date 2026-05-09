package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/arcp"
	"github.com/nficano/xpc/internal/profile"
	"github.com/nficano/xpc/internal/version"
)

// MCP protocol version this bridge speaks. ARCP envelopes ride below.
const mcpProtocolVersion = "2025-06-18"

func newMCPCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server that proxies tool calls to the agent.",
		Long: `Run an MCP server on stdio that adapts ARCP-shaped tool calls onto the
Model Context Protocol. Intended to be launched by an MCP client (Claude Code,
Claude Desktop, etc.) via its server config:

  {
    "mcpServers": {
      "xpc": { "command": "xpc", "args": ["mcp"] }
    }
  }

On startup the bridge dials the agent named by the active profile (or
--profile), opens an ARCP session, and asks the agent for its tool catalog
via a tools.list envelope. Each MCP tools/call is then translated into an
ARCP tool.invoke; ARCP stream.chunk envelopes surface to the client as
notifications/progress while the call is in flight.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := g.ResolveProfile()
			if err != nil {
				return err
			}
			conn, sid, err := dialAndOpen(p, g.Timeout)
			if err != nil {
				return err
			}
			defer func() {
				closeSession(conn, p.PSK, sid)
				_ = conn.Close()
			}()
			return runMCPBridge(cmd.Context(), p, conn, sid, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	return c
}

// ---- Bridge core ----------------------------------------------------------

type mcpBridge struct {
	profile   *profile.Profile
	conn      net.Conn
	sessionID string
	traceID   string

	stdin  *bufio.Reader
	stdout io.Writer

	stdoutMu sync.Mutex
	arcpMu   sync.Mutex

	tools []toolDescriptor

	callsMu sync.Mutex
	// arcp message id (the tool.invoke id) -> in-flight call
	calls map[string]*toolCall
	// arcp job id -> in-flight call (populated after job.accepted)
	jobs map[string]*toolCall
	// mcp request id (string form) -> in-flight call
	mcpReqs map[string]*toolCall
}

type toolDescriptor struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type toolCall struct {
	mcpReqID      json.RawMessage // raw so we can echo back unchanged
	mcpReqIDStr   string          // string form for map lookup
	progressToken interface{}     // from request _meta.progressToken
	toolName      string

	streamChannels map[string]string // stream_id -> channel
	stdout         strings.Builder
	stderr         strings.Builder

	resultPayload map[string]interface{}
	toolErr       *toolErrInfo
	terminal      string // "completed" | "failed" | "cancelled"

	progressSeq int

	done chan struct{}
}

type toolErrInfo struct {
	Code      string
	Message   string
	Retryable bool
}

func runMCPBridge(ctx context.Context, p *profile.Profile, conn net.Conn, sid string, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b := &mcpBridge{
		profile:   p,
		conn:      conn,
		sessionID: sid,
		traceID:   arcp.MustNewID(arcp.PrefixTrace),
		stdin:     bufio.NewReader(stdin),
		stdout:    stdout,
		calls:     map[string]*toolCall{},
		jobs:      map[string]*toolCall{},
		mcpReqs:   map[string]*toolCall{},
	}

	// Pre-fetch the tool catalog before we start serving MCP. If the agent
	// doesn't speak tools.list (older build, NACK), we surface an empty list.
	if err := b.fetchTools(ctx, 5*time.Second); err != nil {
		// Non-fatal: log to stderr and continue with an empty catalog so the
		// MCP client at least sees a working initialize handshake.
		fmt.Fprintln(os.Stderr, "xpc mcp: tools.list failed:", err)
	}

	readerErr := make(chan error, 1)
	go func() { readerErr <- b.arcpReadLoop(ctx) }()

	if err := b.mcpServe(ctx); err != nil {
		return err
	}
	// stdin closed; let the arcp reader exit on conn close in the deferred
	// cleanup. Don't block forever on it.
	select {
	case <-readerErr:
	case <-time.After(500 * time.Millisecond):
	}
	return nil
}

// ---- ARCP side ------------------------------------------------------------

func (b *mcpBridge) writeArcp(env *arcp.Envelope) error {
	if err := arcp.Sign(env, b.profile.PSK); err != nil {
		return err
	}
	b.arcpMu.Lock()
	defer b.arcpMu.Unlock()
	return arcp.WriteFrame(b.conn, env)
}

func (b *mcpBridge) fetchTools(ctx context.Context, timeout time.Duration) error {
	req := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeToolsList, arcp.FormatTimestamp(time.Now()))
	req.SessionID = b.sessionID
	req.TraceID = b.traceID
	if err := b.writeArcp(req); err != nil {
		return wrapConnection(err)
	}

	deadline := time.Now().Add(timeout)
	if dl, ok := b.conn.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = dl.SetReadDeadline(deadline)
		defer func() { _ = dl.SetReadDeadline(time.Time{}) }()
	}
	for {
		env, err := readSignedFrame(b.conn, b.profile.PSK, 0)
		if err != nil {
			return err
		}
		if env.Type == arcp.TypeNack {
			code, _ := env.Payload["code"].(string)
			msg, _ := env.Payload["message"].(string)
			return fmt.Errorf("agent rejected tools.list: %s: %s", code, msg)
		}
		if env.Type != arcp.TypeToolsResult || env.CorrelationID != req.ID {
			continue
		}
		b.tools = parseToolDescriptors(env.Payload)
		return nil
	}
}

func parseToolDescriptors(payload map[string]interface{}) []toolDescriptor {
	raw, _ := payload["tools"].([]interface{})
	out := make([]toolDescriptor, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := m["description"].(string)
		schema, _ := m["input_schema"].(map[string]interface{})
		if schema == nil {
			schema = map[string]interface{}{"type": "object"}
		}
		out = append(out, toolDescriptor{Name: name, Description: desc, InputSchema: schema})
	}
	return out
}

// arcpReadLoop reads envelopes from the agent and routes them to the
// matching toolCall. Returns when the connection is closed.
func (b *mcpBridge) arcpReadLoop(ctx context.Context) error {
	for {
		env, err := arcp.ReadFrame(b.conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := arcp.VerifySig(env, b.profile.PSK); err != nil {
			fmt.Fprintln(os.Stderr, "xpc mcp: auth failure on inbound envelope:", err)
			return err
		}
		b.routeEnvelope(env)
	}
}

func (b *mcpBridge) routeEnvelope(env *arcp.Envelope) {
	// Most envelopes carry a correlation_id pointing at the original
	// tool.invoke; job.* envelopes carry job_id.
	b.callsMu.Lock()
	var call *toolCall
	if env.CorrelationID != "" {
		call = b.calls[env.CorrelationID]
	}
	if call == nil && env.JobID != "" {
		call = b.jobs[env.JobID]
	}
	b.callsMu.Unlock()
	if call == nil {
		return
	}

	switch env.Type {
	case arcp.TypeJobAccepted:
		if env.JobID != "" {
			b.callsMu.Lock()
			b.jobs[env.JobID] = call
			b.callsMu.Unlock()
		}
	case arcp.TypeJobStarted:
		// informational
	case arcp.TypeStreamOpen:
		ch, _ := env.Payload["channel"].(string)
		call.streamChannels[env.StreamID] = ch
	case arcp.TypeStreamChunk:
		ch := call.streamChannels[env.StreamID]
		text, _ := env.Payload["delta"].(string)
		if text != "" {
			if ch == "stderr" {
				call.stderr.WriteString(text)
			} else {
				call.stdout.WriteString(text)
			}
			b.sendProgress(call, ch, text)
		}
	case arcp.TypeStreamClose, arcp.TypeStreamError:
		delete(call.streamChannels, env.StreamID)
	case arcp.TypeToolResult:
		call.resultPayload = env.Payload
	case arcp.TypeToolError:
		code, _ := env.Payload["code"].(string)
		msg, _ := env.Payload["message"].(string)
		retry, _ := env.Payload["retryable"].(bool)
		call.toolErr = &toolErrInfo{Code: code, Message: msg, Retryable: retry}
	case arcp.TypeJobCompleted:
		call.terminal = "completed"
		closeOnce(call.done)
	case arcp.TypeJobFailed:
		call.terminal = "failed"
		closeOnce(call.done)
	case arcp.TypeJobCancelled:
		call.terminal = "cancelled"
		closeOnce(call.done)
	}
}

func closeOnce(c chan struct{}) {
	defer func() { _ = recover() }()
	close(c)
}

// ---- MCP side -------------------------------------------------------------

type jsonrpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

const (
	jrpcParseError     = -32700
	jrpcInvalidRequest = -32600
	jrpcMethodNotFound = -32601
	jrpcInvalidParams  = -32602
	jrpcInternalError  = -32603
)

func (b *mcpBridge) mcpServe(ctx context.Context) error {
	for {
		line, err := b.stdin.ReadBytes('\n')
		if len(line) > 0 {
			b.handleMCPLine(ctx, line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (b *mcpBridge) handleMCPLine(ctx context.Context, line []byte) {
	var req jsonrpcReq
	if err := json.Unmarshal(line, &req); err != nil {
		b.sendError(nil, jrpcParseError, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		b.sendError(req.ID, jrpcInvalidRequest, "jsonrpc must be 2.0")
		return
	}

	switch req.Method {
	case "initialize":
		b.handleInitialize(req)
	case "notifications/initialized", "notifications/cancelled", "notifications/roots/list_changed":
		// Notifications carry no id; cancelled is handled separately.
		if req.Method == "notifications/cancelled" {
			b.handleCancelled(req)
		}
	case "tools/list":
		b.handleToolsList(req)
	case "tools/call":
		go b.handleToolsCall(ctx, req)
	case "ping":
		b.sendResult(req.ID, map[string]interface{}{})
	default:
		if len(req.ID) > 0 {
			b.sendError(req.ID, jrpcMethodNotFound, "method not found: "+req.Method)
		}
	}
}

func (b *mcpBridge) handleInitialize(req jsonrpcReq) {
	result := map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "xpc-mcp",
			"version": version.String(),
		},
	}
	b.sendResult(req.ID, result)
}

func (b *mcpBridge) handleToolsList(req jsonrpcReq) {
	b.sendResult(req.ID, map[string]interface{}{"tools": b.tools})
}

func (b *mcpBridge) handleCancelled(req jsonrpcReq) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
		Reason    string          `json:"reason"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return
	}
	key := string(p.RequestID)
	b.callsMu.Lock()
	call := b.mcpReqs[key]
	b.callsMu.Unlock()
	if call == nil {
		return
	}
	// Look up the agent-side job_id by walking the jobs map (small).
	var jobID string
	b.callsMu.Lock()
	for jid, c := range b.jobs {
		if c == call {
			jobID = jid
			break
		}
	}
	b.callsMu.Unlock()
	if jobID == "" {
		return
	}
	cancel := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeCancel, arcp.FormatTimestamp(time.Now()))
	cancel.SessionID = b.sessionID
	cancel.JobID = jobID
	cancel.Payload = map[string]interface{}{"job_id": jobID}
	_ = b.writeArcp(cancel)
}

func (b *mcpBridge) handleToolsCall(ctx context.Context, req jsonrpcReq) {
	var p struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
		Meta      struct {
			ProgressToken interface{} `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		b.sendError(req.ID, jrpcInvalidParams, "invalid params: "+err.Error())
		return
	}
	if p.Name == "" {
		b.sendError(req.ID, jrpcInvalidParams, "missing tool name")
		return
	}
	if p.Arguments == nil {
		p.Arguments = map[string]interface{}{}
	}

	invoke := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeToolInvoke, arcp.FormatTimestamp(time.Now()))
	invoke.SessionID = b.sessionID
	invoke.TraceID = b.traceID
	invoke.Payload = map[string]interface{}{
		"tool":      p.Name,
		"arguments": p.Arguments,
	}

	call := &toolCall{
		mcpReqID:       req.ID,
		mcpReqIDStr:    string(req.ID),
		progressToken:  p.Meta.ProgressToken,
		toolName:       p.Name,
		streamChannels: map[string]string{},
		done:           make(chan struct{}),
	}
	b.callsMu.Lock()
	b.calls[invoke.ID] = call
	if call.mcpReqIDStr != "" {
		b.mcpReqs[call.mcpReqIDStr] = call
	}
	b.callsMu.Unlock()

	defer func() {
		b.callsMu.Lock()
		delete(b.calls, invoke.ID)
		if call.mcpReqIDStr != "" {
			delete(b.mcpReqs, call.mcpReqIDStr)
		}
		for jid, c := range b.jobs {
			if c == call {
				delete(b.jobs, jid)
			}
		}
		b.callsMu.Unlock()
	}()

	if err := b.writeArcp(invoke); err != nil {
		b.sendError(req.ID, jrpcInternalError, "agent write failed: "+err.Error())
		return
	}

	select {
	case <-call.done:
	case <-ctx.Done():
		b.sendError(req.ID, jrpcInternalError, "context cancelled")
		return
	}

	b.sendResult(req.ID, buildToolCallResult(call))
}

// buildToolCallResult assembles the MCP tools/call response from the call's
// captured streams and tool.result/tool.error.
func buildToolCallResult(call *toolCall) map[string]interface{} {
	if call.terminal == "cancelled" {
		return map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "tool call cancelled"},
			},
			"isError": true,
		}
	}
	if call.toolErr != nil {
		text := fmt.Sprintf("%s: %s", call.toolErr.Code, call.toolErr.Message)
		return map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": text},
			},
			"isError": true,
		}
	}

	var sb strings.Builder
	if call.resultPayload != nil {
		// Render the result payload as compact JSON. For exec the agent
		// returns {"exit_code": N, "timed_out": bool}; for agent.info a
		// nested object. Keep the raw shape so an LLM can introspect it.
		raw, err := json.Marshal(call.resultPayload)
		if err == nil {
			sb.Write(raw)
		}
	}
	stdoutText := call.stdout.String()
	stderrText := call.stderr.String()
	if stdoutText != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("--- stdout ---\n")
		sb.WriteString(stdoutText)
	}
	if stderrText != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("--- stderr ---\n")
		sb.WriteString(stderrText)
	}
	text := sb.String()
	if text == "" {
		text = "(no output)"
	}

	out := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": text},
		},
	}
	if call.terminal == "failed" {
		out["isError"] = true
	}
	return out
}

// ---- MCP wire helpers -----------------------------------------------------

func (b *mcpBridge) sendResult(id json.RawMessage, result interface{}) {
	b.writeMCP(jsonrpcResp{JSONRPC: "2.0", ID: id, Result: result})
}

func (b *mcpBridge) sendError(id json.RawMessage, code int, msg string) {
	if id == nil {
		id = json.RawMessage("null")
	}
	b.writeMCP(jsonrpcResp{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: msg}})
}

func (b *mcpBridge) sendProgress(call *toolCall, channel, delta string) {
	if call.progressToken == nil {
		return
	}
	call.progressSeq++
	notification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params": map[string]interface{}{
			"progressToken": call.progressToken,
			"progress":      call.progressSeq,
			"message":       fmt.Sprintf("[%s] %s", channel, delta),
		},
	}
	b.writeMCP(notification)
}

func (b *mcpBridge) writeMCP(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	b.stdoutMu.Lock()
	defer b.stdoutMu.Unlock()
	_, _ = b.stdout.Write(data)
	_, _ = b.stdout.Write([]byte("\n"))
}
