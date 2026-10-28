package ext

import "testing"

func TestFrontmatter(t *testing.T) {
	n, d := frontmatter("---\nname: a\ndescription: >\n  line one\n  line two\nother: x\n---\nbody")
	if n != "a" || d != "line one line two" {
		t.Fatalf("%q %q", n, d)
	}
	n, d = frontmatter("---\nname: \"b\"\ndescription: 'one line'\n---\n")
	if n != "b" || d != "one line" {
		t.Fatalf("%q %q", n, d)
	}
}
