package cmd

import "testing"

func TestSanitizeDriveName(t *testing.T) {
	cases := map[string]string{
		"":       "_",
		".":      "_",
		"..":     "_",
		"hello":  "hello",
		"a/b":    "a_b",
		"a\\b":   "a_b",
		"  foo ": "foo",
	}
	for input, expected := range cases {
		if got := sanitizeDriveName(input); got != expected {
			t.Fatalf("sanitizeDriveName(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestJoinDrivePath(t *testing.T) {
	if got := joinDrivePath("", "file"); got != "file" {
		t.Fatalf("joinDrivePath empty = %q", got)
	}
	if got := joinDrivePath("dir", "file"); got != "dir/file" {
		t.Fatalf("joinDrivePath dir = %q", got)
	}
}

func TestSummarizeDriveDu(t *testing.T) {
	items := []driveTreeItem{
		{ID: "f1", Path: "a", ParentID: "root", MimeType: driveMimeFolder, Depth: 1},
		{ID: "f2", Path: "a/b", ParentID: "f1", MimeType: driveMimeFolder, Depth: 2},
		{ID: "file1", Path: "a/file.txt", ParentID: "f1", MimeType: "text/plain", Size: 10},
		{ID: "file2", Path: "a/b/file2.txt", ParentID: "f2", MimeType: "text/plain", Size: 5},
	}

	summaries := summarizeDriveDu(items, "root", 1)
	if len(summaries) == 0 {
		t.Fatalf("expected summaries")
	}

	var rootSize int64
	var aSize int64
	for _, s := range summaries {
		if s.Path == "." {
			rootSize = s.Size
		}
		if s.Path == "a" {
			aSize = s.Size
		}
	}
	if rootSize != 15 {
		t.Fatalf("root size = %d, want 15", rootSize)
	}
	if aSize != 15 {
		t.Fatalf("a size = %d, want 15", aSize)
	}
}
