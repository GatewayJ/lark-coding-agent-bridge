package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const telemetryModuleEnv = "LARK_CHANNEL_TELEMETRY_MODULE"

type TelemetryLoaderOptions struct {
	Module     string
	NodeBinary string
	Env        []string
	Stderr     io.Writer
}

// LoadTelemetryAdapter starts the optional JavaScript telemetry adapter module
// used by the TypeScript CLI. It never lets module load or method failures
// break the host process; faulty adapters degrade to a best-effort no-op.
func LoadTelemetryAdapter(ctx context.Context, meta AdapterMeta, options TelemetryLoaderOptions) (TelemetryAdapter, error) {
	module := options.Module
	if module == "" {
		module = os.Getenv(telemetryModuleEnv)
	}
	if module == "" {
		return nil, nil
	}
	node := options.NodeBinary
	if node == "" {
		node = os.Getenv("LARK_CHANNEL_NODE")
	}
	if node == "" {
		node = "node"
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, node, "-e", nodeTelemetryBridgeScript)
	env := append([]string{}, os.Environ()...)
	env = append(env, options.Env...)
	env = append(env,
		telemetryModuleEnv+"="+module,
		"LARK_CHANNEL_TELEMETRY_META="+string(metaJSON),
	)
	cmd.Env = env
	if options.Stderr != nil {
		cmd.Stderr = options.Stderr
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	adapter := &nodeTelemetryAdapter{
		cmd:     cmd,
		stdin:   stdin,
		pending: map[uint64]chan struct{}{},
		done:    make(chan struct{}),
	}
	go adapter.readAcks(stdout)
	go func() {
		_ = cmd.Wait()
		adapter.closeDone()
	}()
	return adapter, nil
}

func LoadTelemetryAdapterFromEnv(ctx context.Context, meta AdapterMeta, stderr io.Writer) (TelemetryAdapter, error) {
	return LoadTelemetryAdapter(ctx, meta, TelemetryLoaderOptions{Stderr: stderr})
}

type nodeTelemetryAdapter struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	seq     atomic.Uint64
	closed  bool
	doneSet bool
	pending map[uint64]chan struct{}
	done    chan struct{}
}

func (a *nodeTelemetryAdapter) Emit(ctx context.Context, event TelemetryEvent) {
	_ = a.send(ctx, map[string]any{"kind": "emit", "event": event}, false)
}

func (a *nodeTelemetryAdapter) RecordError(ctx context.Context, err error, fields map[string]any) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	_ = a.send(ctx, map[string]any{
		"kind":   "recordError",
		"error":  message,
		"fields": sanitizeTelemetryContext(fields),
	}, false)
}

func (a *nodeTelemetryAdapter) RecordMetric(ctx context.Context, name string, value float64, tags map[string]string) {
	_ = a.send(ctx, map[string]any{
		"kind":  "recordMetric",
		"name":  name,
		"value": value,
		"tags":  sanitizeMetricTags(tags),
	}, false)
}

func (a *nodeTelemetryAdapter) Flush(ctx context.Context) error {
	return a.send(ctx, map[string]any{"kind": "flush", "timeoutMs": 2000}, true)
}

func (a *nodeTelemetryAdapter) Close(ctx context.Context) error {
	err := a.send(ctx, map[string]any{"kind": "close"}, true)
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		_ = a.stdin.Close()
	}
	a.mu.Unlock()
	select {
	case <-a.done:
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		}
	case <-time.After(2 * time.Second):
	}
	return err
}

func (a *nodeTelemetryAdapter) send(ctx context.Context, payload map[string]any, wait bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var id uint64
	var ack chan struct{}
	if wait {
		id = a.seq.Add(1)
		payload["id"] = id
		ack = make(chan struct{})
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	if wait {
		a.pending[id] = ack
	}
	_, err = a.stdin.Write(append(line, '\n'))
	if err != nil {
		if wait {
			delete(a.pending, id)
		}
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	if !wait {
		return nil
	}
	select {
	case <-ack:
		return nil
	case <-a.done:
		return nil
	case <-ctx.Done():
		a.removePending(id)
		return ctx.Err()
	case <-time.After(2 * time.Second):
		a.removePending(id)
		return nil
	}
}

func (a *nodeTelemetryAdapter) readAcks(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var ack struct {
			ID any `json:"id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &ack); err != nil {
			continue
		}
		id, ok := telemetryAckID(ack.ID)
		if !ok {
			continue
		}
		a.mu.Lock()
		ch := a.pending[id]
		delete(a.pending, id)
		a.mu.Unlock()
		if ch != nil {
			close(ch)
		}
	}
	a.closeDone()
}

func (a *nodeTelemetryAdapter) removePending(id uint64) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

func (a *nodeTelemetryAdapter) closeDone() {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		_ = a.stdin.Close()
	}
	for id, ch := range a.pending {
		delete(a.pending, id)
		close(ch)
	}
	if !a.doneSet {
		a.doneSet = true
		close(a.done)
	}
	a.mu.Unlock()
}

func telemetryAckID(value any) (uint64, bool) {
	switch typed := value.(type) {
	case float64:
		return uint64(typed), true
	case string:
		parsed, err := strconv.ParseUint(typed, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

const nodeTelemetryBridgeScript = `
const readline = require('node:readline');

function diag(event, fields) {
  console.warn('[telemetry.' + event + '] ' + JSON.stringify(fields));
}

function normalizeModuleSpecifier(specifier) {
  return specifier.startsWith('file:') ? specifier.replace(/%7E/gi, '~') : specifier;
}

function errorMessage(err) {
  return err && err.message ? err.message : String(err);
}

async function main() {
  const moduleName = process.env.LARK_CHANNEL_TELEMETRY_MODULE;
  const meta = JSON.parse(process.env.LARK_CHANNEL_TELEMETRY_META || '{}');
  let active = null;
  try {
    const imported = await import(normalizeModuleSpecifier(moduleName));
    const factory = imported.default || imported.createAdapter;
    if (typeof factory !== 'function') {
      diag('bad_module', { module: moduleName });
      return;
    }
    active = factory(meta);
    if (!active || typeof active.emit !== 'function') {
      diag('bad_adapter', { module: moduleName });
      return;
    }
  } catch (err) {
    diag('load_fail', { module: moduleName, err: errorMessage(err) });
    return;
  }

  async function call(method, args) {
    const fn = active && active[method];
    if (typeof fn !== 'function') return;
    try {
      await fn.apply(active, args);
    } catch (err) {
      diag('method_threw', { method, err: errorMessage(err) });
    }
  }

  const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
  for await (const line of rl) {
    if (!line.trim()) continue;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      continue;
    }
    if (msg.kind === 'emit') {
      await call('emit', [msg.event]);
    } else if (msg.kind === 'recordError') {
      await call('recordError', [msg.error, msg.fields || {}]);
    } else if (msg.kind === 'recordMetric') {
      await call('recordMetric', [msg.name, msg.value, msg.tags || {}]);
    } else if (msg.kind === 'flush') {
      await call('flush', [msg.timeoutMs]);
    } else if (msg.kind === 'close') {
      await call('close', []);
    }
    if (msg.id !== undefined) {
      console.log(JSON.stringify({ id: msg.id, ok: true }));
    }
    if (msg.kind === 'close') {
      return;
    }
  }
}

main().catch((err) => diag('helper_fail', { err: errorMessage(err) }));
`

var _ TelemetryAdapter = (*nodeTelemetryAdapter)(nil)
