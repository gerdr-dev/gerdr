package native

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// PDFs for the read tool: their text, page by page. A user drops a PDF into the chat (it goes over as its local path)
// and most machines have no pdftotext / pypdf for the model to reach for. macOS has PDFKit, reachable from
// osascript's JavaScript without installing anything; elsewhere pdfText says there is no reader and the model uses
// whatever tool it can find.

// pdfJS prints every page's text, each under a "--- page N ---" line. Exits 2 for a file PDFKit can't open.
const pdfJS = `ObjC.import('PDFKit')
ObjC.import('stdlib')
function run(argv) {
  const doc = $.PDFDocument.alloc.initWithURL($.NSURL.fileURLWithPath(argv[0]))
  if (!doc || doc.isNil()) { $.exit(2) }
  const out = []
  for (let i = 0; i < doc.pageCount; i++) {
    const s = doc.pageAtIndex(i).string
    out.push('--- page ' + (i + 1) + ' ---\n' + (s && !s.isNil() ? s.js : ''))
  }
  return out.join('\n')
}`

var errNoPDFReader = errors.New("no built-in PDF reader on this system; try pdftotext or a PDF library via bash")

// pdfText is the text of the PDF at p, page by page. Empty pages (a scan) come back as just their marker.
func pdfText(ctx context.Context, p string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, errNoPDFReader
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", pdfJS, p).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 2 {
			return nil, fmt.Errorf("%s is not a PDF that can be opened (damaged or encrypted?)", p)
		}
		return nil, fmt.Errorf("reading the PDF failed: %v", err)
	}
	return out, nil
}

// pdfHasText: some page has more than its marker. A scanned PDF has none: the model should look at it another way.
func pdfHasText(b []byte) bool {
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "--- page ") {
			return true
		}
	}
	return false
}
