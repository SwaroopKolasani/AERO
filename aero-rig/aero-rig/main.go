package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aero-rig/internal/config"
	"aero-rig/internal/probe"
	"aero-rig/internal/report"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	var code int
	switch os.Args[1] {
	case "probe-http":
		code = runProbeHTTP(os.Args[2:])
	case "summary-http":
		code = runSummaryHTTP(os.Args[2:])
	case "probe-chat":
		code = runProbeChat(os.Args[2:])
	case "help", "-h", "--help":
		usage(os.Stdout)
		code = 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage(os.Stderr)
		code = 2
	}

	os.Exit(code)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "aerorig - Project Aero measurement harness")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  probe-http     measure HTTP reachability and latency")
	fmt.Fprintln(w, "  summary-http   summarize HTTP probe JSONL output")
	fmt.Fprintln(w, "  probe-chat     measure non-streaming OpenAI-compatible chat completion latency")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "examples:")
	fmt.Fprintln(w, "  aerorig probe-http -name aerocache -target http://127.0.0.1:8080/healthz -count 5")
	fmt.Fprintln(w, "  aerorig summary-http -in out/smoke_http.jsonl")
	fmt.Fprintln(w, "  aerorig probe-chat -name ollama -target http://127.0.0.1:11434/v1/chat/completions -model llama3.2:3b -prompt 'Say pong.' -count 3")
	fmt.Fprintln(w, "  aerorig probe-chat -name aerocache -target http://127.0.0.1:8080/v1/chat/completions -model tiny -prompt 'Say pong.' -count 2 -out out/chat.jsonl")
}

func runProbeHTTP(args []string) int {
	fs := flag.NewFlagSet("probe-http", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	name := fs.String("name", "target", "logical target name")
	target := fs.String("target", "", "http or https URL to probe")
	method := fs.String("method", http.MethodGet, "HTTP method")
	count := fs.Int("count", 1, "number of samples")
	timeoutRaw := fs.String("timeout", config.DefaultTimeout().String(), "per-sample timeout, for example 2s or 500ms")
	outPath := fs.String("out", config.DefaultOutputPath(), "optional JSONL output path")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *target == "" {
		fmt.Fprintln(os.Stderr, "-target is required")
		return 2
	}
	if *count <= 0 {
		fmt.Fprintln(os.Stderr, "-count must be greater than zero")
		return 2
	}

	timeout, err := time.ParseDuration(*timeoutRaw)
	if err != nil || timeout <= 0 {
		fmt.Fprintf(os.Stderr, "invalid -timeout: %s\n", *timeoutRaw)
		return 2
	}

	var w io.Writer = os.Stdout
	var f *os.File
	if *outPath != "" {
		if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
			return 1
		}
		f, err = os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create output file: %v\n", err)
			return 1
		}
		defer f.Close()
		w = f
	}

	client := &http.Client{Timeout: timeout}
	runID := newRunID()
	enc := json.NewEncoder(w)

	exitCode := 0
	for i := 1; i <= *count; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		res := probe.HTTP(ctx, client, probe.HTTPRequest{
			RunID:      runID,
			Sample:     i,
			TargetName: *name,
			TargetURL:  *target,
			Method:     *method,
		})
		cancel()

		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(os.Stderr, "write result: %v\n", err)
			return 1
		}
		if !res.OK {
			exitCode = 1
		}
	}

	return exitCode
}

func runSummaryHTTP(args []string) int {
	fs := flag.NewFlagSet("summary-http", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	inRaw := fs.String("in", "", "comma-separated JSONL input files")
	format := fs.String("format", "text", "output format: text or json")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	paths := splitCSV(*inRaw)
	paths = append(paths, fs.Args()...)

	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "-in or positional JSONL file is required")
		return 2
	}

	summary, err := report.SummarizeHTTP(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "summarize http: %v\n", err)
		return 1
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(summary); err != nil {
			fmt.Fprintf(os.Stderr, "write summary: %v\n", err)
			return 1
		}
	case "text":
		printHTTPSummary(os.Stdout, summary)
	default:
		fmt.Fprintf(os.Stderr, "unsupported -format: %s\n", *format)
		return 2
	}

	if summary.FailedSamples > 0 {
		return 1
	}

	return 0
}

