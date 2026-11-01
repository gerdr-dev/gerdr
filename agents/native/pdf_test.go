package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// minimalPDF is a one-page PDF showing text (Helvetica), with a correct xref table.
func minimalPDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 24 Tf 72 720 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return []byte(b.String())
}

func TestPDFText(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if _, err := pdfText(context.Background(), "x.pdf"); err != errNoPDFReader {
			t.Fatalf("want errNoPDFReader, got %v", err)
		}
		return
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "a report.pdf") // a space in the path, like files users drop in
	if err := os.WriteFile(p, minimalPDF("secret ALPHA-42"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := pdfText(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, "--- page 1 ---") || !strings.Contains(s, "ALPHA-42") || !pdfHasText(b) {
		t.Fatalf("got %q", s)
	}

	bad := filepath.Join(dir, "bad.pdf")
	os.WriteFile(bad, []byte("not a pdf"), 0o644)
	if _, err := pdfText(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "can be opened") {
		t.Fatalf("bad pdf: %v", err)
	}
}

func TestPDFHasText(t *testing.T) {
	if pdfHasText([]byte("--- page 1 ---\n\n--- page 2 ---\n")) {
		t.Fatal("markers only is no text")
	}
	if !pdfHasText([]byte("--- page 1 ---\nhello\n")) {
		t.Fatal("want text")
	}
}
