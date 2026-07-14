package report

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSummarizeHTTP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.jsonl")

	data := `
{"schema_version":"aerorig.http_probe.v1","probe":"http","sample":1,"target_name":"local","target_url":"http://127.0.0.1/","method":"GET","duration_ms":3.0,"status_code":200,"bytes_read":10,"ok":true}
{"schema_version":"aerorig.http_probe.v1","probe":"http","sample":2,"target_name":"local","target_url":"http://127.0.0.1/","method":"GET","duration_ms":1.0,"status_code":200,"bytes_read":10,"ok":true}
{"schema_version":"aerorig.http_probe.v1","probe":"http","sample":3,"target_name":"bad","target_url":"http://127.0.0.1:1/","method":"GET","duration_ms":2.0,"bytes_read":0,"ok":false,"error":"connection refused"}
`

	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := SummarizeHTTP([]string{path})
	if err != nil {
		t.Fatal(err)
	}

	if s.SchemaVersion != HTTPSummarySchemaV1 {
		t.Fatalf("schema = %q", s.SchemaVersion)
	}
	if s.TotalSamples != 3 {
		t.Fatalf("total = %d, want 3", s.TotalSamples)
	}
	if s.OKSamples != 2 {
		t.Fatalf("ok = %d, want 2", s.OKSamples)
	}
	if s.FailedSamples != 1 {
		t.Fatalf("failed = %d, want 1", s.FailedSamples)
	}
	if s.SuccessRate != float64(2)/float64(3) {
		t.Fatalf("success rate = %v", s.SuccessRate)
	}
	if s.LatencyMS.MinMS != 1.0 {
		t.Fatalf("min = %v, want 1", s.LatencyMS.MinMS)
	}
	if s.LatencyMS.P50MS != 2.0 {
		t.Fatalf("p50 = %v, want 2", s.LatencyMS.P50MS)
	}
	if s.LatencyMS.P95MS != 3.0 {
		t.Fatalf("p95 = %v, want 3", s.LatencyMS.P95MS)
	}
	if len(s.StatusCodes) != 1 || s.StatusCodes[0].StatusCode != 200 || s.StatusCodes[0].Count != 2 {
		t.Fatalf("unexpected status counts: %+v", s.StatusCodes)
	}
	if len(s.Errors) != 1 || s.Errors[0].Count != 1 {
		t.Fatalf("unexpected errors: %+v", s.Errors)
	}
}

func TestSummarizeHTTPRejectsBadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")

	if err := os.WriteFile(path, []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := SummarizeHTTP([]string{path})
	if err == nil {
		t.Fatal("expected error")
	}
}
