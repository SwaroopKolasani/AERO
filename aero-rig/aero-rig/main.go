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
	"time"

	"aero-rig/internal/config"
	"aero-rig/internal/probe"
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
	fmt.Fprintln(w, "  probe-http   measure HTTP reachability and latency")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "examples:")
	fmt.Fprintln(w, "  aerorig probe-http -name aerocache -target http://127.0.0.1:8080/healthz -count 5")
	fmt.Fprintln(w, "  aerorig probe-http -name ollama -target http://127.0.0.1:11434/api/tags -count 5 -out out/ollama.jsonl")
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

func newRunID() string {
	return fmt.Sprintf("%d-%d", time.Now().UTC().UnixNano(), os.Getpid())
}