func runProbeChat(args []string) int {
	fs := flag.NewFlagSet("probe-chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	name := fs.String("name", "target", "logical target name")
	target := fs.String("target", "", "OpenAI-compatible /v1/chat/completions URL")
	model := fs.String("model", "", "model name")
	prompt := fs.String("prompt", "", "user prompt")
	promptFile := fs.String("prompt-file", "", "path to prompt file")
	count := fs.Int("count", 1, "number of samples")
	timeoutRaw := fs.String("timeout", "30s", "per-sample timeout, for example 30s or 2m")
	apiKey := fs.String("api-key", os.Getenv("AERORIG_API_KEY"), "optional bearer token; defaults to AERORIG_API_KEY")
	outPath := fs.String("out", "", "optional JSONL output path")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *target == "" {
		fmt.Fprintln(os.Stderr, "-target is required")
		return 2
	}
	if *model == "" {
		fmt.Fprintln(os.Stderr, "-model is required")
		return 2
	}
	if *prompt == "" && *promptFile == "" {
		fmt.Fprintln(os.Stderr, "-prompt or -prompt-file is required")
		return 2
	}
	if *count <= 0 {
		fmt.Fprintln(os.Stderr, "-count must be greater than zero")
		return 2
	}

	finalPrompt := *prompt
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read prompt file: %v\n", err)
			return 1
		}
		finalPrompt = string(b)
	}

	timeout, err := time.ParseDuration(*timeoutRaw)
	if err != nil || timeout <= 0 {
		fmt.Fprintf(os.Stderr, "invalid -timeout: %s\n", *timeoutRaw)
		return 2
	}

	var w io.Writer = os.Stdout
	var f *os.File
	if *outPath != "" {
		if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
			return 1
		}
		f, err = os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create output file: %v\n", err)
			return 1
		}
		defer f.Close()
		w = f
	}

	client := &http.Client{Timeout: timeout}
	runID := newRunID()
	enc := json.NewEncoder(w)

	exitCode := 0
	for i := 1; i <= *count; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		res := probe.Chat(ctx, client, probe.ChatRequest{
			RunID:      runID,
			Sample:     i,
			TargetName: *name,
			TargetURL:  *target,
			Model:      *model,
			Prompt:     finalPrompt,
			APIKey:     *apiKey,
			Timeout:    timeout,
		})
		cancel()

		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(os.Stderr, "write result: %v\n", err)
			return 1
		}
		if !res.OK {
			exitCode = 1
		}
	}

	return exitCode
}

func printHTTPSummary(w io.Writer, s report.HTTPSummary) {
	fmt.Fprintln(w, "HTTP summary")
	fmt.Fprintf(w, "schema: %s\n", s.SchemaVersion)
	fmt.Fprintf(w, "files: %s\n", strings.Join(s.SourceFiles, ", "))
	fmt.Fprintf(w, "samples: %d\n", s.TotalSamples)
	fmt.Fprintf(w, "ok: %d\n", s.OKSamples)
	fmt.Fprintf(w, "failed: %d\n", s.FailedSamples)
	fmt.Fprintf(w, "success_rate: %.2f%%\n", s.SuccessRate*100.0)
	fmt.Fprintf(
		w,
		"latency_ms: min=%.3f p50=%.3f p95=%.3f max=%.3f avg=%.3f\n",
		s.LatencyMS.MinMS,
		s.LatencyMS.P50MS,
		s.LatencyMS.P95MS,
		s.LatencyMS.MaxMS,
		s.LatencyMS.AvgMS,
	)

	fmt.Fprintln(w, "status_codes:")
	if len(s.StatusCodes) == 0 {
		fmt.Fprintln(w, "  none")
	} else {
		for _, item := range s.StatusCodes {
			fmt.Fprintf(w, "  %d: %d\n", item.StatusCode, item.Count)
		}
	}

	fmt.Fprintln(w, "errors:")
	if len(s.Errors) == 0 {
		fmt.Fprintln(w, "  none")
	} else {
		for _, item := range s.Errors {
			fmt.Fprintf(w, "  %s: %d\n", item.Value, item.Count)
		}
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}

	return out
}

func newRunID() string {
	return fmt.Sprintf("%d-%d", time.Now().UTC().UnixNano(), os.Getpid())
}
